package npmfake_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/itsKontra/ddash/components/nginx-proxy-manager-api/npmfake"
)

func baseline(t *testing.T) *npmfake.NPM {
	t.Helper()
	f, err := npmfake.New("../testdata/baseline", npmfake.Options{Miss: func(r string, err error) { t.Errorf("unanswered %s: %v", r, err) }})
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func do(f *npmfake.NPM, token, method, target, body string) (int, any) {
	w := httptest.NewRecorder()
	r := httptest.NewRequest(method, target, strings.NewReader(body))
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	f.ServeHTTP(w, r)
	var out any
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return w.Code, out
}

func signIn(t *testing.T, f *npmfake.NPM) string {
	t.Helper()
	code, out := do(f, "", http.MethodPost, "/api/tokens", `{"identity":"ddash@home.example","secret":"`+npmfake.DefaultPassword+`"}`)
	token, _ := out.(map[string]any)["token"].(string)
	if code != http.StatusOK || token == "" {
		t.Fatalf("sign-in = %d %v", code, out)
	}
	return token
}

const grafana = `{"domain_names":["grafana2.home.example"],"forward_scheme":"http","forward_host":"198.18.1.204","forward_port":3000}`

// v2.10's schema has no trust_forwarded_proto (v2.14), and like every
// unknown field it is refused: a write echoing a read fails.
func TestABodyWithAFieldV210DoesntKnowIsRefused(t *testing.T) {
	f := baseline(t)
	token := signIn(t, f)

	for _, body := range []string{
		strings.Replace(grafana, "}", `,"trust_forwarded_proto":false}`, 1),
		strings.Replace(grafana, "}", `,"id":40}`, 1),
	} {
		code, out := do(f, token, http.MethodPost, "/api/nginx/proxy-hosts", body)
		if code != http.StatusBadRequest || !strings.Contains(out.(map[string]any)["error"].(map[string]any)["message"].(string), "additional properties") {
			t.Errorf("POST %s = %d %v, want 400 additional properties", body, code, out)
		}
	}
	if len(f.ProxyHosts()) != 23 {
		t.Errorf("a refused POST was saved")
	}
}

func TestFlagsReadZeroOrOneBeforeV212AndTrueOrFalseFrom(t *testing.T) {
	f := baseline(t)
	token := signIn(t, f)

	_, out := do(f, token, http.MethodGet, "/api/nginx/proxy-hosts/1", "")
	if v := out.(map[string]any)["ssl_forced"]; v != float64(1) {
		t.Errorf("v2.10 ssl_forced = %v, want 1", v)
	}
	f.SetVersion(2, 12, 0)
	_, out = do(f, token, http.MethodGet, "/api/nginx/proxy-hosts/1", "")
	if v := out.(map[string]any)["ssl_forced"]; v != true {
		t.Errorf("v2.12 ssl_forced = %v, want true", v)
	}
}

func TestASaveSortsTheDomainNames(t *testing.T) {
	f := baseline(t)
	token := signIn(t, f)

	code, out := do(f, token, http.MethodPost, "/api/nginx/proxy-hosts", strings.Replace(grafana, `["grafana2.home.example"]`, `["b.home.example","a.home.example"]`, 1))

	names := out.(map[string]any)["domain_names"].([]any)
	if code != http.StatusCreated || names[0] != "a.home.example" || names[1] != "b.home.example" {
		t.Errorf("POST = %d %v, want 201 with the names sorted", code, out)
	}
}

func TestATokenPastItsExpiryIsRefused(t *testing.T) {
	f := baseline(t)
	token := signIn(t, f)
	f.ExpireTokens()

	code, out := do(f, token, http.MethodGet, "/api/nginx/proxy-hosts", "")

	if code != http.StatusBadRequest || out.(map[string]any)["error"].(map[string]any)["message"] != "Token has expired" {
		t.Errorf("GET = %d %v, want 400 Token has expired", code, out)
	}
}
