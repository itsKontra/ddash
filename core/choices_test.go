package core_test

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	adguardapi "github.com/itsKontra/ddash/components/adguard-api"
	"github.com/itsKontra/ddash/components/checks"
	"github.com/itsKontra/ddash/components/network"
	ntfyapi "github.com/itsKontra/ddash/components/ntfy-api"
	pfsenseapi "github.com/itsKontra/ddash/components/pfsense-api"
	"github.com/itsKontra/ddash/components/pfsense-api/pfsenseapipb"
	"github.com/itsKontra/ddash/core/coretest"
	"github.com/itsKontra/ddash/core/runtime"
	"github.com/itsKontra/ddash/sdk"
	"github.com/itsKontra/ddash/sdk/fixture"
)

var networkPlugin = coretest.Plugin{Name: "network", Requires: []string{"network", "pfsense-api", "adguard-api"}}

// formField returns the config form's field named name.
func formField(t *testing.T, form map[string]any, name string) map[string]any {
	t.Helper()
	fields, _ := form["fields"].([]any)
	for _, f := range fields {
		if f := f.(map[string]any); f["name"] == name {
			return f
		}
	}
	t.Fatalf("form has no field %s: %v", name, form)
	return nil
}

func TestAFieldsChoicesComeFromTheGetterItsChoicesFromNames(t *testing.T) {
	srv := fixture.Server(t, "../components/pfsense-api/testdata/baseline")
	h := coretest.New(t, coretest.Options{Components: []sdk.Definition{network.Definition(), pfsenseapi.Definition(), adguardapi.Definition(), checks.Definition(), ntfyapi.Definition()}, Plugins: []coretest.Plugin{networkPlugin}})
	h.SeedConfig("pfsense-api", &pfsenseapipb.PfsenseApiConfig{Url: srv.URL, ApiKey: "key"})
	h.Install("network")
	h.WaitState("pfsense-api", runtime.OK)
	h.WaitState("network", runtime.Unconfigured)

	field := formField(t, coreJSON(t, h, "GetConfigForm", `{"owner":"network"}`), "bypass_alias")

	// The address aliases, in pfSense's order, labelled with their
	// description when they have one.
	want := []any{
		map[string]any{"value": "DNS_SERVERS", "label": "DNS_SERVERS"},
		map[string]any{"value": "FRITZBOX_IP6", "label": "FRITZBOX_IP6"},
		map[string]any{"value": "DNS_SERVER", "label": "DNS_SERVER"},
		map[string]any{"value": "RFC1918", "label": "RFC1918 — RFC1918"},
		map[string]any{"value": "VPN_BYPASS", "label": "VPN_BYPASS"},
		map[string]any{"value": "WAN_SUBNETWORK", "label": "WAN_SUBNETWORK"},
		map[string]any{"value": "VictronAllow", "label": "VictronAllow"},
		map[string]any{"value": "KILL_SWITCH", "label": "KILL_SWITCH"},
		map[string]any{"value": "DDASH_SMOKE", "label": "DDASH_SMOKE — TEST"},
	}
	if diff := cmp.Diff(want, field["choices"]); diff != "" {
		t.Errorf("bypass_alias choices (-want +got):\n%s", diff)
	}
	if field["choicesNote"] != nil {
		t.Errorf("choices note %v, want none", field["choicesNote"])
	}
	if field := formField(t, coreJSON(t, h, "GetConfigForm", `{"owner":"network"}`), "reserved_range"); field["choices"] != nil || field["choicesNote"] != nil {
		t.Errorf("reserved_range = %v, want no choices and no note", field)
	}
}

func TestAFieldWhoseChoicesCantBeReadIsFreeTextWithANote(t *testing.T) {
	h := coretest.New(t, coretest.Options{Components: []sdk.Definition{network.Definition(), pfsenseapi.Definition(), adguardapi.Definition(), checks.Definition(), ntfyapi.Definition()}, Plugins: []coretest.Plugin{networkPlugin}})
	h.Install("network")
	h.WaitState("pfsense-api", runtime.Unconfigured)
	h.WaitState("network", runtime.Unconfigured)

	field := formField(t, coreJSON(t, h, "GetConfigForm", `{"owner":"network"}`), "bypass_alias")

	note, _ := field["choicesNote"].(string)
	if field["choices"] != nil || !strings.Contains(note, "pfsense-api") || !strings.Contains(note, "not configured") || !strings.Contains(note, "type") {
		t.Errorf("bypass_alias = %v, want no choices and a note that pfsense-api isn't configured, so type the value", field)
	}
}
