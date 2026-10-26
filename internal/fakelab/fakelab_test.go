package fakelab_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"google.golang.org/protobuf/encoding/protojson"

	adguardapi "github.com/itsKontra/ddash/components/adguard-api"
	"github.com/itsKontra/ddash/components/adguard-api/adguardfake"
	"github.com/itsKontra/ddash/components/checks"
	"github.com/itsKontra/ddash/components/network"
	"github.com/itsKontra/ddash/components/network/networkpb"
	ntfyapi "github.com/itsKontra/ddash/components/ntfy-api"
	pfsenseapi "github.com/itsKontra/ddash/components/pfsense-api"
	"github.com/itsKontra/ddash/components/pfsense-api/pfsensefake"
	"github.com/itsKontra/ddash/core/appstate"
	"github.com/itsKontra/ddash/core/auth"
	"github.com/itsKontra/ddash/core/coretest"
	"github.com/itsKontra/ddash/core/runtime"
	"github.com/itsKontra/ddash/internal/fakelab"
	"github.com/itsKontra/ddash/sdk"
)

var repoRoot = filepath.Join("..", "..")

// bootNetwork boots the core with network, pfsense-api, adguard-api and
// checks (with its ntfy-api) in-process against a lab serving scenarios,
// configured as `make dev` does, once network reported the VPN Tunnel item.
func bootNetwork(t *testing.T, scenarios ...string) *coretest.Harness {
	h, _ := bootNetworkLab(t, scenarios...)
	return h
}

// bootNetworkLab is bootNetwork, also returning the lab.
func bootNetworkLab(t *testing.T, scenarios ...string) (*coretest.Harness, *fakelab.Lab) {
	return bootNetworkLabWith(t, nil, scenarios...)
}

// bootNetworkLabWith is bootNetworkLab with network's config changed by
// configure first (when set).
func bootNetworkLabWith(t *testing.T, configure func(*networkpb.NetworkConfig), scenarios ...string) (*coretest.Harness, *fakelab.Lab) {
	lab, err := fakelab.Start(repoRoot, "127.0.0.1", scenarios...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(lab.Close)
	h := coretest.New(t, coretest.Options{
		Components: []sdk.Definition{network.Definition(), pfsenseapi.Definition(), adguardapi.Definition(), checks.Definition(), ntfyapi.Definition()},
		Plugins:    []coretest.Plugin{{Name: "network", Requires: []string{"network", "pfsense-api", "adguard-api"}}},
	})
	for component, cfg := range lab.Config() {
		if cfg, ok := cfg.(*networkpb.NetworkConfig); ok && configure != nil {
			configure(cfg)
		}
		if component == "pfsense-api" || component == "network" || component == "adguard-api" || component == "ntfy-api" {
			h.SeedConfig(component, cfg)
		}
	}
	h.Install("network")
	h.WaitState("pfsense-api", runtime.OK)
	h.WaitState("adguard-api", runtime.OK)
	h.WaitState("network", runtime.OK)
	vpnReport(t, h)
	return h, lab
}

// vpnReport waits for network's report of the VPN Tunnel item to checks,
// the one after its Start, and returns its request.
func vpnReport(t *testing.T, h *coretest.Harness) string {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		for _, e := range h.AuditLog() {
			if e.Target == "checks" && e.Method == "ReportState" && e.Username == "network" && strings.Contains(e.Request, `"vpn-tunnel"`) {
				return e.Request
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("network never reported the VPN Tunnel item to checks")
		}
		time.Sleep(5 * time.Millisecond)
	}
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

// network reports the VPN Tunnel item to checks through the host: up on
// the fallback (failover is no Notification), down with both tiers down.
func TestNetworkReportsTheVPNTunnelItemToChecksThroughTheHost(t *testing.T) {
	for scenario, want := range map[string]string{
		"vpn-on-fallback": `"state":"STATE_UP"`,
		"vpn-down":        `"state":"STATE_DOWN"`,
	} {
		t.Run(scenario, func(t *testing.T) {
			h, _ := bootNetworkLab(t, scenario)
			if got := vpnReport(t, h); !strings.Contains(got, want) {
				t.Errorf("ReportState request = %s, want %s", got, want)
			}
		})
	}
}

func TestTheBaselineFirewallHasAdoptedNewDirectAndTunnelledDevices(t *testing.T) {
	body := networkCall(t, "ListDevices")
	var got struct {
		Devices []struct {
			Mac, Name, IP string
			Adopted       bool
			InternetPath  string
			Online        bool
			Aliases       []struct{ Name string }
		}
	}
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatal(err)
	}
	var lines []string
	for _, d := range got.Devices {
		line := fmt.Sprintf("%s %s %q adopted=%v %s online=%v", d.IP, d.Mac, d.Name, d.Adopted, strings.TrimPrefix(d.InternetPath, "INTERNET_PATH_"), d.Online)
		for _, a := range d.Aliases {
			line += " " + a.Name
		}
		lines = append(lines, line)
	}
	// Adopted (static mappings) and New (dynamic leases) Devices, Direct
	// through VPN_BYPASS or Tunnelled, online or not, with the other
	// aliases holding their address.
	want := []string{
		`198.18.1.2 02:5e:92:9d:f0:a6 "w0" adopted=true TUNNELLED online=true`,
		`198.18.1.3 02:56:74:bd:32:5b "Access Point (OpenWrt)" adopted=true TUNNELLED online=true`,
		`198.18.1.4 02:eb:2f:cc:08:07 "lwip" adopted=true TUNNELLED online=true VictronAllow`,
		`198.18.1.5 02:08:c2:85:3d:a1 "Amazon Alexa" adopted=true TUNNELLED online=true`,
		`198.18.1.6 02:4a:a6:ab:ab:d4 "AndroidTV" adopted=true DIRECT online=true`,
		`198.18.1.7 02:ff:c4:52:c5:c3 "einstein" adopted=true TUNNELLED online=true VictronAllow`,
		`198.18.1.10 02:1c:d5:5e:3d:e5 "LexmarkCX317dn" adopted=true DIRECT online=true`,
		`198.18.1.11 02:b9:d2:5c:b9:9f "s22-von-operator" adopted=true TUNNELLED online=true`,
		`198.18.1.12 02:75:79:44:12:e3 "operator-pc" adopted=true DIRECT online=true`,
		`198.18.1.22 02:aa:97:4f:49:e7 "" adopted=false TUNNELLED online=true`,
		`198.18.1.25 02:2f:c2:65:95:10 "" adopted=false TUNNELLED online=false`,
		`198.18.1.26 02:2d:14:a9:09:b6 "a25-von-member" adopted=false TUNNELLED online=true`,
		`198.18.1.101 02:61:c0:a3:8a:16 "homeassistant" adopted=true TUNNELLED online=true`,
		`198.18.1.102 02:e6:29:fa:fc:39 "laptop-2" adopted=true DIRECT online=false`,
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
		if e.Target != "core" && e.ActorKind != auth.Component {
			out = append(out, e)
		}
	}
	return out
}

func TestAdoptAndRenameAreAuditedWithTheirHopsInOneChain(t *testing.T) {
	h := bootNetwork(t)

	res := h.Call("network", "network", "NextReservedAddress", `{}`)
	if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), `"ip":"198.18.1.150"`) {
		t.Fatalf("NextReservedAddress: %d %s, want 198.18.1.150", res.Code, res.Body)
	}
	res = h.Call("network", "network", "AdoptDevice", `{"mac":"02:2d:14:a9:09:b6","name":"Phone","ip":"198.18.1.150"}`)
	if res.Code != http.StatusOK {
		t.Fatalf("AdoptDevice: status %d: %s", res.Code, res.Body)
	}
	res = h.Call("network", "network", "RenameDevice", `{"mac":"02:2d:14:a9:09:b6","name":"Member's phone"}`)
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
		if d.GetMac() == "02:2d:14:a9:09:b6" && (!d.GetAdopted() || d.GetName() != "Member's phone" || d.GetIp() != "198.18.1.150") {
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

	// The Device page's picker on the New Device with no hostname.
	res := h.Call("network", "network", "SetInternetPath", `{"mac":"02:2f:c2:65:95:10","path":"INTERNET_PATH_DIRECT"}`)
	if res.Code != http.StatusOK {
		t.Fatalf("SetInternetPath: status %d: %s", res.Code, res.Body)
	}
	var got networkpb.SetInternetPathResponse
	if err := protojson.Unmarshal(res.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if d := got.GetDevice(); !d.GetAdopted() || d.GetInternetPath() != networkpb.InternetPath_INTERNET_PATH_DIRECT || d.GetIp() != "198.18.1.150" {
		t.Errorf("Device after Direct on = %v, want Adopted at 198.18.1.150 and Direct", d)
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
		Addresses: []string{"198.18.1.208", "198.18.1.1", "198.18.1.6", "198.18.1.10", "198.18.1.102", "198.18.1.16", "198.18.1.12", "198.18.1.122", "198.18.1.54", "198.18.1.150"},
		Details:   []string{"Cloudflare Tunnel", "pfSense", "AndroidTV", "Drucker", "ArbeitsLaptop", "ArchLinux", "operator PC", "devSetup", "member Handy", "02:2f:c2:65:95:10"},
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
		"pfsense-api MoveAliasAddress ok test",
		"pfsense-api CreateStaticMapping ok test",
		"network SetInternetPath ok test",
	}
	if diff := cmp.Diff(wantHops, hops); diff != "" {
		t.Errorf("Audit Log (-want +got):\n%s", diff)
	}
	if len(chains) != 1 {
		t.Errorf("%d chains, want 1", len(chains))
	}
}

func TestAdBlockingOffOnANewDeviceIsOneChainEndingWithItsAdGuardClient(t *testing.T) {
	h, lab := bootNetworkLab(t)

	res := h.Call("network", "network", "SetAdBlocking", `{"mac":"02:2d:14:a9:09:b6","enabled":false}`)
	if res.Code != http.StatusOK {
		t.Fatalf("SetAdBlocking: status %d: %s", res.Code, res.Body)
	}
	var got networkpb.SetAdBlockingResponse
	if err := protojson.Unmarshal(res.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if d := got.GetDevice(); !d.GetAdopted() || d.GetIp() != "198.18.1.150" || !d.GetAdBlockingOff() {
		t.Errorf("Device after the toggle = %v, want Adopted at 198.18.1.150 with ad-blocking off", d)
	}

	// The lab's AdGuard ends with a client for the address the Device
	// moves to, with its own settings and no filtering.
	ag := lab.Handler("adguard-api").(*adguardfake.AdGuard)
	client := ag.PersistentClient("a25-von-member")
	if client == nil || !cmp.Equal(client["ids"], []any{"198.18.1.150"}) || client["use_global_settings"] != false || client["filtering_enabled"] != false {
		t.Errorf("AdGuard's client for the phone = %v, want ids [198.18.1.150], own settings, no filtering", client)
	}
	if n := len(ag.Clients()); n != 7 {
		t.Errorf("%d AdGuard clients, want the 6 it had and the phone's", n)
	}

	// One chain, newest first, all as the Operator.
	var hops []string
	chains := map[string]bool{}
	for _, e := range componentHops(h) {
		hops = append(hops, fmt.Sprintf("%s %s %s %s", e.Target, e.Method, e.Outcome, e.Username))
		chains[e.Chain] = true
	}
	wantHops := []string{
		"adguard-api SetClientFiltering ok test",
		"pfsense-api CreateStaticMapping ok test",
		"network SetAdBlocking ok test",
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
