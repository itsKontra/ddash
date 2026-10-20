// Package coretest runs the real ddash core in a test, driven through its
// HTTP API: a temp data dir, a local Release whose catalogue lists the given
// Plugins and Components, and the in-process launcher running real
// Components. External systems are faked at their transports.
//
//	h := coretest.New(t, coretest.Options{
//		Components: []sdk.Definition{dockerapi.Definition()},
//		Plugins:    []coretest.Plugin{{Name: "docker", Requires: []string{"docker-api"}}},
//	})
//	h.SeedConfig("docker-api", &dockerapipb.DockerApiConfig{...})
//	h.Install("docker")
//	h.WaitState("docker-api", runtime.OK)
//	res := h.Call("docker", "docker-api", "ListContainers", `{}`)
//
// It works inside a testing/synctest bubble.
package coretest

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/itsKontra/ddash/core"
	"github.com/itsKontra/ddash/core/auth"
	"github.com/itsKontra/ddash/core/router"
	"github.com/itsKontra/ddash/core/runtime"
	"github.com/itsKontra/ddash/sdk"
)

// Plugin is a Plugin in the test Release's catalogue.
type Plugin struct {
	Name     string
	Requires []string
}

// Options configure a Harness.
type Options struct {
	// Components are built into the in-process launcher and listed in the
	// catalogue.
	Components []sdk.Definition
	// Plugins are listed in the catalogue.
	Plugins []Plugin
	// Launcher replaces the in-process launcher of Components.
	Launcher runtime.Launcher
	// Actor every request is signed in as. Default "operator:test".
	Actor string
	// DataDir is reused instead of a fresh one, to restart a host.
	DataDir string
}

// Harness is one running core.
type Harness struct {
	Host *core.Host
	// DataDir is the host's /data.
	DataDir string

	t        testing.TB
	shutdown bool
}

// PublicURL is the public URL the harnessed host runs at.
const PublicURL = "https://ddash.home.example"

// New starts a core until the test ends.
func New(t testing.TB, opts Options) *Harness {
	t.Helper()
	if opts.Actor == "" {
		opts.Actor = "operator:test"
	}
	if opts.DataDir == "" {
		opts.DataDir = t.TempDir()
	}
	logger := slog.New(slog.NewTextHandler(t.Output(), nil))
	if opts.Launcher == nil {
		opts.Launcher = runtime.InProcess(logger, opts.Components...)
	}
	host, err := core.New(t.Context(), core.Options{
		DataDir:   opts.DataDir,
		Release:   release(t, opts),
		Launcher:  opts.Launcher,
		Auth:      auth.As(opts.Actor),
		PublicURL: PublicURL,
		Logger:    logger,
	})
	if err != nil {
		t.Fatal(err)
	}
	h := &Harness{Host: host, DataDir: opts.DataDir, t: t}
	t.Cleanup(h.Shutdown)
	host.Start()
	return h
}

// release writes the test Release's catalogue.json into a temp dir.
func release(t testing.TB, opts Options) runtime.Dir {
	cat := runtime.Catalogue{Release: sdk.Release}
	for _, p := range opts.Plugins {
		cat.Plugins = append(cat.Plugins, runtime.PluginEntry{Name: p.Name, Title: p.Name, Requires: p.Requires})
	}
	for _, d := range opts.Components {
		cat.Components = append(cat.Components, runtime.ComponentEntry{Name: d.Name(), Description: d.Description(), Requires: d.Requires()})
	}
	b, err := json.Marshal(cat)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "catalogue.json"), b, 0o600); err != nil {
		t.Fatal(err)
	}
	return runtime.Dir(dir)
}

// Shutdown shuts the host down as SIGTERM would. The test's cleanup does
// this too.
func (h *Harness) Shutdown() {
	if h.shutdown {
		return
	}
	h.shutdown = true
	if err := h.Host.Shutdown(context.Background()); err != nil {
		h.t.Errorf("shutdown: %v", err)
	}
}

// SeedConfig stores cfg as owner's config, as a saved form would.
func (h *Harness) SeedConfig(owner string, cfg proto.Message) {
	h.t.Helper()
	if err := h.Host.Config().Put(h.t.Context(), owner, cfg); err != nil {
		h.t.Fatal(err)
	}
}

// Install installs plugin.
func (h *Harness) Install(plugin string) {
	h.t.Helper()
	if err := h.Host.Runtime().Install(h.t.Context(), plugin); err != nil {
		h.t.Fatalf("install %s: %v", plugin, err)
	}
}

// Status returns the host's view of component.
func (h *Harness) Status(component string) runtime.Status {
	return h.Host.Runtime().Status()[component]
}

// WaitState waits until component is in state, failing the test after 10 s.
func (h *Harness) WaitState(component string, state runtime.State) {
	h.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		st := h.Status(component)
		if st.State == state {
			return
		}
		if time.Now().After(deadline) {
			h.t.Fatalf("%s is %s (%s), want %s", component, st.State, st.Message, state)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// Call makes a Connect unary JSON call from plugin's transport to
// component's method, as the shell would.
func (h *Harness) Call(plugin, component, method, body string) *httptest.ResponseRecorder {
	r := ConnectRequest(component, method, body)
	r.Header.Set(PluginHeader, plugin)
	return h.Do(r)
}

// PluginHeader names the calling Plugin on each request from its transport.
const PluginHeader = router.PluginHeader

// ConnectRequest is a Connect unary JSON request to component's method,
// naming no Plugin.
func ConnectRequest(component, method, body string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "/api/c/"+component+"/"+method, bytes.NewBufferString(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Connect-Protocol-Version", "1")
	return r
}

// Do serves r with the host's handler.
func (h *Harness) Do(r *http.Request) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	h.Host.Handler().ServeHTTP(w, r)
	return w
}
