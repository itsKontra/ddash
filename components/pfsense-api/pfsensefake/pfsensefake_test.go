package pfsensefake_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/itsKontra/ddash/components/pfsense-api/pfsensefake"
)

func baseline(t *testing.T) *pfsensefake.Firewall {
	t.Helper()
	f, err := pfsensefake.New("../testdata/baseline", pfsensefake.Options{Miss: func(r string, err error) { t.Errorf("unanswered %s: %v", r, err) }})
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func do(f *pfsensefake.Firewall, method, target string) (int, map[string]any) {
	w := httptest.NewRecorder()
	f.ServeHTTP(w, httptest.NewRequest(method, target, nil))
	var body map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	return w.Code, body
}

func macs(ms []pfsensefake.Mapping) []string {
	var out []string
	for _, m := range ms {
		out = append(out, m.MAC)
	}
	return out
}

// pfSense takes a DELETE's parameters from the query string; the ids after
// the deleted mapping move up one.
func TestDeleteRemovesAStaticMappingByPosition(t *testing.T) {
	f := baseline(t)

	code, body := do(f, http.MethodDelete, "/api/v2/services/dhcp_server/static_mapping?parent_id=lan&id=1")

	if code != http.StatusOK {
		t.Fatalf("DELETE = %d %v", code, body)
	}
	if data, _ := body["data"].(map[string]any); data["mac"] != "02:5e:00:00:10:02" {
		t.Errorf("answered %v, want the deleted mapping", body["data"])
	}
	if got, want := macs(f.StaticMappings("lan")), []string{"02:5E:00:00:10:01", "02:5e:00:00:10:03"}; !slices.Equal(got, want) {
		t.Errorf("mappings = %v, want %v", got, want)
	}
	if !f.Pending() {
		t.Errorf("a DELETE without apply left nothing pending")
	}
	do(f, http.MethodPost, "/api/v2/services/dhcp_server/apply")
	if f.Pending() {
		t.Errorf("still pending after the apply")
	}
}

func TestDeleteWithApplyTakesEffectAtOnce(t *testing.T) {
	f := baseline(t)

	do(f, http.MethodDelete, "/api/v2/services/dhcp_server/static_mapping?parent_id=lan&id=0&apply=true")

	if f.Pending() || !slices.Contains(f.Log(), pfsensefake.Applied) {
		t.Errorf("not applied: %v", f.Log())
	}
}

func TestDeleteOfAMissingMappingIsNotFound(t *testing.T) {
	f := baseline(t)

	for _, target := range []string{
		"/api/v2/services/dhcp_server/static_mapping?parent_id=lan&id=3",
		"/api/v2/services/dhcp_server/static_mapping?parent_id=lan",
		"/api/v2/services/dhcp_server/static_mapping?parent_id=opt9&id=0",
	} {
		if code, _ := do(f, http.MethodDelete, target); code != http.StatusNotFound {
			t.Errorf("DELETE %s = %d, want 404", target, code)
		}
	}
	if len(f.StaticMappings("lan")) != 3 {
		t.Errorf("a refused DELETE removed a mapping")
	}
}
