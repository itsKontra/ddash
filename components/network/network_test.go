package network_test

import (
	"context"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/google/go-cmp/cmp"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/testing/protocmp"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/itsKontra/ddash/components/adguard-api/adguardapipb"
	"github.com/itsKontra/ddash/components/network"
	"github.com/itsKontra/ddash/components/network/networkpb"
	"github.com/itsKontra/ddash/components/pfsense-api/pfsenseapipb"
	"github.com/itsKontra/ddash/sdk"
	"github.com/itsKontra/ddash/sdk/sdktest"
)

// firewall fakes pfsense-api's generated client: the gateway group and the
// gateways' statuses it answers can be changed between reads.
type firewall struct {
	pfsenseapipb.PfsenseApiServiceClient

	mu       sync.Mutex
	groups   []*pfsenseapipb.GatewayGroup
	gateways []*pfsenseapipb.Gateway
	err      error
	reads    int

	// What the DHCP and alias getters answer (devices_test.go); nil
	// servers and aliases mean the home firewall's.
	leases  []*pfsenseapipb.Lease
	servers []*pfsenseapipb.DhcpServer
	aliases []*pfsenseapipb.Alias

	// interfaces answers ListInterfaces, called on every read; nil means
	// the homelab's (remote_access_test.go).
	interfaces func() []*pfsenseapipb.Interface
	// readAt, when set, is the counters' read time ListInterfaces reports.
	readAt  func() time.Time
	tunnels []*pfsenseapipb.WireguardTunnel
	rules   []*pfsenseapipb.FirewallRule

	// writes is every write request received, in order (devices_test.go).
	writes []proto.Message
}

func (f *firewall) ListGatewayGroups(context.Context, *pfsenseapipb.ListGatewayGroupsRequest, ...grpc.CallOption) (*pfsenseapipb.ListGatewayGroupsResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reads++
	if f.err != nil {
		return nil, f.err
	}
	return &pfsenseapipb.ListGatewayGroupsResponse{Groups: f.groups}, nil
}

func (f *firewall) ListGateways(context.Context, *pfsenseapipb.ListGatewaysRequest, ...grpc.CallOption) (*pfsenseapipb.ListGatewaysResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return nil, f.err
	}
	return &pfsenseapipb.ListGatewaysResponse{Gateways: f.gateways}, nil
}

func (f *firewall) ListInterfaces(context.Context, *pfsenseapipb.ListInterfacesRequest, ...grpc.CallOption) (*pfsenseapipb.ListInterfacesResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return nil, f.err
	}
	if f.interfaces == nil {
		return &pfsenseapipb.ListInterfacesResponse{Interfaces: homelab()}, nil
	}
	resp := &pfsenseapipb.ListInterfacesResponse{Interfaces: f.interfaces()}
	if f.readAt != nil {
		resp.ReadAt = timestamppb.New(f.readAt())
	}
	return resp, nil
}

func (f *firewall) ListWireguardTunnels(context.Context, *pfsenseapipb.ListWireguardTunnelsRequest, ...grpc.CallOption) (*pfsenseapipb.ListWireguardTunnelsResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return nil, f.err
	}
	return &pfsenseapipb.ListWireguardTunnelsResponse{Tunnels: f.tunnels}, nil
}

func (f *firewall) ListFirewallRules(_ context.Context, req *pfsenseapipb.ListFirewallRulesRequest, _ ...grpc.CallOption) (*pfsenseapipb.ListFirewallRulesResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return nil, f.err
	}
	out := &pfsenseapipb.ListFirewallRulesResponse{}
	for _, r := range f.rules {
		if slices.Contains(r.GetInterfaces(), req.GetInterface()) {
			out.Rules = append(out.Rules, r)
		}
	}
	return out, nil
}

// fail makes every read fail with err from now on (nil: answer again).
func (f *firewall) fail(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.err = err
}

// set changes what the firewall answers from the next read on.
func (f *firewall) set(gateways ...*pfsenseapipb.Gateway) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.gateways = gateways
}

// failover is the homelab's group: WireGuard on tier 1, OpenVPN on tier 2,
// a member excluded when down or losing packets.
func failover() *pfsenseapipb.GatewayGroup {
	return &pfsenseapipb.GatewayGroup{Name: "VPN_FAILOVER", Trigger: "downloss", Members: []*pfsenseapipb.GatewayGroupMember{
		{Gateway: "WG_VPN", Tier: 1},
		{Gateway: "OVPN_VPN", Tier: 2},
	}}
}

func online(name string, delay float64) *pfsenseapipb.Gateway {
	return &pfsenseapipb.Gateway{Name: name, Status: "online", Substatus: "none", DelayMs: proto.Float64(delay), LossPercent: proto.Float64(0)}
}

func down(name string) *pfsenseapipb.Gateway {
	return &pfsenseapipb.Gateway{Name: name, Status: "down", Substatus: "highloss", DelayMs: proto.Float64(0), LossPercent: proto.Float64(100)}
}

func start(t *testing.T, fw *firewall) (*sdktest.Harness, networkpb.NetworkServiceClient) {
	return startWithAdGuard(t, fw, &adguard{})
}

// startWithAdGuard starts network on fw and ag (adblocking_test.go).
func startWithAdGuard(t *testing.T, fw *firewall, ag *adguard) (*sdktest.Harness, networkpb.NetworkServiceClient) {
	return startAll(t, fw, ag, homeConfig())
}

// startConfigured starts network on fw with cfg.
func startConfigured(t *testing.T, fw *firewall, cfg *networkpb.NetworkConfig) (*sdktest.Harness, networkpb.NetworkServiceClient) {
	return startAll(t, fw, &adguard{}, cfg)
}

func startAll(t *testing.T, fw *firewall, ag *adguard, cfg *networkpb.NetworkConfig) (*sdktest.Harness, networkpb.NetworkServiceClient) {
	h := sdktest.New(t, network.Definition(),
		sdktest.WithClient[pfsenseapipb.PfsenseApiServiceClient](fw),
		sdktest.WithClient[adguardapipb.AdguardApiServiceClient](ag))
	h.MustStart(cfg)
	return h, networkpb.NewNetworkServiceClient(h.Conn)
}

func tunnel(t *testing.T, c networkpb.NetworkServiceClient) *networkpb.GetVpnTunnelResponse {
	t.Helper()
	resp, err := c.GetVpnTunnel(t.Context(), &networkpb.GetVpnTunnelRequest{})
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func TestTheTunnelIsViaWireGuardWhileItsGatewayIsUp(t *testing.T) {
	fw := &firewall{groups: []*pfsenseapipb.GatewayGroup{failover()}}
	fw.set(online("WAN_DHCP", 8), online("WG_VPN", 24.1), online("OVPN_VPN", 31.9))
	_, c := start(t, fw)

	got := tunnel(t, c)

	want := &networkpb.GetVpnTunnelResponse{
		Tier:  networkpb.Tier_TIER_WIREGUARD,
		Group: "VPN_FAILOVER",
		Gateways: []*networkpb.TunnelGateway{
			{Name: "WG_VPN", Tier: 1, Up: true, Status: "online", Substatus: "none", DelayMs: proto.Float64(24.1), LossPercent: proto.Float64(0)},
			{Name: "OVPN_VPN", Tier: 2, Up: true, Status: "online", Substatus: "none", DelayMs: proto.Float64(31.9), LossPercent: proto.Float64(0)},
		},
	}
	if diff := cmp.Diff(want, got, protocmp.Transform()); diff != "" {
		t.Errorf("GetVpnTunnel (-want +got):\n%s", diff)
	}
}

func TestTheTunnelIsViaTheOpenVPNFallbackWhileOnlyItsGatewayIsUp(t *testing.T) {
	fw := &firewall{groups: []*pfsenseapipb.GatewayGroup{failover()}}
	fw.set(online("WAN_DHCP", 8), down("WG_VPN"), online("OVPN_VPN", 33.2))
	_, c := start(t, fw)

	got := tunnel(t, c)

	if got.GetTier() != networkpb.Tier_TIER_OPENVPN {
		t.Errorf("tier = %v, want via the OpenVPN fallback", got.GetTier())
	}
	if ups := upByName(got); !cmp.Equal(ups, map[string]bool{"WG_VPN": false, "OVPN_VPN": true}) {
		t.Errorf("members up = %v", ups)
	}
}

func TestTheTunnelIsDownWhenBothTiersAreDown(t *testing.T) {
	fw := &firewall{groups: []*pfsenseapipb.GatewayGroup{failover()}}
	fw.set(online("WAN_DHCP", 8), down("WG_VPN"), down("OVPN_VPN"))
	h, c := start(t, fw)

	if got := tunnel(t, c).GetTier(); got != networkpb.Tier_TIER_DOWN {
		t.Errorf("tier = %v, want down", got)
	}
	if got := h.Health(); got.State != sdk.OK {
		t.Errorf("health = %v, want ok: a down VPN Tunnel is news, not network failing", got)
	}
}

// pfSense leaves a member out of the group when its trigger says so: a
// "downloss" group drops an online gateway that is losing packets, a
// "down" group keeps it.
func TestTheGroupsTriggerDecidesWhetherALossyGatewayCarriesTheTunnel(t *testing.T) {
	lossy := &pfsenseapipb.Gateway{Name: "WG_VPN", Status: "online", Substatus: "loss", DelayMs: proto.Float64(40), LossPercent: proto.Float64(12)}
	for trigger, want := range map[string]networkpb.Tier{
		"down":            networkpb.Tier_TIER_WIREGUARD,
		"downloss":        networkpb.Tier_TIER_OPENVPN,
		"downlatency":     networkpb.Tier_TIER_WIREGUARD,
		"downlosslatency": networkpb.Tier_TIER_OPENVPN,
	} {
		t.Run(trigger, func(t *testing.T) {
			group := failover()
			group.Trigger = trigger
			fw := &firewall{groups: []*pfsenseapipb.GatewayGroup{group}}
			fw.set(lossy, online("OVPN_VPN", 33))
			_, c := start(t, fw)

			if got := tunnel(t, c).GetTier(); got != want {
				t.Errorf("tier = %v, want %v", got, want)
			}
		})
	}
}

func TestAGatewayPfSenseDoesNotReportDoesNotCarryTheTunnel(t *testing.T) {
	fw := &firewall{groups: []*pfsenseapipb.GatewayGroup{failover()}}
	fw.set(online("OVPN_VPN", 33))
	_, c := start(t, fw)

	got := tunnel(t, c)

	if got.GetTier() != networkpb.Tier_TIER_OPENVPN {
		t.Errorf("tier = %v, want via the OpenVPN fallback", got.GetTier())
	}
	if wg := got.GetGateways()[0]; wg.GetName() != "WG_VPN" || wg.GetUp() || wg.DelayMs != nil {
		t.Errorf("unreported member = %v, want WG_VPN listed as not up, unmeasured", wg)
	}
}

func TestTheFailoverGroupIsTheFirstWithGatewaysOnTwoTiers(t *testing.T) {
	single := &pfsenseapipb.GatewayGroup{Name: "WAN_ONLY", Trigger: "down", Members: []*pfsenseapipb.GatewayGroupMember{{Gateway: "WAN_DHCP", Tier: 1}}}
	fw := &firewall{groups: []*pfsenseapipb.GatewayGroup{single, failover()}}
	fw.set(online("WAN_DHCP", 8), online("WG_VPN", 24), online("OVPN_VPN", 33))
	_, c := start(t, fw)

	if got := tunnel(t, c); got.GetGroup() != "VPN_FAILOVER" || got.GetTier() != networkpb.Tier_TIER_WIREGUARD {
		t.Errorf("group %q tier %v, want VPN_FAILOVER via WireGuard", got.GetGroup(), got.GetTier())
	}
}

func TestWithoutAFailoverGroupTheTierIsUnknownAndNetworkDegraded(t *testing.T) {
	single := &pfsenseapipb.GatewayGroup{Name: "WAN_ONLY", Trigger: "down", Members: []*pfsenseapipb.GatewayGroupMember{{Gateway: "WAN_DHCP", Tier: 1}}}
	fw := &firewall{groups: []*pfsenseapipb.GatewayGroup{single}}
	fw.set(online("WAN_DHCP", 8))
	h, c := start(t, fw)

	got := tunnel(t, c)

	if got.GetTier() != networkpb.Tier_TIER_UNSPECIFIED || !strings.Contains(got.GetError(), "no failover gateway group") {
		t.Errorf("tunnel = %v, want unknown naming the missing failover group", got)
	}
	if got := h.Health(); got.State != sdk.Degraded || !strings.Contains(got.Message, "no failover gateway group") {
		t.Errorf("health = %v, want degraded naming the missing failover group", got)
	}
}

func TestWhilePfsenseAPIFailsTheTierIsUnknownAndNetworkDegraded(t *testing.T) {
	fw := &firewall{groups: []*pfsenseapipb.GatewayGroup{failover()}, err: status.Error(codes.Unavailable, "pfSense unreachable: connection refused")}
	h, c := start(t, fw)

	got := tunnel(t, c)

	if got.GetTier() != networkpb.Tier_TIER_UNSPECIFIED || !strings.Contains(got.GetError(), "pfSense unreachable") {
		t.Errorf("tunnel = %v, want unknown naming pfsense-api's error", got)
	}
	if got := h.Health(); got.State != sdk.Degraded || !strings.Contains(got.Message, "pfSense unreachable") {
		t.Errorf("health = %v, want degraded naming pfsense-api's error", got)
	}
}

func TestTheGroupIsReadEveryMinute(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		fw := &firewall{groups: []*pfsenseapipb.GatewayGroup{failover()}}
		fw.set(online("WG_VPN", 24), online("OVPN_VPN", 33))
		_, c := start(t, fw)
		if got := tunnel(t, c).GetTier(); got != networkpb.Tier_TIER_WIREGUARD {
			t.Fatalf("tier = %v at start, want via WireGuard", got)
		}

		fw.set(down("WG_VPN"), online("OVPN_VPN", 33))
		time.Sleep(59 * time.Second)
		synctest.Wait()
		if got := tunnel(t, c).GetTier(); got != networkpb.Tier_TIER_WIREGUARD {
			t.Errorf("tier = %v after 59 s, want the last read's via WireGuard", got)
		}

		// One minute plus at most 10% jitter.
		time.Sleep(7 * time.Second)
		synctest.Wait()
		if got := tunnel(t, c).GetTier(); got != networkpb.Tier_TIER_OPENVPN {
			t.Errorf("tier = %v after 66 s, want via the OpenVPN fallback", got)
		}
		fw.mu.Lock()
		defer fw.mu.Unlock()
		if fw.reads != 2 {
			t.Errorf("%d reads of the group in 66 s, want 2", fw.reads)
		}
	})
}

func upByName(t *networkpb.GetVpnTunnelResponse) map[string]bool {
	out := map[string]bool{}
	for _, g := range t.GetGateways() {
		out[g.GetName()] = g.GetUp()
	}
	return out
}
