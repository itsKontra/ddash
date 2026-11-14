package pfsenseapi_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/testing/protocmp"

	"github.com/itsKontra/ddash/components/pfsense-api/pfsenseapipb"
	"github.com/itsKontra/ddash/components/pfsense-api/pfsensefake"
)

// tabletKey is a public key no baseline peer has.
const tabletKey = "Sp1SgxiS0jUVRWz7YLbBn+jLG7KoXYVnB7hO9GrGPmA="

func newPeer(key, description string, allowed ...string) *pfsenseapipb.CreateWireguardPeerRequest {
	return &pfsenseapipb.CreateWireguardPeerRequest{Tunnel: "tun_wg1", PublicKey: key, Description: description, AllowedIps: allowed}
}

// writesTo is the requests the fake got for path other than GETs.
func writesTo(fw *fakeFirewall, path string) []string {
	var out []string
	for _, e := range fw.Log() {
		if strings.HasSuffix(e, " "+path) && !strings.HasPrefix(e, "GET ") {
			out = append(out, e)
		}
	}
	return out
}

func TestListsTheConfiguredWireguardPeersDisabledOnesIncluded(t *testing.T) {
	fw := firewall(t, "baseline")
	_, c := start(t, fw.URL)

	got, err := c.ListWireguardPeers(t.Context(), &pfsenseapipb.ListWireguardPeersRequest{})
	if err != nil {
		t.Fatal(err)
	}

	want := []*pfsenseapipb.WireguardPeerConfig{
		{Tunnel: "tun_wg1", Enabled: true, PublicKey: "BEYg670lDINWndm6HepeqvFcG1Vtu9i213Oz43fsmP4=", Description: "Phone", AllowedIps: []string{"198.18.40.2/32"}},
		{Tunnel: "tun_wg1", Enabled: true, PublicKey: "3DSY1uvmxh/dJZxoYlkRe4+x/ziorV+tX79ngdO2dR4=", Description: "Laptop", AllowedIps: []string{"198.18.40.3/32"}},
		{Tunnel: "tun_wg1", PublicKey: "q4WZt7iaUU1OeeuBdFw6eJbLkMfNaSyEa+OJ34mbOVY=", Description: "Old tablet", AllowedIps: []string{"198.18.40.4/32"}},
		{
			Tunnel: "tun_wg2", Enabled: true, PublicKey: "o822xaDT9sDiY64O140R4zsB3eQYmMTjjQVuXcvw2Lc=", Description: "KILL_SWITCH AT#38",
			AllowedIps: []string{"0.0.0.0/0"}, Endpoint: "203.0.113.195:51820", PersistentKeepalive: 25,
		},
	}
	if diff := cmp.Diff(want, got.GetPeers(), protocmp.Transform()); diff != "" {
		t.Errorf("peers (-want +got):\n%s", diff)
	}
}

// The peer is written, then WireGuard applied once: the tunnel's live
// status lists it from then on.
func TestCreatesAWireguardPeerThenAppliesIt(t *testing.T) {
	fw := firewall(t, "baseline")
	_, c := start(t, fw.URL)
	// Read the live status first, so a stale cache would show.
	if _, err := c.ListWireguardTunnels(t.Context(), &pfsenseapipb.ListWireguardTunnelsRequest{}); err != nil {
		t.Fatal(err)
	}

	got, err := c.CreateWireguardPeer(t.Context(), newPeer(tabletKey, "Anna's tablet", "198.18.40.5/32"))
	if err != nil {
		t.Fatal(err)
	}

	peer := &pfsenseapipb.WireguardPeerConfig{Tunnel: "tun_wg1", Enabled: true, PublicKey: tabletKey, Description: "Anna's tablet", AllowedIps: []string{"198.18.40.5/32"}}
	if diff := cmp.Diff(&pfsenseapipb.CreateWireguardPeerResponse{Peer: peer}, got, protocmp.Transform()); diff != "" {
		t.Errorf("CreateWireguardPeer (-want +got):\n%s", diff)
	}
	held := fw.WireguardPeers()
	want := pfsensefake.WireguardPeer{Tunnel: "tun_wg1", Enabled: true, PublicKey: tabletKey, Description: "Anna's tablet", AllowedIPs: []string{"198.18.40.5/32"}}
	if diff := cmp.Diff(want, held[len(held)-1]); diff != "" {
		t.Errorf("the fake's last peer (-want +got):\n%s", diff)
	}
	if fw.WireGuardPending() {
		t.Error("WireGuard changes still pending, want applied")
	}
	if log := fw.Log(); log[len(log)-1] != pfsensefake.AppliedWireGuard {
		t.Errorf("the apply isn't last: %v", log)
	}
	if w := writesTo(fw, "/api/v2/vpn/wireguard/apply"); len(w) != 1 {
		t.Errorf("applies sent: %v, want one", w)
	}
	tunnels, err := c.ListWireguardTunnels(t.Context(), &pfsenseapipb.ListWireguardTunnelsRequest{})
	if err != nil {
		t.Fatal(err)
	}
	live := tunnels.GetTunnels()[0]
	if !slices.ContainsFunc(live.GetPeers(), func(p *pfsenseapipb.WireguardPeer) bool { return p.GetPublicKey() == tabletKey }) {
		t.Errorf("tun_wg1's live peers %v miss the new one", live.GetPeers())
	}
}

func TestCreatingAPeerRefusesWhatItCantWriteBeforeSendingIt(t *testing.T) {
	for name, tc := range map[string]struct {
		req  *pfsenseapipb.CreateWireguardPeerRequest
		code codes.Code
		says string
	}{
		"a public key another peer has": {newPeer("3DSY1uvmxh/dJZxoYlkRe4+x/ziorV+tX79ngdO2dR4=", "Twin", "198.18.40.5/32"), codes.AlreadyExists, "Laptop"},
		"an address a peer holds":       {newPeer(tabletKey, "Tablet", "198.18.40.3/32"), codes.AlreadyExists, "Laptop"},
		"a disabled peer's address":     {newPeer(tabletKey, "Tablet", "198.18.40.4/32"), codes.AlreadyExists, "Old tablet"},
		"a range overlapping a peer":    {newPeer(tabletKey, "Tablet", "198.18.40.0/29"), codes.AlreadyExists, "Phone"},
		"no key":                        {newPeer("", "Tablet", "198.18.40.5/32"), codes.InvalidArgument, "public key"},
		"a key that isn't one":          {newPeer("not-a-key", "Tablet", "198.18.40.5/32"), codes.InvalidArgument, "public key"},
		"no address":                    {newPeer(tabletKey, "Tablet"), codes.InvalidArgument, "allowed IP"},
		"an address without a prefix":   {newPeer(tabletKey, "Tablet", "198.18.40.5"), codes.InvalidArgument, "198.18.40.5"},
		"no tunnel":                     {&pfsenseapipb.CreateWireguardPeerRequest{PublicKey: tabletKey, AllowedIps: []string{"198.18.40.5/32"}}, codes.InvalidArgument, "tunnel"},
	} {
		t.Run(name, func(t *testing.T) {
			fw := firewall(t, "baseline")
			_, c := start(t, fw.URL)

			_, err := c.CreateWireguardPeer(t.Context(), tc.req)

			if status.Code(err) != tc.code || !strings.Contains(err.Error(), tc.says) {
				t.Errorf("error = %v, want %v naming %q", err, tc.code, tc.says)
			}
			if w := slices.Concat(writesTo(fw, "/api/v2/vpn/wireguard/peer"), writesTo(fw, "/api/v2/vpn/wireguard/apply")); len(w) != 0 {
				t.Errorf("sent %v, want nothing", w)
			}
		})
	}
}

// A peer on another tunnel may hold the same address: the VPN Tunnel's
// provider peer holds 0.0.0.0/0.
func TestAnAddressOnlyAnotherTunnelsPeerHoldsIsFree(t *testing.T) {
	fw := firewall(t, "baseline")
	_, c := start(t, fw.URL)

	if _, err := c.CreateWireguardPeer(t.Context(), newPeer(tabletKey, "Tablet", "198.18.40.5/32")); err != nil {
		t.Fatal(err)
	}
}

func TestAPeerWhoseApplyFailsIsHeldAndTheErrorSaysSo(t *testing.T) {
	fw := firewall(t, "baseline")
	fw.FailWireGuardApply()
	_, c := start(t, fw.URL)

	_, err := c.CreateWireguardPeer(t.Context(), newPeer(tabletKey, "Tablet", "198.18.40.5/32"))

	if err == nil || !strings.Contains(err.Error(), "apply") {
		t.Fatalf("error = %v, want one naming the failed apply", err)
	}
	var detail *pfsenseapipb.CreateWireguardPeerResponse
	for _, d := range status.Convert(err).Details() {
		if r, ok := d.(*pfsenseapipb.CreateWireguardPeerResponse); ok {
			detail = r
		}
	}
	if !detail.GetApplyFailed() || detail.GetPeer().GetPublicKey() != tabletKey {
		t.Errorf("details = %v, want the peer with apply_failed", detail)
	}
	if !fw.WireGuardPending() {
		t.Error("nothing pending, want the peer waiting for an apply")
	}
}
