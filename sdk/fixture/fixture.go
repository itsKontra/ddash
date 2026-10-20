// Package fixture fakes an external system's HTTP API from response files,
// for tests of a system Component's transport.
//
// A scenario is a directory under the Component's testdata/. A request maps
// to a file by its URL path, ignoring the query:
//
//	GET  /api/v2/status/gateways  →  api/v2/status/gateways.json        (200)
//	GET  /api/v2/status/gateways  →  api/v2/status/gateways.503.json    (503)
//	POST /api/v2/firewall/apply   →  api/v2/firewall/apply.post.json    (200)
//
// A scenario other than baseline holds only the files that differ: any file
// it lacks comes from the sibling baseline directory. Recorded bodies use
// only the scrubbed ranges 198.18.N.0/24, 203.0.113.0/24, 2001:db8::/32 and
// the domain home.example.
package fixture

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// Fake is an external system serving a scenario until the test ends.
type Fake struct {
	*httptest.Server

	t    testing.TB
	dirs []string // the scenario, then baseline

	mu   sync.Mutex
	hits map[string]int
}

// Server serves the scenario in dir, e.g. "testdata/unauthorized". A
// request without a file gets 404 and fails the test.
func Server(t testing.TB, dir string) *Fake {
	t.Helper()
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("fixture: %v", err)
	}
	s := &Fake{t: t, dirs: []string{dir}, hits: map[string]int{}}
	if baseline := filepath.Join(filepath.Dir(dir), "baseline"); filepath.Clean(dir) != baseline {
		s.dirs = append(s.dirs, baseline)
	}
	s.Server = httptest.NewServer(http.HandlerFunc(s.serve))
	t.Cleanup(s.Close)
	return s
}

// Hits counts the requests for "METHOD /path", queries ignored.
func (s *Fake) Hits(request string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.hits[request]
}

func (s *Fake) serve(w http.ResponseWriter, r *http.Request) {
	request := r.Method + " " + r.URL.Path
	s.mu.Lock()
	s.hits[request]++
	s.mu.Unlock()

	for _, dir := range s.dirs {
		file, code, ok := find(dir, r.Method, r.URL.Path)
		if !ok {
			continue
		}
		body, err := os.ReadFile(file)
		if err != nil {
			s.t.Errorf("fixture: %v", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		w.Write(body)
		return
	}
	s.t.Errorf("fixture: no file for %s in %v", request, s.dirs)
	http.NotFound(w, r)
}

var statusSuffix = regexp.MustCompile(`^\.(\d{3})\.json$`)

// find looks in dir for the file answering method urlPath, and its status.
func find(dir, method, urlPath string) (file string, code int, ok bool) {
	base := filepath.Join(dir, filepath.FromSlash(strings.TrimPrefix(urlPath, "/")))
	if method != http.MethodGet {
		base += "." + strings.ToLower(method)
	}
	if _, err := os.Stat(base + ".json"); err == nil {
		return base + ".json", http.StatusOK, true
	}
	matches, _ := filepath.Glob(globEscape(base) + ".[0-9][0-9][0-9].json")
	for _, m := range matches {
		if sm := statusSuffix.FindStringSubmatch(strings.TrimPrefix(m, base)); sm != nil {
			code, _ := strconv.Atoi(sm[1])
			return m, code, true
		}
	}
	return "", 0, false
}

var globMeta = regexp.MustCompile(`[*?\[\\]`)

func globEscape(p string) string { return globMeta.ReplaceAllString(p, `\$0`) }
