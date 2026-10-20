package fakelab_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"google.golang.org/protobuf/encoding/protojson"

	"github.com/itsKontra/ddash/components/network"
	"github.com/itsKontra/ddash/components/network/networkpb"
	pfsenseapi "github.com/itsKontra/ddash/components/pfsense-api"
	"github.com/itsKontra/ddash/components/pfsense-api/pfsensefake"
	"github.com/itsKontra/ddash/core/appstate"
	"github.com/itsKontra/ddash/core/coretest"
	"github.com/itsKontra/ddash/core/runtime"
	"github.com/itsKontra/ddash/internal/fakelab"
	"github.com/itsKontra/ddash/sdk"
)

var repoRoot = filepath.Join("..", "..")

// bootNetwork boots the core with network and pfsense-api in-process
// against a lab serving scenarios, both configured as `make dev` does.
func bootNetwork(t *testing.T, scenarios ...string) *coretest.Harness {
	h, _ := bootNetworkLab(t, scenarios...)
	return h
}

// bootNetworkLab is bootNetwork, also returning the lab.
func bootNetworkLab(t *testing.T, scenarios ...string) (*coretest.Harness, *fakelab.Lab) {
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
	return h, lab
}

// networkCall returns network's answer to method on a lab serving scenarios.
func networkCall(t *testing.T, method string, scenarios ...string) string {
	res := bootNetwork(t, scenarios...).Call("network", "network", method, `{}`)
	if res.Code != http.StatusOK {
		t.Fatalf("%s: status %d: %s", method, res.Code, res.Body)
	}
	return res.Body.String()
}

// vpnTier returns network's GetVpnTunnel answer on a lab serving scenarios.
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

func TestTheBaselineFirewallHasAdoptedNewDirectAndTunnelledDevices(t *testing.T) {
	body := networkCall(t, "ListDevices")
	var got struct {
		Devices []struct {
			Mac, Name, IP   string
			Adopted, Direct bool
			Online          bool
			Aliases         []struct{ Name string }
		}
	}
	if err := json.Unmarshal([]byte(body), &got); err != nil {
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

func TestTheBaselineFirewallHasTwoRemoteAccessPeers(t *testing.T) {
	body := networkCall(t, "GetRemoteAccess")
	var got networkpb.GetRemoteAccessResponse
	if err := protojson.Unmarshal([]byte(body), &got); err != nil {
		t.Fatal(err)
	}

	var names []string
	for _, p := range got.GetPeers() {
		names = append(names, p.GetName())
	}
	if got.GetTunnel() != "tun_wg1" || strings.Join(names, ",") != "Phone,Laptop" {
		t.Fatalf("GetRemoteAccess = %s, want tun_wg1 with Phone and Laptop", body)
	}
	if first := got.GetPeers()[0].GetReaches()[0]; first.GetDestination() != "198.18.1.0/24" || first.GetLabel() != "LAN subnet" {
		t.Errorf("Phone's first reach = %v, want the LAN subnet", first)
	}
}

// componentHops is the Audit Log's entries without the core's (the install).
func componentHops(h *coretest.Harness) []appstate.Entry {
	var out []appstate.Entry
	for _, e := range h.AuditLog() {
		if e.Target != "core" {
			out = append(out, e)
		}
	}
	return out
}

func TestAdoptAndRenameAreAuditedWithTheirHopsInOneChain(t *testing.T) {
	h := bootNetwork(t)

	res := h.Call("network", "network", "NextReservedAddress", `{}`)
	if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), `"ip":"198.18.1.23"`) {
		t.Fatalf("NextReservedAddress: %d %s, want 198.18.1.23", res.Code, res.Body)
	}
	res = h.Call("network", "network", "AdoptDevice", `{"mac":"02:5e:00:00:20:01","name":"Phone","ip":"198.18.1.23"}`)
	if res.Code != http.StatusOK {
		t.Fatalf("AdoptDevice: status %d: %s", res.Code, res.Body)
	}
	res = h.Call("network", "network", "RenameDevice", `{"mac":"02:5e:00:00:20:01","name":"Anna's phone"}`)
	if res.Code != http.StatusOK {
		t.Fatalf("RenameDevice: status %d: %s", res.Code, res.Body)
	}

	// The lab's firewall keeps the write: the Device lists as Adopted.
	body := h.Call("network", "network", "ListDevices", `{}`).Body.String()
	var list networkpb.ListDevicesResponse
	if err := protojson.Unmarshal([]byte(body), &list); err != nil {
		t.Fatal(err)
	}
	for _, d := range list.GetDevices() {
		if d.GetMac() == "02:5e:00:00:20:01" && (!d.GetAdopted() || d.GetName() != "Anna's phone" || d.GetIp() != "198.18.1.23") {
			t.Errorf("Device after Adopt and rename = %v", d)
		}
	}

	// Newest first: each Action is network's hop then pfsense-api's, on
	// one chain per Action, all as the Operator.
	var hops []string
	chains := map[string]string{}
	for _, e := range componentHops(h) {
		hops = append(hops, fmt.Sprintf("%s %s %s %s", e.Target, e.Method, e.Outcome, e.Username))
		if first, ok := chains[e.Method]; ok && first != e.Chain {
			t.Errorf("%s on two chains", e.Method)
		}
		chains[e.Method] = e.Chain
	}
	want := []string{
		"pfsense-api RenameStaticMapping ok test",
		"network RenameDevice ok test",
		"pfsense-api CreateStaticMapping ok test",
		"network AdoptDevice ok test",
	}
	if diff := cmp.Diff(want, hops); diff != "" {
		t.Errorf("Audit Log (-want +got):\n%s", diff)
	}
	if chains["AdoptDevice"] != chains["CreateStaticMapping"] || chains["RenameDevice"] != chains["RenameStaticMapping"] || chains["AdoptDevice"] == chains["RenameDevice"] {
		t.Errorf("chains = %v, want Adopt's two hops on one chain and rename's on another", chains)
	}
}

func TestDirectOnANewDeviceIsOneAuditChainEndingWithItsAddressInTheBypassAlias(t *testing.T) {
	h, lab := bootNetworkLab(t)

	// The Device page's toggle on the New Device with no hostname.
	res := h.Call("network", "network", "SetDirect", `{"mac":"02:5e:00:00:20:02","direct":true}`)
	if res.Code != http.StatusOK {
		t.Fatalf("SetDirect: status %d: %s", res.Code, res.Body)
	}
	var got networkpb.SetDirectResponse
	if err := protojson.Unmarshal(res.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if d := got.GetDevice(); !d.GetAdopted() || !d.GetDirect() || d.GetIp() != "198.18.1.23" {
		t.Errorf("Device after Direct on = %v, want Adopted at 198.18.1.23 and Direct", d)
	}

	// The lab's firewall ends with the Device's address in the bypass
	// alias, after the members that are no Devices, all applied.
	fw := lab.Handler("pfsense-api").(*pfsensefake.Firewall)
	var bypass pfsensefake.Alias
	for _, a := range fw.Aliases() {
		if a.Name == "VPN_BYPASS" {
			bypass = a
		}
	}
	want := pfsensefake.Alias{
		Name:      "VPN_BYPASS",
		Addresses: []string{"198.18.1.21", "198.18.1.150", "198.18.2.10", "198.18.1.23"},
		Details:   []string{"Work laptop", "Phone", "Media server", "02:5e:00:00:20:02"},
	}
	if diff := cmp.Diff(want, bypass); diff != "" {
		t.Errorf("VPN_BYPASS (-want +got):\n%s", diff)
	}
	if fw.FirewallPending() || fw.Pending() {
		t.Error("changes still pending on the firewall, want applied")
	}

	// One chain, newest first: pfsense-api's alias and Adopt hops, then
	// network's root, all as the Operator.
	var hops []string
	chains := map[string]bool{}
	for _, e := range componentHops(h) {
		hops = append(hops, fmt.Sprintf("%s %s %s %s", e.Target, e.Method, e.Outcome, e.Username))
		chains[e.Chain] = true
	}
	wantHops := []string{
		"pfsense-api AddAliasAddress ok test",
		"pfsense-api CreateStaticMapping ok test",
		"network SetDirect ok test",
	}
	if diff := cmp.Diff(wantHops, hops); diff != "" {
		t.Errorf("Audit Log (-want +got):\n%s", diff)
	}
	if len(chains) != 1 {
		t.Errorf("%d chains, want 1", len(chains))
	}
}

func TestAnUnknownScenarioIsRefused(t *testing.T) {
	if _, err := fakelab.Start(repoRoot, "127.0.0.1", "no-such-scenario"); err == nil || !strings.Contains(err.Error(), "no-such-scenario") {
		t.Errorf("Start = %v, want an error naming the scenario no system has", err)
	}
}
