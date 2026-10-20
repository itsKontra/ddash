package fakelab_test

import (
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/itsKontra/ddash/components/network"
	pfsenseapi "github.com/itsKontra/ddash/components/pfsense-api"
	"github.com/itsKontra/ddash/core/coretest"
	"github.com/itsKontra/ddash/core/runtime"
	"github.com/itsKontra/ddash/internal/fakelab"
	"github.com/itsKontra/ddash/sdk"
)

var repoRoot = filepath.Join("..", "..")

// networkCall boots the core with network and pfsense-api in-process
// against a lab serving scenarios, and returns network's answer to method.
func networkCall(t *testing.T, method string, scenarios ...string) string {
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
		if component == "pfsense-api" {
			h.SeedConfig(component, cfg)
		}
	}
	h.Install("network")
	h.WaitState("pfsense-api", runtime.OK)
	h.WaitState("network", runtime.OK)

	res := h.Call("network", "network", method, `{}`)
	if res.Code != http.StatusOK {
		t.Fatalf("%s: status %d: %s", method, res.Code, res.Body)
	}
	return res.Body.String()
}

// vpnTier returns network's GetVpnTunnel answer against the scenarios.
func vpnTier(t *testing.T, scenarios ...string) string {
	return networkCall(t, "GetVpnTunnel", scenarios...)
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

func TestTheBaselineFirewallHasTwoRemoteAccessPeers(t *testing.T) {
	got := networkCall(t, "GetRemoteAccess")
	for _, want := range []string{`"tunnel":"tun_wg1"`, `"name":"Phone"`, `"name":"Laptop"`, `"destination":"198.18.1.0/24","label":"LAN subnet"`} {
		if !strings.Contains(got, want) {
			t.Errorf("GetRemoteAccess = %s, want %s in it", got, want)
		}
	}
}

func TestAnUnknownScenarioIsRefused(t *testing.T) {
	if _, err := fakelab.Start(repoRoot, "127.0.0.1", "no-such-scenario"); err == nil || !strings.Contains(err.Error(), "no-such-scenario") {
		t.Errorf("Start = %v, want an error naming the scenario no system has", err)
	}
}
