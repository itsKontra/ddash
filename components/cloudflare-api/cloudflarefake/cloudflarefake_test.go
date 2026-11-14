package cloudflarefake_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/itsKontra/ddash/components/cloudflare-api/cloudflarefake"
)

func baseline(t *testing.T) *cloudflarefake.Cloudflare {
	t.Helper()
	f, err := cloudflarefake.New("../testdata/baseline", cloudflarefake.Options{Miss: func(r string, err error) { t.Errorf("unanswered %s: %v", r, err) }})
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func do(f *cloudflarefake.Cloudflare, method, target, body string) (int, map[string]any) {
	w := httptest.NewRecorder()
	r := httptest.NewRequest(method, target, strings.NewReader(body))
	r.Header.Set("Authorization", "Bearer "+cloudflarefake.DefaultToken)
	f.ServeHTTP(w, r)
	var out map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return w.Code, out
}

const configurations = "/accounts/" + cloudflarefake.AccountID + "/cfd_tunnel/" + cloudflarefake.TunnelID + "/configurations"

// Cloudflare refuses an ingress whose last rule isn't a catch-all, and
// keeps the configuration as it was.
func TestAPutWithoutTheCatchAllLastIsRefused(t *testing.T) {
	f := baseline(t)

	code, out := do(f, http.MethodPut, configurations, `{"config":{"ingress":[{"hostname":"a.home.example","service":"http://198.18.1.9:80"}]}}`)

	if code != http.StatusBadRequest || out["success"] != false {
		t.Errorf("PUT = %d %v, want 400", code, out)
	}
	if f.Version() != 7 || len(f.Ingress()) != 4 {
		t.Errorf("version %d with %d rules, want the baseline's 7 with 4", f.Version(), len(f.Ingress()))
	}
}

func TestAPutReplacesTheWholeConfigurationAndRaisesItsVersion(t *testing.T) {
	f := baseline(t)

	code, out := do(f, http.MethodPut, configurations, `{"config":{"ingress":[{"service":"http_status:404"}]}}`)

	result, _ := out["result"].(map[string]any)
	if code != http.StatusOK || result["version"] != 8.0 || len(f.Ingress()) != 1 {
		t.Errorf("PUT = %d %v; ingress %v; want version 8 with the catch-all alone", code, out, f.Ingress())
	}
}

func TestAWrongTokenIsAnAuthenticationError(t *testing.T) {
	f := baseline(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, configurations, nil)
	r.Header.Set("Authorization", "Bearer nope")

	f.ServeHTTP(w, r)

	if w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), "Authentication error") {
		t.Errorf("GET with a wrong token = %d %s", w.Code, w.Body)
	}
}
