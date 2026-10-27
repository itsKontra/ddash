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
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
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
	"github.com/itsKontra/ddash/core/auth/oidctest"
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
	// Keycloak, when set, makes the host sign in with this issuer instead
	// of signing every request in as Actor: requests then need SignIn first.
	Keycloak *oidctest.Issuer
	// TrustedProxy is the host's trusted reverse proxy (core.Options).
	TrustedProxy string
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
	keycloak *oidctest.Issuer
	// cookies is the browser's cookie jar, by name.
	cookies map[string]*http.Cookie
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
	hostOpts := core.Options{
		DataDir:      opts.DataDir,
		Release:      rel,
		Launcher:     opts.Launcher,
		Shell:        opts.Shell,
		PublicURL:    PublicURL,
		TrustedProxy: opts.TrustedProxy,
		Logger:       logger,
	}
	if opts.Keycloak != nil {
		oidc := opts.Keycloak.Config()
		hostOpts.OIDC = &oidc
	} else {
		hostOpts.Auth = auth.As(opts.Actor)
	}
	host, err := core.New(t.Context(), hostOpts)
	if err != nil {
		t.Fatal(err)
	}
	h := &Harness{
		Host: host, DataDir: opts.DataDir, t: t, actor: auth.ParseActor(opts.Actor),
		keycloak: opts.Keycloak, cookies: map[string]*http.Cookie{},
	}
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

// Stream makes a Connect server-streaming JSON call from plugin's transport
// to component's method, as the shell would, and decodes what streamed
// back. It fails the test unless the answer is a whole stream.
func (h *Harness) Stream(plugin, component, method, body string) Streamed {
	h.t.Helper()
	r := StreamRequest(component, method, body)
	r.Header.Set(PluginHeader, plugin)
	res := h.Do(r)
	s, err := ReadStream(res.Body)
	if err != nil {
		h.t.Fatalf("%s: status %d: %v", method, res.Code, err)
	}
	s.Status, s.Header = res.Code, res.Header()
	return s
}

// StreamRequest is a Connect server-streaming JSON request to component's
// method, body in its one envelope, naming no Plugin.
func StreamRequest(component, method, body string) *http.Request {
	env := binary.BigEndian.AppendUint32([]byte{0}, uint32(len(body)))
	r := httptest.NewRequest(http.MethodPost, "/api/c/"+component+"/"+method, bytes.NewReader(append(env, body...)))
	r.Header.Set("Content-Type", "application/connect+json")
	r.Header.Set("Connect-Protocol-Version", "1")
	return r
}

// Streamed is what a streamed call answered.
type Streamed struct {
	Status int
	Header http.Header
	// Messages are the messages' JSON, in order.
	Messages []string
	// Error is the end-of-stream's error; nil when the stream ended ok.
	Error *ConnectError
}

// ConnectError is a Connect error as its JSON carries it.
type ConnectError struct {
	Code    string
	Message string
	Details []struct{ Type, Value string }
}

// ReadStream reads a Connect stream from r: its messages, then its
// end-of-stream, which must come last.
func ReadStream(r io.Reader) (Streamed, error) {
	var s Streamed
	for {
		flags, msg, err := ReadEnvelope(r)
		if err != nil {
			return s, err
		}
		if flags == 0 {
			s.Messages = append(s.Messages, string(msg))
			continue
		}
		if flags != 2 {
			return s, fmt.Errorf("envelope flags %#x", flags)
		}
		var end struct{ Error *ConnectError }
		if err := json.Unmarshal(msg, &end); err != nil {
			return s, fmt.Errorf("end-of-stream %s: %v", msg, err)
		}
		if extra, _ := io.ReadAll(r); len(extra) > 0 {
			return s, fmt.Errorf("%d bytes after the end-of-stream", len(extra))
		}
		s.Error = end.Error
		return s, nil
	}
}

// ReadEnvelope reads one Connect envelope from r.
func ReadEnvelope(r io.Reader) (flags byte, msg []byte, err error) {
	var head [5]byte
	if _, err := io.ReadFull(r, head[:]); err != nil {
		return 0, nil, fmt.Errorf("envelope: %w", err)
	}
	msg = make([]byte, binary.BigEndian.Uint32(head[1:]))
	if _, err := io.ReadFull(r, msg); err != nil {
		return 0, nil, fmt.Errorf("envelope: %w", err)
	}
	return head[0], msg, nil
}

// CoreCall makes a Connect unary JSON call to the core API's method, as the
// shell would.
func (h *Harness) CoreCall(method, body string) *httptest.ResponseRecorder {
	return h.Do(CoreRequest(method, body))
}

// CoreRequest is a Connect unary JSON request to the core API's method.
func CoreRequest(method, body string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "/api/core/ddash.core.CoreService/"+method, bytes.NewBufferString(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Connect-Protocol-Version", "1")
	return r
}

// Get serves a GET of path, as the browser would.
func (h *Harness) Get(path string) *httptest.ResponseRecorder {
	return h.Do(httptest.NewRequest(http.MethodGet, path, nil))
}

// Do serves r with the host's handler. Like a browser, it sends the
// cookies the host set earlier (unless r carries its own) and keeps the
// ones this response sets.
func (h *Harness) Do(r *http.Request) *httptest.ResponseRecorder {
	if len(r.Cookies()) == 0 {
		for _, c := range h.cookies {
			r.AddCookie(c)
		}
	}
	w := httptest.NewRecorder()
	h.Host.Handler().ServeHTTP(w, r)
	for _, c := range w.Result().Cookies() {
		if c.MaxAge < 0 {
			delete(h.cookies, c.Name)
		} else {
			h.cookies[c.Name] = c
		}
	}
	return w
}

// Cookie returns the browser's cookie name, or nil.
func (h *Harness) Cookie(name string) *http.Cookie { return h.cookies[name] }

// ClearCookies empties the browser's cookie jar.
func (h *Harness) ClearCookies() { clear(h.cookies) }

// TrySignIn goes through sign-in as a browser would: /auth/login, the
// Keycloak issuer's login page as its User, then the callback. It returns
// the callback's response.
func (h *Harness) TrySignIn() *httptest.ResponseRecorder {
	h.t.Helper()
	if h.keycloak == nil {
		h.t.Fatal("SignIn needs Options.Keycloak")
	}
	login := h.Get(auth.LoginPath)
	if login.Code != http.StatusFound {
		h.t.Fatalf("%s: status %d, want a redirect to Keycloak: %s", auth.LoginPath, login.Code, login.Body)
	}
	return h.Get(h.keycloak.Authorize(login.Header().Get("Location")))
}

// SignIn signs the Keycloak issuer's User in, failing the test unless that
// gives the browser a session.
func (h *Harness) SignIn() {
	h.t.Helper()
	res := h.TrySignIn()
	if res.Code != http.StatusOK || h.Cookie(auth.SessionCookie) == nil {
		h.t.Fatalf("sign-in: status %d, session cookie %v: %s", res.Code, h.Cookie(auth.SessionCookie), res.Body)
	}
}
