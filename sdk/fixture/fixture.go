// Package fixture fakes an external system's HTTP API from response files,
// for tests of a system Component's transport.
//
// A scenario is a directory under the Component's testdata/. A request maps
// to a file by its URL path, ignoring the query:
//
//	GET  /api/v2/status/gateways  →  api/v2/status/gateways.json        (200)
//	GET  /api/v2/status/gateways  →  api/v2/status/gateways.503.json    (503)
//	POST /api/v2/firewall/apply   →  api/v2/firewall/apply.post.json    (200)
//	POST /                        →  index.post.json                    (200)
//
// A path ending in / is its directory's index file.
// A scenario other than baseline holds only the files that differ: any file
// it lacks comes from the sibling baseline directory. Recorded bodies use
// only the scrubbed ranges 198.18.N.0/24, 203.0.113.0/24, 2001:db8::/32 and
// the domain home.example.
//
// Record mode (Recorder, Record) is the other direction: a reverse proxy to
// the real system writing each response's body, never its headers, into a
// scenario in this layout after a Scrub (internal/scrub) rewrote it.
// WriteCommand does the same for a command an SSH fake answers. `make
// record` (internal/smoke) uses both.
package fixture

import (
	"fmt"
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
	s := &Fake{hits: map[string]int{}}
	scenario := Scenario(dir, func(request string, err error) { t.Errorf("fixture: %v", err) })
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.hits[r.Method+" "+r.URL.Path]++
		s.mu.Unlock()
		scenario.ServeHTTP(w, r)
	}))
	t.Cleanup(s.Close)
	return s
}

// Hits counts the requests for "METHOD /path", queries ignored.
func (s *Fake) Hits(request string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.hits[request]
}

// Scenario answers requests from the scenario in dir, falling back to its
// sibling baseline, outside a test too: fakelab serves baseline with it. A
// request without a file gets 404. miss, when not nil, hears of every
// request that got no file, and why.
func Scenario(dir string, miss func(request string, err error)) http.Handler {
	dirs := []string{dir}
	if baseline := filepath.Join(filepath.Dir(dir), "baseline"); filepath.Clean(dir) != baseline {
		dirs = append(dirs, baseline)
	}
	if miss == nil {
		miss = func(string, error) {}
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		request := r.Method + " " + r.URL.Path
		for _, dir := range dirs {
			file, code, ok := find(dir, r.Method, r.URL.Path)
			if !ok {
				continue
			}
			body, err := os.ReadFile(file)
			if err != nil {
				miss(request, err)
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(code)
			w.Write(body)
			return
		}
		miss(request, fmt.Errorf("no file for %s in %v", request, dirs))
		http.NotFound(w, r)
	})
}

var statusSuffix = regexp.MustCompile(`^\.(\d{3})\.json$`)

// find looks in dir for the file answering method urlPath, and its status.
func find(dir, method, urlPath string) (file string, code int, ok bool) {
	if strings.HasSuffix(urlPath, "/") {
		urlPath += "index"
	}
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
