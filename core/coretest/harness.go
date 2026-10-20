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
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io/fs"
	"log/slog"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/itsKontra/ddash/core"
	"github.com/itsKontra/ddash/core/appstate"
	"github.com/itsKontra/ddash/core/auth"
	"github.com/itsKontra/ddash/core/router"
	"github.com/itsKontra/ddash/core/runtime"
	corepb "github.com/itsKontra/ddash/proto/ddash/core"
	"github.com/itsKontra/ddash/sdk"
)

// Plugin is a Plugin in the test Release's catalogue.
type Plugin struct {
	Name     string
	Requires []string
	// Title defaults to Name.
	Title, Description, Icon, Author string
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
	// ReleaseDir runs a Release dir built from the repo (internal/release)
	// instead of Components and Plugins: the host reads its catalogue.json,
	// and the default launcher is the subprocess one over its binaries.
	ReleaseDir string
	// Actor every request is signed in as. Default "operator:test".
	Actor string
	// DataDir is reused instead of a fresh one, to restart a host.
	DataDir string
	// Release names the test Release. Default sdk.Release.
	Release string
	// ReleaseFiles are written into the test Release dir, by file name,
	// e.g. Plugin bundles ("docker.plugin.js").
	ReleaseFiles map[string]string
	// Shell is the shell's built assets the host serves (web.Shell()).
	// Default: none.
	Shell fs.FS
	// Source replaces the test Release dir as the host's release source,
	// e.g. runtime.GitHub over a fakegithub publishing ReleaseDirs. The
	// in-process launcher still runs Components.
	Source runtime.ReleaseSource
}

// Harness is one running core.
type Harness struct {
	Host *core.Host
	// DataDir is the host's /data.
	DataDir string

	t        testing.TB
	actor    auth.Actor
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
	var rel runtime.ReleaseSource
	switch {
	case opts.ReleaseDir != "":
		rel = runtime.Dir(opts.ReleaseDir)
		if opts.Launcher == nil {
			opts.Launcher = runtime.Subprocess(t.Output())
		}
	default:
		rel = opts.Source
		if rel == nil {
			rel = runtime.Dir(ReleaseDir(t, opts))
		}
		if opts.Launcher == nil {
			opts.Launcher = runtime.InProcess(logger, opts.Components...)
		}
	}
	host, err := core.New(t.Context(), core.Options{
		DataDir:   opts.DataDir,
		Release:   rel,
		Launcher:  opts.Launcher,
		Auth:      auth.As(opts.Actor),
		Shell:     opts.Shell,
		PublicURL: PublicURL,
		Logger:    logger,
	})
	if err != nil {
		t.Fatal(err)
	}
	h := &Harness{Host: host, DataDir: opts.DataDir, t: t, actor: auth.ParseActor(opts.Actor)}
	t.Cleanup(h.Shutdown)
	host.Start()
	return h
}

// ReleaseDir lays out the test Release of opts in a temp dir, as release
// assets: catalogue.json listing opts.Components and opts.Plugins of
// Release opts.Release, a stand-in binary per Component (the in-process
// launcher ignores it), stand-in bundles per Plugin unless ReleaseFiles has
// them, the ReleaseFiles, and SHA256SUMS over them all.
func ReleaseDir(t testing.TB, opts Options) string {
	cat := runtime.Catalogue{Release: cmp.Or(opts.Release, sdk.Release)}
	for _, p := range opts.Plugins {
		cat.Plugins = append(cat.Plugins, runtime.PluginEntry{
			Name: p.Name, Title: cmp.Or(p.Title, p.Name), Description: p.Description,
			Icon: p.Icon, Author: p.Author, Requires: p.Requires,
		})
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
	files := maps.Clone(opts.ReleaseFiles)
	if files == nil {
		files = map[string]string{}
	}
	for _, d := range opts.Components {
		files[d.Name()] = "stand-in for " + d.Name() + " of Release " + cat.Release
	}
	for _, p := range opts.Plugins {
		for _, file := range []string{p.Name + ".plugin.js", p.Name + ".plugin.css"} {
			if _, ok := files[file]; !ok {
				files[file] = "/* " + file + " of Release " + cat.Release + " */"
			}
		}
	}
	var sums strings.Builder
	for _, name := range slices.Sorted(maps.Keys(files)) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(files[name]), 0o600); err != nil {
			t.Fatal(err)
		}
		fmt.Fprintf(&sums, "%x  %s\n", sha256.Sum256([]byte(files[name])), name)
	}
	if err := os.WriteFile(filepath.Join(dir, runtime.SumsFile), []byte(sums.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
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

// Install installs plugin, as the harness's actor, failing the test if it
// can't.
func (h *Harness) Install(plugin string) {
	h.t.Helper()
	if err := h.TryInstall(plugin); err != nil {
		h.t.Fatalf("install %s: %v", plugin, err)
	}
}

// TryInstall runs the core Action Install as the harness's actor.
func (h *Harness) TryInstall(plugin string) error {
	_, err := h.Host.Install(h.ctx(), &corepb.InstallRequest{Plugin: plugin})
	return err
}

// Uninstall runs the core Action Uninstall as the harness's actor and
// returns the orphans it leaves.
func (h *Harness) Uninstall(plugin string, deleteData bool) (orphans []string, err error) {
	resp, err := h.Host.Uninstall(h.ctx(), &corepb.UninstallRequest{Plugin: plugin, DeleteData: deleteData})
	return resp.GetOrphans(), err
}

// Restart runs the core Action Restart on component as the harness's actor.
func (h *Harness) Restart(component string) error {
	_, err := h.Host.Restart(h.ctx(), &corepb.RestartRequest{Component: component})
	return err
}

// ctx is the test's context, signed in as the harness's actor.
func (h *Harness) ctx() context.Context {
	return auth.WithActor(h.t.Context(), h.actor)
}

// AuditLog returns the Audit Log, newest first.
func (h *Harness) AuditLog() []appstate.Entry {
	h.t.Helper()
	entries, err := h.Host.AuditLog().Entries(h.t.Context())
	if err != nil {
		h.t.Fatal(err)
	}
	return entries
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

// CoreCall makes a Connect unary JSON call to the core API's method, as the
// shell would.
func (h *Harness) CoreCall(method, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodPost, "/api/core/ddash.core.CoreService/"+method, bytes.NewBufferString(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Connect-Protocol-Version", "1")
	return h.Do(r)
}

// Get serves a GET of path, as the browser would.
func (h *Harness) Get(path string) *httptest.ResponseRecorder {
	return h.Do(httptest.NewRequest(http.MethodGet, path, nil))
}

// Do serves r with the host's handler.
func (h *Harness) Do(r *http.Request) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	h.Host.Handler().ServeHTTP(w, r)
	return w
}
