package agentfake

import (
	"net/http"
	"strings"
	"sync"
)

// Registry is a fake image registry serving the fake Engines' registry
// digests: manifest HEADs behind a bearer token, as Docker Hub does, with
// 401 and 429 on request.
type Registry struct {
	engines []*Engine

	mu       sync.Mutex
	requests int
	limited  bool
	private  map[string]bool // repos that refuse anonymous tokens
}

// NewRegistry serves the images of engines.
func NewRegistry(engines ...*Engine) *Registry {
	return &Registry{engines: engines, private: map[string]bool{}}
}

// RateLimit makes every request answer 429 while on.
func (g *Registry) RateLimit(on bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.limited = on
}

// Private makes repo refuse ddash: its token request answers 401.
func (g *Registry) Private(repo string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.private[repo] = true
}

// Requests is how many requests it answered.
func (g *Registry) Requests() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.requests
}

func (g *Registry) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	g.mu.Lock()
	g.requests++
	limited := g.limited
	g.mu.Unlock()
	if limited {
		http.Error(w, `{"errors":[{"code":"TOOMANYREQUESTS"}]}`, http.StatusTooManyRequests)
		return
	}
	if r.URL.Path == "/token" {
		repo := strings.TrimSuffix(strings.TrimPrefix(r.URL.Query().Get("scope"), "repository:"), ":pull")
		g.mu.Lock()
		private := g.private[repo]
		g.mu.Unlock()
		if private {
			http.Error(w, `{"details":"incorrect username or password"}`, http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"token":"fake-token:` + repo + `"}`))
		return
	}
	rest, ok := strings.CutPrefix(r.URL.Path, "/v2/")
	repo, tag, found := strings.Cut(rest, "/manifests/")
	if !ok || !found || r.Method != http.MethodHead {
		http.NotFound(w, r)
		return
	}
	if r.Header.Get("Authorization") != "Bearer fake-token:"+repo {
		scheme := "http"
		if r.TLS != nil {
			scheme = "https"
		}
		w.Header().Set("WWW-Authenticate", `Bearer realm="`+scheme+"://"+r.Host+`/token",service="registry.fake",scope="repository:`+repo+`:pull"`)
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	for _, ref := range []string{repo + ":" + tag, strings.TrimPrefix(repo, "library/") + ":" + tag} {
		for _, e := range g.engines {
			if d, ok := e.RegistryDigest(ref); ok {
				w.Header().Set("Docker-Content-Digest", d)
				w.Header().Set("Content-Type", "application/vnd.oci.image.index.v1+json")
				w.WriteHeader(http.StatusOK)
				return
			}
		}
	}
	w.WriteHeader(http.StatusNotFound)
}
