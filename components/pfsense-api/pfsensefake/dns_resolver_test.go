package pfsensefake_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/itsKontra/ddash/components/pfsense-api/pfsensefake"
)

func send(f *pfsensefake.Firewall, method, target, body string) (int, map[string]any) {
	w := httptest.NewRecorder()
	f.ServeHTTP(w, httptest.NewRequest(method, target, strings.NewReader(body)))
	var out map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return w.Code, out
}

func overrideNames(f *pfsensefake.Firewall) []string {
	var out []string
	for _, h := range f.HostOverrides() {
		out = append(out, h.Host)
	}
	return out
}

func dataOf(body map[string]any) map[string]any {
	data, _ := body["data"].(map[string]any)
	return data
}

// Like pfSense, a create re-sorts the Host Overrides by host, so the ones
// after the new one move down a position, and answers with the new id.
func TestCreatingAHostOverrideResortsThemByHost(t *testing.T) {
	f := baseline(t)

	code, body := send(f, http.MethodPost, "/api/v2/services/dns_resolver/host_override", `{"host": "dns", "domain": "home.example", "ip": ["198.18.1.53"], "descr": "AdGuard"}`)

	if code != http.StatusOK {
		t.Fatalf("POST = %d %v", code, body)
	}
	if id := dataOf(body)["id"]; id != float64(1) {
		t.Errorf("answered id %v, want 1", id)
	}
	if got, want := overrideNames(f), []string{"cloud", "dns", "docker", "nas", "nginx", "pfsense", "proxmox"}; !slices.Equal(got, want) {
		t.Errorf("Host Overrides = %v, want %v", got, want)
	}
	_, list := do(f, http.MethodGet, "/api/v2/services/dns_resolver/host_overrides")
	if nginx := list["data"].([]any)[4].(map[string]any); nginx["host"] != "nginx" || nginx["id"] != float64(4) {
		t.Errorf("GET lists %v at 4, want nginx with id 4", nginx)
	}
	if !f.DNSPending() {
		t.Error("a write without apply left nothing pending")
	}
	if _, state := do(f, http.MethodGet, "/api/v2/services/dns_resolver/apply"); dataOf(state)["applied"] != false {
		t.Errorf("GET apply = %v, want applied false", state["data"])
	}
	do(f, http.MethodPost, "/api/v2/services/dns_resolver/apply")
	if f.DNSPending() || !slices.Contains(f.Log(), pfsensefake.AppliedDNS) {
		t.Errorf("not applied: %v", f.Log())
	}
}

func aliasHosts(h pfsensefake.HostOverride) []string {
	var out []string
	for _, a := range h.Aliases {
		out = append(out, a.Host)
	}
	return out
}

// The fixtures keep the web UI's order of nginx's aliases; the first write
// to one of them sorts them all by host, and the answer's id is the new
// alias's sorted position.
func TestAddingAnAliasResortsItsParentsAliases(t *testing.T) {
	f := baseline(t)

	code, body := send(f, http.MethodPost, "/api/v2/services/dns_resolver/host_override/alias", `{"parent_id": 3, "host": "jellyfin", "domain": "home.example", "descr": ""}`)

	if code != http.StatusOK {
		t.Fatalf("POST = %d %v", code, body)
	}
	got := aliasHosts(f.HostOverrides()[3])
	if !slices.IsSorted(got) || !slices.Contains(got, "jellyfin") || len(got) != 24 {
		t.Errorf("nginx's aliases = %v, want the 23 and jellyfin sorted", got)
	}
	if id := dataOf(body)["id"]; id != float64(slices.Index(got, "jellyfin")) {
		t.Errorf("answered id %v, want jellyfin's sorted position %d", id, slices.Index(got, "jellyfin"))
	}
	if code, _ := send(f, http.MethodPost, "/api/v2/services/dns_resolver/host_override/alias", `{"parent_id": 3, "host": "jellyfin", "domain": "home.example"}`); code != http.StatusBadRequest {
		t.Errorf("a second jellyfin under nginx = %d, want 400", code)
	}
}

func TestAnInjectedFailureFailsOnlyThatWrite(t *testing.T) {
	f := baseline(t)
	f.FailDNSWrite(2)

	first, _ := send(f, http.MethodDelete, "/api/v2/services/dns_resolver/host_override?id=0", "")
	second, _ := send(f, http.MethodDelete, "/api/v2/services/dns_resolver/host_override?id=0", "")
	third, _ := send(f, http.MethodDelete, "/api/v2/services/dns_resolver/host_override?id=0", "")

	if first != http.StatusOK || second != http.StatusInternalServerError || third != http.StatusOK {
		t.Errorf("answers = %d %d %d, want 200 500 200", first, second, third)
	}
	if got, want := overrideNames(f), []string{"nas", "nginx", "pfsense", "proxmox"}; !slices.Equal(got, want) {
		t.Errorf("Host Overrides = %v, want %v", got, want)
	}
}

// A test changes Host Overrides as the Operator would in pfSense's web UI,
// and the API reads the result.
func TestHostOverridesChangedOutsideTheAPIReadBack(t *testing.T) {
	f := baseline(t)

	f.EditHostOverrides(func(all []pfsensefake.HostOverride) []pfsensefake.HostOverride {
		all[0].IPs = []string{"198.18.1.219"}
		all[2].Aliases = append(all[2].Aliases, pfsensefake.HostOverrideAlias{Host: "files", Domain: "home.example"})
		return append(all[:1], all[2:]...)
	})

	_, list := do(f, http.MethodGet, "/api/v2/services/dns_resolver/host_overrides")
	var got []string
	for _, h := range list["data"].([]any) {
		h := h.(map[string]any)
		line := h["host"].(string) + "=" + h["ip"].([]any)[0].(string)
		for _, a := range h["aliases"].([]any) {
			line += "," + a.(map[string]any)["host"].(string)
		}
		got = append(got, line)
	}
	want := []string{"cloud=198.18.1.219", "nas=198.18.2.10,files"}
	if len(got) != 5 || !slices.Equal(got[:2], want) {
		t.Errorf("Host Overrides = %v, want cloud re-addressed, docker gone and files an alias of nas", got)
	}
	if f.DNSPending() {
		t.Error("a change made in the web UI is pending, want it applied")
	}
}
