package fixture_test

import (
	"bytes"
	"errors"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/itsKontra/ddash/sdk/fixture"
)

// realSystem stands in for a homelab system: it answers with what a
// scrubber must change ("realname") and headers a fixture must never hold.
func realSystem(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Set-Cookie", "session=headersecret")
		w.Header().Set("X-Api-Key-Echo", r.Header.Get("X-Api-Key"))
		switch r.Method + " " + r.URL.Path {
		case "GET /api/v1/status":
			w.Write([]byte(`{"host": "realname"}`))
		case "POST /api/v1/apply":
			w.Write([]byte(`{"applied": true}`))
		case "GET /api/v1/broken":
			w.WriteHeader(http.StatusServiceUnavailable)
			w.Write([]byte(`{"error": "realname is down"}`))
		case "GET /nodes/realname/":
			w.Write([]byte(`{"node": "realname"}`))
		case "GET /api/v1/leaky":
			w.Write([]byte(`{"left": "LEAK"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// scrubName stands in for the scrubber: realname becomes node1, and LEAK
// can't be scrubbed.
func scrubName(file string, body []byte) (string, []byte, error) {
	if bytes.Contains(body, []byte("LEAK")) {
		return "", nil, errors.New("still holds LEAK")
	}
	return strings.ReplaceAll(file, "realname", "node1"), bytes.ReplaceAll(body, []byte("realname"), []byte("node1")), nil
}

func get(t *testing.T, method, url string) (int, string, http.Header) {
	t.Helper()
	req, _ := http.NewRequestWithContext(t.Context(), method, url, nil)
	req.Header.Set("X-Api-Key", "secret-key")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var b bytes.Buffer
	b.ReadFrom(resp.Body)
	return resp.StatusCode, b.String(), resp.Header
}

func files(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, _ := os.ReadFile(path)
		rel, _ := filepath.Rel(dir, path)
		out[filepath.ToSlash(rel)] = string(b)
		return nil
	})
	return out
}

func TestRecordProxiesTheRealSystemAndWritesScrubbedBodiesInTheScenarioLayout(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "recorded")
	rec := fixture.Record(t, dir, realSystem(t).URL, fixture.RecordOptions{Scrub: scrubName})

	code, body, header := get(t, "GET", rec.URL+"/api/v1/status?verbose=1")
	if code != 200 || body != `{"host": "realname"}` || header.Get("X-Api-Key-Echo") != "secret-key" {
		t.Errorf("GET status = %d %s %v: want the real answer, request headers passed on", code, body, header)
	}
	get(t, "POST", rec.URL+"/api/v1/apply")
	get(t, "GET", rec.URL+"/api/v1/broken")
	get(t, "GET", rec.URL+"/nodes/realname/")

	want := map[string]string{
		"api/v1/status.json":     `{"host": "node1"}`,
		"api/v1/apply.post.json": `{"applied": true}`,
		"api/v1/broken.503.json": `{"error": "node1 is down"}`,
		"nodes/node1/index.json": `{"node": "node1"}`,
	}
	got := files(t, dir)
	for file, body := range want {
		if got[file] != body {
			t.Errorf("%s = %q, want %q", file, got[file], body)
		}
	}
	if len(got) != len(want) {
		t.Errorf("wrote %v, want only %v", got, want)
	}
	if rec.Hits("GET /api/v1/status") != 1 {
		t.Errorf("hits = %d, want 1", rec.Hits("GET /api/v1/status"))
	}
}

func TestARecordingNeverHoldsHeaders(t *testing.T) {
	dir := t.TempDir()
	rec := fixture.Record(t, dir, realSystem(t).URL, fixture.RecordOptions{Scrub: scrubName})

	get(t, "GET", rec.URL+"/api/v1/status")

	for file, body := range files(t, dir) {
		if strings.Contains(body, "headersecret") || strings.Contains(body, "secret-key") || strings.Contains(body, "Set-Cookie") {
			t.Errorf("%s holds a header: %s", file, body)
		}
	}
}

func TestARecordingServesBackWhatWasRecorded(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "baseline")
	rec := fixture.Record(t, dir, realSystem(t).URL, fixture.RecordOptions{Scrub: scrubName})
	get(t, "GET", rec.URL+"/api/v1/broken")
	get(t, "POST", rec.URL+"/api/v1/apply")

	srv := fixture.Server(t, dir)

	if code, body, _ := get(t, "GET", srv.URL+"/api/v1/broken"); code != 503 || body != `{"error": "node1 is down"}` {
		t.Errorf("served %d %s", code, body)
	}
	if code, body, _ := get(t, "POST", srv.URL+"/api/v1/apply"); code != 200 || body != `{"applied": true}` {
		t.Errorf("served %d %s", code, body)
	}
}

func TestARefusedBodyIsNotWrittenAndFailsTheTest(t *testing.T) {
	dir := t.TempDir()
	r := &recorder{T: t}
	rec := fixture.Record(r, dir, realSystem(t).URL, fixture.RecordOptions{Scrub: scrubName})

	code, body, _ := get(t, "GET", rec.URL+"/api/v1/leaky")

	if code != 200 || body != `{"left": "LEAK"}` {
		t.Errorf("the client got %d %s, want the real answer anyway", code, body)
	}
	if got := files(t, dir); len(got) != 0 {
		t.Errorf("wrote %v, want nothing", got)
	}
	if len(r.errors) != 1 || !strings.Contains(r.errors[0], "api/v1/leaky") || !strings.Contains(r.errors[0], "still holds LEAK") {
		t.Errorf("reported %q, want one error naming the file and why", r.errors)
	}
}

func TestRecordingWithoutAScrubberWritesNothing(t *testing.T) {
	dir := t.TempDir()
	var refused []string
	srv := httptest.NewServer(fixture.Recorder(realSystem(t).URL, dir, fixture.RecordOptions{
		Refused: func(file string, err error) { refused = append(refused, file+": "+err.Error()) },
	}))
	defer srv.Close()

	get(t, "GET", srv.URL+"/api/v1/status")

	if got := files(t, dir); len(got) != 0 || len(refused) != 1 {
		t.Errorf("wrote %v, refused %v: want nothing written and one refusal", got, refused)
	}
}

func TestWriteCommandWritesACapturedCommandAsTheSSHServerReadsIt(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "baseline")
	err := fixture.WriteCommand(dir, "/usr/sbin/nginx -T", fixture.Output{
		Stdout: []byte("server_name realname;\n"), Stderr: []byte("test is successful\n"), Exit: 1,
	}, scrubName)
	if err != nil {
		t.Fatal(err)
	}

	want := map[string]string{
		"nginx_-T.stdout": "server_name node1;\n",
		"nginx_-T.stderr": "test is successful\n",
		"nginx_-T.exit":   "1\n",
	}
	if got := files(t, dir); len(got) != len(want) || got["nginx_-T.stdout"] != want["nginx_-T.stdout"] ||
		got["nginx_-T.stderr"] != want["nginx_-T.stderr"] || got["nginx_-T.exit"] != want["nginx_-T.exit"] {
		t.Errorf("wrote %q, want %q", got, want)
	}
}

func TestWriteCommandRefusesWhatTheScrubberRefuses(t *testing.T) {
	dir := t.TempDir()

	err := fixture.WriteCommand(dir, "nginx -T", fixture.Output{Stdout: []byte("LEAK")}, scrubName)

	if err == nil || len(files(t, dir)) != 0 {
		t.Errorf("err = %v, files %v: want a refusal and nothing written", err, files(t, dir))
	}
}
