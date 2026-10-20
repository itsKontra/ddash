package network_test

import (
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/testing/protocmp"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/itsKontra/ddash/components/network/networkpb"
	"github.com/itsKontra/ddash/components/pfsense-api/pfsenseapipb"
)

var handshake = time.Date(2026, 10, 20, 7, 30, 0, 0, time.UTC)

// homelab is the firewall's interfaces: the Remote Access tunnel (tun_wg1)
// sits on opt1, which has no gateway; the VPN Tunnel's (tun_wg2) on opt3,
// which has one.
func homelab() []*pfsenseapipb.Interface {
	return []*pfsenseapipb.Interface{
		{Name: "wan", Description: "WAN", HwInterface: "igc0", Ipv4Address: "203.0.113.10", Ipv4Prefix: 24, Gateway: "203.0.113.1"},
		{Name: "lan", Description: "LAN", HwInterface: "igc1", Ipv4Address: "198.18.1.1", Ipv4Prefix: 24},
		{Name: "opt1", Description: "REMOTEACCESS", HwInterface: "tun_wg1", Ipv4Address: "198.18.40.1", Ipv4Prefix: 24},
		{Name: "opt3", Description: "WG_VPN", HwInterface: "tun_wg2", Ipv4Address: "198.18.30.2", Ipv4Prefix: 32, Gateway: "198.18.30.1"},
	}
}

func wireguard() []*pfsenseapipb.WireguardTunnel {
	return []*pfsenseapipb.WireguardTunnel{
		{Name: "tun_wg2", Description: "WG_VPN", Status: "up", Peers: []*pfsenseapipb.WireguardPeer{
			{PublicKey: "provider-key", Description: "VPN provider", Endpoint: "203.0.113.200:51820", AllowedIps: []string{"0.0.0.0/0"}, LatestHandshake: timestamppb.New(handshake)},
		}},
		{Name: "tun_wg1", Description: "Remote Access", Status: "up", ListenPort: 51820, Peers: []*pfsenseapipb.WireguardPeer{
			{PublicKey: "phone-key", Description: "Phone", Endpoint: "203.0.113.77:41234", AllowedIps: []string{"198.18.40.2/32"}, LatestHandshake: timestamppb.New(handshake), RxBytes: 1003948, TxBytes: 8384712},
			{PublicKey: "laptop-key", Description: "Laptop", AllowedIps: []string{"198.18.40.3/32"}},
		}},
	}
}

func rule(typ, source, destination, protocol, port, description string) *pfsenseapipb.FirewallRule {
	return &pfsenseapipb.FirewallRule{Type: typ, Interfaces: []string{"opt1"}, IpProtocol: "inet", Source: source, Destination: destination, Protocol: protocol, DestinationPort: port, Description: description}
}

// remoteAccessRules are opt1's rules, in order.
func remoteAccessRules() []*pfsenseapipb.FirewallRule {
	disabled := rule("pass", "any", "any", "", "", "Old allow all")
	disabled.Disabled = true
	lanRule := rule("pass", "lan", "any", "", "", "Default allow LAN to any rule")
	lanRule.Interfaces = []string{"lan"}
	return []*pfsenseapipb.FirewallRule{
		lanRule,
		rule("pass", "198.18.40.2", "lan", "", "", "Phone reaches the LAN"),
		rule("pass", "opt1", "198.18.1.20", "tcp", "443", "Peers reach the reverse proxy"),
		rule("pass", "opt1", "DNS_SERVERS", "tcp/udp", "53", "Peers resolve names"),
		rule("block", "opt1", "198.18.1.30", "", "", "Not the printer"),
		rule("pass", "198.18.40.3", "!lan:ip", "icmp", "", "Laptop pings anything but the firewall"),
		disabled,
		rule("reject", "any", "any", "", "", "Block the rest"),
		rule("pass", "opt1", "(self)", "tcp", "443", "Firewall GUI (after the block: never matches)"),
	}
}

func remoteAccess(t *testing.T, fw *firewall) *networkpb.GetRemoteAccessResponse {
	t.Helper()
	_, c := start(t, fw)
	resp, err := c.GetRemoteAccess(t.Context(), &networkpb.GetRemoteAccessRequest{})
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func TestRemoteAccessListsPeersWithHandshakeTrafficAndWhatTheirPassRulesReach(t *testing.T) {
	fw := &firewall{groups: []*pfsenseapipb.GatewayGroup{failover()}, interfaces: homelab, tunnels: wireguard(), rules: remoteAccessRules()}

	got := remoteAccess(t, fw)

	proxy := &networkpb.Reach{Destination: "198.18.1.20", Protocol: "tcp", Ports: "443", Rule: "Peers reach the reverse proxy"}
	dns := &networkpb.Reach{Destination: "DNS_SERVERS", Label: "alias", Protocol: "tcp/udp", Ports: "53", Rule: "Peers resolve names"}
	want := &networkpb.GetRemoteAccessResponse{
		Tunnel:    "tun_wg1",
		Interface: "REMOTEACCESS",
		Peers: []*networkpb.RemoteAccessPeer{
			{
				Name: "Phone", PublicKey: "phone-key", Addresses: []string{"198.18.40.2/32"}, Endpoint: "203.0.113.77:41234",
				LatestHandshake: timestamppb.New(handshake), RxBytes: 1003948, TxBytes: 8384712,
				Reaches: []*networkpb.Reach{
					{Destination: "198.18.1.0/24", Label: "LAN subnet", Protocol: "any", Rule: "Phone reaches the LAN"},
					proxy,
					dns,
				},
			},
			{
				Name: "Laptop", PublicKey: "laptop-key", Addresses: []string{"198.18.40.3/32"},
				Reaches: []*networkpb.Reach{
					proxy,
					dns,
					{Destination: "!198.18.1.1", Label: "not LAN address", Protocol: "icmp", Rule: "Laptop pings anything but the firewall"},
				},
			},
		},
	}
	if diff := cmp.Diff(want, got, protocmp.Transform()); diff != "" {
		t.Errorf("GetRemoteAccess (-want +got):\n%s", diff)
	}
}

func TestAPeerReachesWhatAnAnySourceRulePassesWithItsDestinationNamed(t *testing.T) {
	fw := &firewall{groups: []*pfsenseapipb.GatewayGroup{failover()}, interfaces: homelab, tunnels: wireguard(), rules: []*pfsenseapipb.FirewallRule{
		rule("pass", "any", "any", "", "", "Allow all"),
		rule("pass", "any", "(self)", "", "", "The firewall"),
		rule("pass", "198.18.40.0/28", "lan:ip", "", "", "LAN address from the peers' /28"),
		rule("pass", "198.18.41.0/24", "lan", "", "", "Another subnet's peers"),
	}}

	got := remoteAccess(t, fw).GetPeers()[0].GetReaches()

	want := []*networkpb.Reach{
		{Destination: "any", Label: "anywhere", Protocol: "any", Rule: "Allow all"},
		{Destination: "(self)", Label: "the firewall", Protocol: "any", Rule: "The firewall"},
		{Destination: "198.18.1.1", Label: "LAN address", Protocol: "any", Rule: "LAN address from the peers' /28"},
	}
	if diff := cmp.Diff(want, got, protocmp.Transform()); diff != "" {
		t.Errorf("Phone's reaches (-want +got):\n%s", diff)
	}
}

func TestWithoutAWireguardTunnelOnAGatewaylessInterfaceThereIsNoRemoteAccess(t *testing.T) {
	onlyVPN := wireguard()[:1]
	fw := &firewall{groups: []*pfsenseapipb.GatewayGroup{failover()}, interfaces: homelab, tunnels: onlyVPN}

	got := remoteAccess(t, fw)

	if len(got.GetPeers()) != 0 || !strings.Contains(got.GetError(), "no Remote Access tunnel") {
		t.Errorf("GetRemoteAccess = %v, want no peers and an error naming the missing Remote Access tunnel", got)
	}
}

func TestRemoteAccessIsUnavailableWhilePfsenseAPIFails(t *testing.T) {
	fw := &firewall{groups: []*pfsenseapipb.GatewayGroup{failover()}, err: status.Error(codes.Unavailable, "pfSense unreachable: connection refused")}
	_, c := start(t, fw)

	_, err := c.GetRemoteAccess(t.Context(), &networkpb.GetRemoteAccessRequest{})

	if status.Code(err) != codes.Unavailable || !strings.Contains(err.Error(), "pfSense unreachable") {
		t.Errorf("GetRemoteAccess error = %v, want Unavailable naming pfsense-api's error", err)
	}
}
