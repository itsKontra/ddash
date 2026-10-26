package fixture

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// Scrub rewrites a recorded response before it is written: its file name,
// relative to the scenario with slashes, and its body. An error refuses the
// write. internal/scrub's Scrubber.Fixture is the one recording uses.
type Scrub func(file string, body []byte) (string, []byte, error)

// RecordOptions shape a recording.
type RecordOptions struct {
	// Scrub rewrites every file before it is written. Without it nothing
	// is written: a recording is never committed unscrubbed.
	Scrub Scrub
	// Transport reaches the real system; nil is http.DefaultTransport.
	Transport http.RoundTripper
	// Refused, when not nil, hears of every response not written, and why.
	Refused func(file string, err error)
}

// Recorder is a reverse proxy to the real system at upstream (e.g.
// "https://198.18.1.1") that writes each response's body, scrubbed, into
// the scenario dir as Scenario would serve it back: GET 200 to <path>.json,
// another status to <path>.<NNN>.json, another method to
// <path>.<method>.json. Headers are never written, and the query is
// ignored as Scenario ignores it. The client gets the real response.
func Recorder(upstream, dir string, opts RecordOptions) http.Handler {
	target, err := url.Parse(upstream)
	if err != nil || target.Host == "" {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, fmt.Sprintf("fixture: bad upstream %q", upstream), http.StatusBadGateway)
		})
	}
	refused := opts.Refused
	if refused == nil {
		refused = func(string, error) {}
	}
	return &httputil.ReverseProxy{
		Rewrite: func(r *httputil.ProxyRequest) {
			r.SetURL(target)
			// Let the transport ask for and undo compression itself, so
			// the body read below is plain.
			r.Out.Header.Del("Accept-Encoding")
		},
		Transport: opts.Transport,
		ModifyResponse: func(resp *http.Response) error {
			body, err := io.ReadAll(resp.Body)
			resp.Body.Close()
			if err != nil {
				return err
			}
			resp.Body = io.NopCloser(bytes.NewReader(body))
			file := File(resp.Request.Method, strings.TrimPrefix(resp.Request.URL.Path, target.Path), resp.StatusCode)
			if err := write(dir, file, body, opts.Scrub); err != nil {
				refused(file, err)
			}
			return nil
		},
	}
}

// Record serves a Recorder to upstream, recording into dir, until the test
// ends. A response not written fails the test.
func Record(t testing.TB, dir, upstream string, opts RecordOptions) *Fake {
	t.Helper()
	opts.Refused = func(file string, err error) { t.Errorf("fixture: %s not recorded: %v", file, err) }
	s := &Fake{hits: map[string]int{}}
	recorder := Recorder(upstream, dir, opts)
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.hits[r.Method+" "+r.URL.Path]++
		s.mu.Unlock()
		recorder.ServeHTTP(w, r)
	}))
	t.Cleanup(s.Close)
	return s
}

// File names the file answering method urlPath with code, as Scenario
// looks it up, relative to the scenario with slashes.
func File(method, urlPath string, code int) string {
	p := urlPath
	if strings.HasSuffix(p, "/") || p == "" {
		p += "index"
	}
	p = strings.TrimPrefix(path.Clean("/"+p), "/")
	if method != http.MethodGet {
		p += "." + strings.ToLower(method)
	}
	if code != http.StatusOK {
		p += "." + strconv.Itoa(code)
	}
	return p + ".json"
}

// write scrubs and writes one recorded body.
func write(dir, file string, body []byte, scrub Scrub) error {
	if scrub == nil {
		return errors.New("no scrubber: nothing is written unscrubbed")
	}
	name, out, err := scrub(file, body)
	if err != nil {
		return err
	}
	p := filepath.Join(dir, filepath.FromSlash(name))
	if rel, err := filepath.Rel(dir, p); err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("%s is outside the scenario", name)
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return os.WriteFile(p, out, 0o644)
}
