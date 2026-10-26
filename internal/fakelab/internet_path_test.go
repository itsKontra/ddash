package fakelab_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	"github.com/itsKontra/ddash/components/network/networkpb"
	"github.com/itsKontra/ddash/components/pfsense-api/pfsensefake"
	"github.com/itsKontra/ddash/core/coretest"
	"github.com/itsKontra/ddash/internal/fakelab"
)

// The no-internet scenario is the baseline firewall with a block alias,
// NO_INTERNET, holding Amazon Alexa (.5) and laptop-2 (.102), which is
// also in VPN_BYPASS.
var (
	bypass0 = []string{"198.18.1.208", "198.18.1.1", "198.18.1.6", "198.18.1.10", "198.18.1.102", "198.18.1.16", "198.18.1.12", "198.18.1.122", "198.18.1.54"}
	block0  = []string{"198.18.1.5", "198.18.1.102"}
)

const (
	s22       = "02:b9:d2:5c:b9:9f" // 198.18.1.11, Tunnelled
	androidTV = "02:4a:a6:ab:ab:d4" // 198.18.1.6, Direct
	alexa     = "02:08:c2:85:3d:a1" // 198.18.1.5, No Internet
	laptop2   = "02:e6:29:fa:fc:39" // 198.18.1.102, in both aliases
)

// bootNoInternet boots network on the no-internet scenario with
// NO_INTERNET as its block alias.
func bootNoInternet(t *testing.T) (*coretest.Harness, *pfsensefake.Firewall) {
	h, lab := bootNetworkLabWith(t, func(cfg *networkpb.NetworkConfig) { cfg.BlockAlias = "NO_INTERNET" }, "no-internet")
	return h, firewallOf(lab)
}

func firewallOf(lab *fakelab.Lab) *pfsensefake.Firewall {
	return lab.Handler("pfsense-api").(*pfsensefake.Firewall)
}

// members is the named alias's addresses on the lab's firewall.
func members(fw *pfsensefake.Firewall, name string) []string {
	for _, a := range fw.Aliases() {
		if a.Name == name {
			return a.Addresses
		}
	}
	return nil
}

// networkJSON calls network's method with body and decodes its answer into out.
func networkJSON(t *testing.T, h *coretest.Harness, method, body string, out proto.Message) {
	t.Helper()
	res := h.Call("network", "network", method, body)
	if res.Code != http.StatusOK {
		t.Fatalf("%s: status %d: %s", method, res.Code, res.Body)
	}
	if err := protojson.Unmarshal(res.Body.Bytes(), out); err != nil {
		t.Fatal(err)
	}
}

func listedDevice(t *testing.T, h *coretest.Harness, mac string) *networkpb.Device {
	t.Helper()
	var list networkpb.ListDevicesResponse
	networkJSON(t, h, "ListDevices", `{}`, &list)
	for _, d := range list.GetDevices() {
		if d.GetMac() == mac {
			return d
		}
	}
	t.Fatalf("ListDevices has no %s", mac)
	return nil
}

func with(list []string, add string) []string { return append(slices.Clone(list), add) }

func without(list []string, remove string) []string {
	return slices.DeleteFunc(slices.Clone(list), func(m string) bool { return m == remove })
}

// countApplies is how many times the firewall's alias changes took effect.
func countApplies(log []string) int {
	n := 0
	for _, e := range log {
		if e == pfsensefake.AppliedFirewall {
			n++
		}
	}
	return n
}

func TestMovingADeviceBetweenInternetPathsWritesOnlyItsOwnAddressWithOneApply(t *testing.T) {
	const (
		tunnelled = networkpb.InternetPath_INTERNET_PATH_TUNNELLED
		direct    = networkpb.InternetPath_INTERNET_PATH_DIRECT
		blocked   = networkpb.InternetPath_INTERNET_PATH_NO_INTERNET
	)
	for name, tc := range map[string]struct {
		mac                   string
		from, to              networkpb.InternetPath
		wantBypass, wantBlock []string
	}{
		"Tunnelled to Direct":        {s22, tunnelled, direct, with(bypass0, "198.18.1.11"), block0},
		"Tunnelled to No Internet":   {s22, tunnelled, blocked, bypass0, with(block0, "198.18.1.11")},
		"Direct to Tunnelled":        {androidTV, direct, tunnelled, without(bypass0, "198.18.1.6"), block0},
		"Direct to No Internet":      {androidTV, direct, blocked, without(bypass0, "198.18.1.6"), with(block0, "198.18.1.6")},
		"No Internet to Tunnelled":   {alexa, blocked, tunnelled, bypass0, without(block0, "198.18.1.5")},
		"No Internet to Direct":      {alexa, blocked, direct, with(bypass0, "198.18.1.5"), without(block0, "198.18.1.5")},
		"in both aliases to Direct":  {laptop2, blocked, direct, bypass0, without(block0, "198.18.1.102")},
		"in both aliases, Tunnelled": {laptop2, blocked, tunnelled, without(bypass0, "198.18.1.102"), without(block0, "198.18.1.102")},
	} {
		t.Run(name, func(t *testing.T) {
			h, fw := bootNoInternet(t)
			if got := listedDevice(t, h, tc.mac).GetInternetPath(); got != tc.from {
				t.Fatalf("before the move the Device is %v, want %v", got, tc.from)
			}
			others := func() []pfsensefake.Alias {
				return slices.DeleteFunc(fw.Aliases(), func(a pfsensefake.Alias) bool { return a.Name == "VPN_BYPASS" || a.Name == "NO_INTERNET" })
			}
			othersBefore, logBefore := others(), len(fw.Log())

			var got networkpb.SetInternetPathResponse
			networkJSON(t, h, "SetInternetPath", fmt.Sprintf(`{"mac":%q,"path":%q}`, tc.mac, tc.to), &got)

			if p := got.GetDevice().GetInternetPath(); p != tc.to {
				t.Errorf("Device after the move is %v, want %v", p, tc.to)
			}
			if p := listedDevice(t, h, tc.mac).GetInternetPath(); p != tc.to {
				t.Errorf("ListDevices after the move has it %v, want %v", p, tc.to)
			}
			if diff := cmp.Diff(tc.wantBypass, members(fw, "VPN_BYPASS")); diff != "" {
				t.Errorf("VPN_BYPASS (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(tc.wantBlock, members(fw, "NO_INTERNET")); diff != "" {
				t.Errorf("NO_INTERNET (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(othersBefore, others()); diff != "" {
				t.Errorf("other aliases changed (-before +after):\n%s", diff)
			}
			if n := countApplies(fw.Log()[logBefore:]); n != 1 || fw.FirewallPending() {
				t.Errorf("%d applies (pending after: %v), want exactly one", n, fw.FirewallPending())
			}
		})
	}
}

func TestADeviceAddedToAnAliasIsAddedUnderItsName(t *testing.T) {
	h, fw := bootNoInternet(t)

	networkJSON(t, h, "SetInternetPath", `{"mac":"`+s22+`","path":"INTERNET_PATH_NO_INTERNET"}`, &networkpb.SetInternetPathResponse{})

	for _, a := range fw.Aliases() {
		if a.Name == "NO_INTERNET" {
			if d := a.Details[len(a.Details)-1]; d != "s22-von-operator" {
				t.Errorf("NO_INTERNET's new member is described %q, want the Device's name", d)
			}
		}
	}
}

// pfsenseCall makes a write through the real pfsense-api, as network's
// Plugin, so its cached aliases follow.
func pfsenseCall(t *testing.T, h *coretest.Harness, method, body string) {
	t.Helper()
	if res := h.Call("network", "pfsense-api", method, body); res.Code != http.StatusOK {
		t.Fatalf("%s: status %d: %s", method, res.Code, res.Body)
	}
}

func TestADeviceInAnAliasThroughAnotherMemberIsRefusedWhenMovedOut(t *testing.T) {
	for _, alias := range []struct{ name, stays, to string }{
		{"VPN_BYPASS", "stays Direct", "INTERNET_PATH_TUNNELLED"},
		{"NO_INTERNET", "stays No Internet", "INTERNET_PATH_TUNNELLED"},
		{"NO_INTERNET", "stays No Internet", "INTERNET_PATH_DIRECT"},
	} {
		for kind, member := range map[string]string{"range": "198.18.1.10-198.18.1.11", "nested alias": "KILL_SWITCH"} {
			t.Run(alias.name+" through a "+kind+" to "+alias.to, func(t *testing.T) {
				h, fw := bootNoInternet(t)
				if member == "KILL_SWITCH" {
					pfsenseCall(t, h, "AddAliasAddress", `{"alias":"KILL_SWITCH","address":"198.18.1.11"}`)
				}
				pfsenseCall(t, h, "AddAliasAddress", fmt.Sprintf(`{"alias":%q,"address":%q}`, alias.name, member))
				logBefore := len(fw.Log())

				res := h.Call("network", "network", "SetInternetPath", fmt.Sprintf(`{"mac":%q,"path":%q}`, s22, alias.to))

				var e struct{ Code, Message string }
				_ = json.Unmarshal(res.Body.Bytes(), &e)
				if e.Code != "failed_precondition" || !strings.Contains(e.Message, alias.stays) || !strings.Contains(e.Message, member) || !strings.Contains(e.Message, alias.name) {
					t.Errorf("SetInternetPath = %d %s, want failed_precondition: s22 %s, naming %s and %s", res.Code, res.Body, alias.stays, alias.name, member)
				}
				for _, e := range fw.Log()[logBefore:] {
					if strings.HasPrefix(e, "PATCH") || strings.HasPrefix(e, "POST") {
						t.Errorf("firewall got %s, want no write", e)
					}
				}
			})
		}
	}
}

func TestWithoutABlockAliasNoInternetIsRefused(t *testing.T) {
	h, lab := bootNetworkLab(t)
	fw := firewallOf(lab)

	var list networkpb.ListDevicesResponse
	networkJSON(t, h, "ListDevices", `{}`, &list)
	if list.GetBlockAlias() != "" {
		t.Errorf("ListDevices' block alias = %q, want none", list.GetBlockAlias())
	}
	logBefore := len(fw.Log())

	res := h.Call("network", "network", "SetInternetPath", `{"mac":"`+s22+`","path":"INTERNET_PATH_NO_INTERNET"}`)

	if res.Code == http.StatusOK || !strings.Contains(res.Body.String(), "No Internet alias") {
		t.Errorf("SetInternetPath = %d %s, want a refusal naming the No Internet alias setting", res.Code, res.Body)
	}
	for _, e := range fw.Log()[logBefore:] {
		if strings.HasPrefix(e, "PATCH") || strings.HasPrefix(e, "POST") {
			t.Errorf("firewall got %s, want no write", e)
		}
	}
}

func TestNetworkInfoByAddressGivesThePathAdsAllowedAndTheDeviceIfAny(t *testing.T) {
	h, _ := bootNoInternet(t)

	var got networkpb.GetNetworkInfoResponse
	// laptop-2 is in both aliases, with ads allowed; .208 is a server in
	// VPN_BYPASS; .21 is no Device, Tunnelled, with ads allowed.
	networkJSON(t, h, "GetNetworkInfo", `{"addresses":["198.18.1.102","198.18.1.208","198.18.1.21"]}`, &got)

	type info struct {
		Address, Path string
		AdsAllowed    bool
		Device        string
	}
	var infos []info
	for _, i := range got.GetInfos() {
		infos = append(infos, info{i.GetAddress(), i.GetInternetPath().String(), i.GetAdsAllowed(), i.GetDevice().GetMac()})
	}
	want := []info{
		{"198.18.1.102", "INTERNET_PATH_NO_INTERNET", true, laptop2},
		{"198.18.1.208", "INTERNET_PATH_DIRECT", false, ""},
		{"198.18.1.21", "INTERNET_PATH_TUNNELLED", true, ""},
	}
	if diff := cmp.Diff(want, infos); diff != "" {
		t.Errorf("GetNetworkInfo (-want +got):\n%s", diff)
	}
	if got.GetAdguardError() != "" {
		t.Errorf("AdGuard error = %q, want none", got.GetAdguardError())
	}
}
