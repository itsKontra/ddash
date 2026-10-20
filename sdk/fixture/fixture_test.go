package fixture_test

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/itsKontra/ddash/sdk/fixture"
)

func call(t *testing.T, method, url string) (int, string, string) {
	t.Helper()
	req, _ := http.NewRequestWithContext(t.Context(), method, url, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, resp.Header.Get("Content-Type"), strings.TrimSpace(string(body))
}

func TestServesOneFilePerResponse(t *testing.T) {
	srv := fixture.Server(t, "testdata/baseline")

	code, ctype, body := call(t, "GET", srv.URL+"/api/v1/status?verbose=1")

	if code != 200 || ctype != "application/json" || body != `{"status": "ok", "host": "fw.home.example"}` {
		t.Errorf("GET status = %d %q %s", code, ctype, body)
	}
}

func TestAScenarioOverridesBaselineAndInheritsTheRest(t *testing.T) {
	srv := fixture.Server(t, "testdata/broken")

	code, _, body := call(t, "GET", srv.URL+"/api/v1/status")
	if code != 503 || body != `{"error": "service unavailable"}` {
		t.Errorf("overridden status = %d %s, want the scenario's 503", code, body)
	}
	code, _, body = call(t, "GET", srv.URL+"/api/v1/hosts")
	if code != 200 || !strings.Contains(body, "media") {
		t.Errorf("inherited hosts = %d %s, want baseline's", code, body)
	}
}

func TestOtherMethodsNameTheirFile(t *testing.T) {
	srv := fixture.Server(&recorder{T: t}, "testdata/baseline")

	if code, _, body := call(t, "POST", srv.URL+"/api/v1/apply"); code != 200 || body != `{"applied": true}` {
		t.Errorf("POST apply = %d %s", code, body)
	}
	if code, _, _ := call(t, "GET", srv.URL+"/api/v1/apply"); code != 404 {
		t.Errorf("GET apply = %d, want 404: only POST has a file", code)
	}
}

func TestADirectoryPathIsItsIndexFile(t *testing.T) {
	srv := fixture.Server(t, "testdata/baseline")

	if code, _, body := call(t, "POST", srv.URL+"/"); code != 200 || body != `{"published": true}` {
		t.Errorf("POST / = %d %s, want index.post.json", code, body)
	}
}

func TestCountsHits(t *testing.T) {
	srv := fixture.Server(t, "testdata/baseline")

	call(t, "GET", srv.URL+"/api/v1/status")
	call(t, "GET", srv.URL+"/api/v1/status?again=1")

	if n := srv.Hits("GET /api/v1/status"); n != 2 {
		t.Errorf("hits = %d, want 2", n)
	}
}

// recorder captures what the fixture server reports to its test.
type recorder struct {
	*testing.T
	errors []string
}

func (r *recorder) Errorf(format string, args ...any) {
	r.errors = append(r.errors, fmt.Sprintf(format, args...))
}

func TestARequestWithoutAFileFailsTheTest(t *testing.T) {
	rec := &recorder{T: t}
	srv := fixture.Server(rec, "testdata/baseline")

	code, _, _ := call(t, "GET", srv.URL+"/api/v1/missing")

	if code != 404 {
		t.Errorf("code = %d, want 404", code)
	}
	if len(rec.errors) != 1 || !strings.Contains(rec.errors[0], "GET /api/v1/missing") {
		t.Errorf("reported %q, want one error naming the request", rec.errors)
	}
}
