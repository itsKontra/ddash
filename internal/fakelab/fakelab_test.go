package fakelab_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/itsKontra/ddash/components/network"
	pfsenseapi "github.com/itsKontra/ddash/components/pfsense-api"
	"github.com/itsKontra/ddash/core/coretest"
	"github.com/itsKontra/ddash/core/runtime"
	"github.com/itsKontra/ddash/internal/fakelab"
	"github.com/itsKontra/ddash/sdk"
)

var repoRoot = filepath.Join("..", "..")

// bootNetwork boots the core with network and pfsense-api in-process
// against a lab serving scenarios, both configured as `make dev` does.
func bootNetwork(t *testing.T, scenarios ...string) *coretest.Harness {
	lab, err := fakelab.Start(repoRoot, "127.0.0.1", scenarios...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(lab.Close)
	h := coretest.New(t, coretest.Options{
		Components: []sdk.Definition{network.Definition(), pfsenseapi.Definition()},
		Plugins:    []coretest.Plugin{{Name: "network", Requires: []string{"network", "pfsense-api"}}},
	})
	for component, cfg := range lab.Config() {
		if component == "pfsense-api" || component == "network" {
			h.SeedConfig(component, cfg)
		}
	}
	h.Install("network")
	h.WaitState("pfsense-api", runtime.OK)
	h.WaitState("network", runtime.OK)
	return h
}

// vpnTier returns network's GetVpnTunnel answer on a lab serving scenarios.
func vpnTier(t *testing.T, scenarios ...string) string {
	res := bootNetwork(t, scenarios...).Call("network", "network", "GetVpnTunnel", `{}`)
	if res.Code != http.StatusOK {
		t.Fatalf("GetVpnTunnel: status %d: %s", res.Code, res.Body)
	}
	return res.Body.String()
}

func TestTheBaselineFirewallHasTheTunnelViaWireGuard(t *testing.T) {
	if got := vpnTier(t); !strings.Contains(got, `"tier":"TIER_WIREGUARD"`) {
		t.Errorf("GetVpnTunnel = %s, want via WireGuard", got)
	}
}

func TestTheVpnOnFallbackScenarioHasTheTunnelViaOpenVPN(t *testing.T) {
	if got := vpnTier(t, "vpn-on-fallback"); !strings.Contains(got, `"tier":"TIER_OPENVPN"`) {
		t.Errorf("GetVpnTunnel = %s, want via the OpenVPN fallback", got)
	}
}

func TestTheBaselineFirewallHasAdoptedNewDirectAndTunnelledDevices(t *testing.T) {
	res := bootNetwork(t).Call("network", "network", "ListDevices", `{}`)
	if res.Code != http.StatusOK {
		t.Fatalf("ListDevices: status %d: %s", res.Code, res.Body)
	}
	var got struct {
		Devices []struct {
			Mac, Name, IP   string
			Adopted, Direct bool
			Online          bool
			Aliases         []struct{ Name string }
		}
	}
	if err := json.Unmarshal(res.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	var lines []string
	for _, d := range got.Devices {
		line := fmt.Sprintf("%s %s %q adopted=%v direct=%v online=%v", d.IP, d.Mac, d.Name, d.Adopted, d.Direct, d.Online)
		for _, a := range d.Aliases {
			line += " " + a.Name
		}
		lines = append(lines, line)
	}
	want := []string{
		`198.18.1.20 02:5e:00:00:10:01 "Living room TV" adopted=true direct=false online=true KILL_SWITCH`,
		`198.18.1.21 02:5e:00:00:10:02 "Work laptop" adopted=true direct=true online=true`,
		`198.18.1.22 02:5e:00:00:10:03 "printer" adopted=true direct=false online=false KILL_SWITCH`,
		`198.18.1.150 02:5e:00:00:20:01 "galaxy-s24" adopted=false direct=true online=true`,
		`198.18.1.151 02:5e:00:00:20:02 "" adopted=false direct=false online=false`,
	}
	if diff := cmp.Diff(want, lines); diff != "" {
		t.Errorf("Devices (-want +got):\n%s", diff)
	}
}

func TestAnUnknownScenarioIsRefused(t *testing.T) {
	if _, err := fakelab.Start(repoRoot, "127.0.0.1", "no-such-scenario"); err == nil || !strings.Contains(err.Error(), "no-such-scenario") {
		t.Errorf("Start = %v, want an error naming the scenario no system has", err)
	}
}
