// Package fakegithub fakes the slice of GitHub's REST API that ddash's
// GitHub release source uses, at its HTTP transport: each published Release
// is a local Release dir, and its files are the release's assets.
//
//	gh := fakegithub.New("itsKontra/ddash")
//	gh.Publish("v1.0.0", dir) // catalogue.json, SHA256SUMS, binaries, bundles
//	src := runtime.GitHub(runtime.GitHubOptions{..., Client: gh.Client()})
//
// It answers in memory, so it works inside a testing/synctest bubble.
package fakegithub

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"sync"
)

// Fake is a GitHub serving one repo's releases.
type Fake struct {
	repo string

	mu       sync.Mutex
	releases map[string]string // tag → Release dir
	order    []string          // tags, oldest first; the last is latest
	assets   []asset           // by id
	offline  bool
	failing  map[string]bool // asset names whose download fails
	requests []Request
}

// Request is one request the fake received.
type Request struct {
	Method, Path string
	// Authorization is the request's Authorization header.
	Authorization string
}

type asset struct{ tag, name string }

// New returns a fake GitHub with no releases in repo ("owner/name").
func New(repo string) *Fake {
	return &Fake{repo: repo, releases: map[string]string{}, failing: map[string]bool{}}
}

// Publish makes dir's files the assets of release tag, which becomes the
// latest release.
func (f *Fake) Publish(tag, dir string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.releases[tag] = dir
	f.order = append(slices.DeleteFunc(f.order, func(t string) bool { return t == tag }), tag)
}

// SetOffline makes every request fail as if GitHub were unreachable.
func (f *Fake) SetOffline(offline bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.offline = offline
}

// FailAsset makes downloads of the asset name (of any release) answer 502.
func (f *Fake) FailAsset(name string, fail bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failing[name] = fail
}

// Requests returns the requests the fake received, oldest first.
func (f *Fake) Requests() []Request {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.requests)
}

// Client is an HTTP client whose every request the fake answers in memory.
func (f *Fake) Client() *http.Client { return &http.Client{Transport: transport{f}} }

type transport struct{ f *Fake }

func (t transport) RoundTrip(r *http.Request) (*http.Response, error) {
	if err := r.Context().Err(); err != nil {
		return nil, err
	}
	t.f.mu.Lock()
	offline := t.f.offline
	t.f.requests = append(t.f.requests, Request{Method: r.Method, Path: r.URL.Path, Authorization: r.Header.Get("Authorization")})
	t.f.mu.Unlock()
	if offline {
		return nil, fmt.Errorf("dial tcp %s: connect: network is unreachable", r.URL.Host)
	}
	served := r.Clone(r.Context())
	served.Host = r.URL.Host // a redirect's request leaves Host empty
	w := httptest.NewRecorder()
	t.f.serve(w, served)
	res := w.Result()
	res.Request = r
	return res, nil
}

const objectsHost = "objects.githubusercontent.example"

func (f *Fake) serve(w http.ResponseWriter, r *http.Request) {
	mux := http.NewServeMux()
	prefix := "GET api.github.com/repos/" + f.repo + "/releases/"
	mux.HandleFunc(prefix+"latest", f.latest)
	mux.HandleFunc(prefix+"tags/{tag}", f.release)
	mux.HandleFunc(prefix+"assets/{id}", f.assetRedirect)
	mux.HandleFunc("GET "+objectsHost+"/{id}", f.download)
	mux.ServeHTTP(w, r)
}

func (f *Fake) latest(w http.ResponseWriter, _ *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.order) == 0 {
		http.Error(w, `{"message":"Not Found"}`, http.StatusNotFound)
		return
	}
	writeJSON(w, map[string]any{"tag_name": f.order[len(f.order)-1]})
}

func (f *Fake) release(w http.ResponseWriter, r *http.Request) {
	tag := r.PathValue("tag")
	f.mu.Lock()
	defer f.mu.Unlock()
	dir, ok := f.releases[tag]
	if !ok {
		http.Error(w, `{"message":"Not Found"}`, http.StatusNotFound)
		return
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	var assets []map[string]any
	for _, e := range entries {
		id := len(f.assets)
		f.assets = append(f.assets, asset{tag: tag, name: e.Name()})
		assets = append(assets, map[string]any{
			"id":   id,
			"name": e.Name(),
			"url":  fmt.Sprintf("https://api.github.com/repos/%s/releases/assets/%d", f.repo, id),
		})
	}
	writeJSON(w, map[string]any{"tag_name": tag, "assets": assets})
}

// assetRedirect answers an asset download as GitHub does: with a redirect
// to its storage host, for Accept: application/octet-stream.
func (f *Fake) assetRedirect(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Accept") != "application/octet-stream" {
		http.Error(w, "want Accept: application/octet-stream", http.StatusNotAcceptable)
		return
	}
	if _, ok := f.asset(r.PathValue("id")); !ok {
		http.NotFound(w, r)
		return
	}
	http.Redirect(w, r, "https://"+objectsHost+"/"+r.PathValue("id"), http.StatusFound)
}

func (f *Fake) download(w http.ResponseWriter, r *http.Request) {
	a, ok := f.asset(r.PathValue("id"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	f.mu.Lock()
	failing, dir := f.failing[a.name], f.releases[a.tag]
	f.mu.Unlock()
	if failing {
		http.Error(w, "bad gateway", http.StatusBadGateway)
		return
	}
	b, err := os.ReadFile(filepath.Join(dir, a.name))
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	_, _ = w.Write(b)
}

func (f *Fake) asset(id string) (asset, bool) {
	n, err := strconv.Atoi(id)
	f.mu.Lock()
	defer f.mu.Unlock()
	if err != nil || n < 0 || n >= len(f.assets) {
		return asset{}, false
	}
	return f.assets[n], true
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
