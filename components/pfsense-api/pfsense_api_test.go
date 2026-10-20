package pfsenseapi_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/testing/protocmp"
	"google.golang.org/protobuf/types/known/timestamppb"

	pfsenseapi "github.com/itsKontra/ddash/components/pfsense-api"
	"github.com/itsKontra/ddash/components/pfsense-api/pfsenseapipb"
	"github.com/itsKontra/ddash/components/pfsense-api/pfsensefake"
	configpb "github.com/itsKontra/ddash/proto/ddash/config"
	"github.com/itsKontra/ddash/sdk"
	"github.com/itsKontra/ddash/sdk/sdktest"
)

const apiKey = "0123456789abcdef0123456789abcdef"

// fakeFirewall is pfSense's REST API serving a scenario: the stateful fake
// for static mappings and their apply, fixtures for the rest.
type fakeFirewall struct {
	*httptest.Server
	*pfsensefake.Firewall
	hits atomic.Int32
}

// firewall fakes pfSense's REST API serving a scenario. Like the real one
// it refuses a request without the configured key in X-API-Key.
func firewall(t *testing.T, scenario string) *fakeFirewall {
	return firewallWith(t, scenario, pfsensefake.Options{})
}

func firewallWith(t *testing.T, scenario string, opts pfsensefake.Options) *fakeFirewall {
	opts.Miss = func(request string, err error) { t.Errorf("fixture: %v", err) }
	scenarioHandler, err := pfsensefake.New("testdata/"+scenario, opts)
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeFirewall{Firewall: scenarioHandler}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.hits.Add(1)
		if r.Header.Get("X-API-Key") != apiKey {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			w.Write([]byte(`{"code":401,"status":"unauthorized","response_id":"AUTH_AUTHENTICATION_FAILED","message":"Authentication failed.","data":[]}`))
			return
		}
		scenarioHandler.ServeHTTP(w, r)
	}))
	t.Cleanup(f.Close)
	return f
}

func start(t *testing.T, url string) (*sdktest.Harness, pfsenseapipb.PfsenseApiServiceClient) {
	h := sdktest.New(t, pfsenseapi.Definition())
	h.MustStart(&pfsenseapipb.PfsenseApiConfig{Url: url, ApiKey: apiKey})
	return h, pfsenseapipb.NewPfsenseApiServiceClient(h.Conn)
}

func TestListsGatewaysWithStatusLatencyAndLoss(t *testing.T) {
	srv := firewall(t, "baseline")
	_, c := start(t, srv.URL)

	got, err := c.ListGateways(t.Context(), &pfsenseapipb.ListGatewaysRequest{})
	if err != nil {
		t.Fatal(err)
	}

	want := &pfsenseapipb.ListGatewaysResponse{Gateways: []*pfsenseapipb.Gateway{
		{Name: "OVPN_VPN", Status: "online", Substatus: "none", DelayMs: proto.Float64(22.246), StddevMs: proto.Float64(0.414), LossPercent: proto.Float64(0), MonitorIp: "203.0.113.233", SourceIp: "203.0.113.169"},
		{Name: "WG_VPN", Status: "online", Substatus: "none", DelayMs: proto.Float64(16.215), StddevMs: proto.Float64(1.379), LossPercent: proto.Float64(0), MonitorIp: "203.0.113.76", SourceIp: "198.18.30.2"},
		{Name: "WANGW", Status: "online", Substatus: "none", DelayMs: proto.Float64(4.815), StddevMs: proto.Float64(0.227), LossPercent: proto.Float64(0), MonitorIp: "203.0.113.146", SourceIp: "203.0.113.53"},
	}}
	if diff := cmp.Diff(want, got, protocmp.Transform()); diff != "" {
		t.Errorf("ListGateways (-want +got):\n%s", diff)
	}
}

func TestListsGatewayGroupsWithTheirMembersByTier(t *testing.T) {
	srv := firewall(t, "baseline")
	_, c := start(t, srv.URL)

	got, err := c.ListGatewayGroups(t.Context(), &pfsenseapipb.ListGatewayGroupsRequest{})
	if err != nil {
		t.Fatal(err)
	}

	want := &pfsenseapipb.ListGatewayGroupsResponse{Groups: []*pfsenseapipb.GatewayGroup{{
		Name:        "VPN_FAILOVER",
		Description: "KILL_SWITCH WG, fallback KILL_SWITCH OpenVPN",
		Trigger:     "downloss",
		Members: []*pfsenseapipb.GatewayGroupMember{
			{Gateway: "WG_VPN", Tier: 1},
			{Gateway: "OVPN_VPN", Tier: 2},
		},
	}}}
	if diff := cmp.Diff(want, got, protocmp.Transform()); diff != "" {
		t.Errorf("ListGatewayGroups (-want +got):\n%s", diff)
	}
}

func TestARefusedKeyIsDownWithWhatPfSenseSaid(t *testing.T) {
	srv := firewall(t, "bad-key")
	h, c := start(t, srv.URL)

	_, err := c.ListGateways(t.Context(), &pfsenseapipb.ListGatewaysRequest{})

	if status.Code(err) != codes.Unavailable || !strings.Contains(err.Error(), "401") || !strings.Contains(err.Error(), "Authentication failed.") {
		t.Errorf("ListGateways error = %v, want Unavailable naming the 401 and pfSense's message", err)
	}
	if got := h.Health(); got.State != sdk.Down || !strings.Contains(got.Message, "Authentication failed.") {
		t.Errorf("health = %v, want down naming pfSense's message", got)
	}
}

func TestAnUnreachableFirewallIsDown(t *testing.T) {
	srv := firewall(t, "baseline")
	srv.Close()
	h, _ := start(t, srv.URL)

	if got := h.Health(); got.State != sdk.Down || !strings.Contains(got.Message, "unreachable") {
		t.Errorf("health = %v, want down: unreachable", got)
	}
}

func TestHealthIsOKWhenTheFirewallAnswers(t *testing.T) {
	srv := firewall(t, "baseline")
	h, _ := start(t, srv.URL)

	if got := h.Health(); got.State != sdk.OK {
		t.Errorf("health = %v, want ok", got)
	}
}

func TestReadsAreSharedForTheFastTier(t *testing.T) {
	srv := firewall(t, "baseline")
	h, c := start(t, srv.URL)

	for range 3 {
		if _, err := c.ListGateways(t.Context(), &pfsenseapipb.ListGatewaysRequest{}); err != nil {
			t.Fatal(err)
		}
	}
	h.Health()

	if got := srv.hits.Load(); got != 1 {
		t.Errorf("%d requests to pfSense, want 1 shared by three reads and the health check", got)
	}
}

func TestListsInterfacesWithTheirCounters(t *testing.T) {
	srv := firewall(t, "baseline")
	_, c := start(t, srv.URL)

	got, err := c.ListInterfaces(t.Context(), &pfsenseapipb.ListInterfacesRequest{})
	if err != nil {
		t.Fatal(err)
	}

	if n := len(got.GetInterfaces()); n != 5 {
		t.Fatalf("%d interfaces, want 5", n)
	}
	want := []*pfsenseapipb.Interface{
		{Name: "wan", Description: "WAN", HwInterface: "vtnet0", Status: "up", Ipv4Address: "203.0.113.53", Ipv4Prefix: 24, Gateway: "203.0.113.100", InBytes: 26109122269, OutBytes: 1878184117, InPackets: 22864297, OutPackets: 8091698},
		{Name: "lan", Description: "LAN", HwInterface: "vtnet1", Status: "up", Ipv4Address: "198.18.1.1", Ipv4Prefix: 24, InBytes: 1655479121, OutBytes: 25222404095, InPackets: 7169645, OutPackets: 21912798},
		// A WireGuard interface no peer has used yet: no counters.
		{Name: "opt1", Description: "WIREGUARDREMOTEACCESS", HwInterface: "tun_wg1", Status: "up", Ipv4Address: "198.18.40.1", Ipv4Prefix: 24},
	}
	if diff := cmp.Diff(want, got.GetInterfaces()[:3], protocmp.Transform()); diff != "" {
		t.Errorf("ListInterfaces (-want +got):\n%s", diff)
	}
}

// pfSense gives an OpenVPN client interface's subnet as a netmask, not a
// prefix length.
func TestReadsAnInterfaceSubnetGivenAsANetmask(t *testing.T) {
	srv := firewall(t, "baseline")
	_, c := start(t, srv.URL)

	got, err := c.ListInterfaces(t.Context(), &pfsenseapipb.ListInterfacesRequest{})
	if err != nil {
		t.Fatal(err)
	}

	for _, i := range got.GetInterfaces() {
		if i.GetName() == "opt2" {
			if i.GetIpv4Prefix() != 16 {
				t.Errorf("opt2's prefix is %d, want 16 from 255.255.0.0", i.GetIpv4Prefix())
			}
			return
		}
	}
	t.Fatal("no opt2")
}

// network samples the counters every 5 s; a 2 s cache serves concurrent
// readers without ever handing a sample stale counters.
func TestInterfaceCountersAreCachedForTwoSeconds(t *testing.T) {
	srv := firewall(t, "baseline")
	_, c := start(t, srv.URL)
	read := func() time.Time {
		t.Helper()
		resp, err := c.ListInterfaces(t.Context(), &pfsenseapipb.ListInterfacesRequest{})
		if err != nil {
			t.Fatal(err)
		}
		if resp.GetReadAt() == nil {
			t.Fatal("no read_at")
		}
		return resp.GetReadAt().AsTime()
	}

	first := read()
	if second := read(); !second.Equal(first) {
		t.Errorf("read_at %v from the cache, want the first read's %v", second, first)
	}
	if got := srv.hits.Load(); got != 1 {
		t.Errorf("%d requests for two reads in a row, want 1", got)
	}
	time.Sleep(2100 * time.Millisecond)
	read()
	if got := srv.hits.Load(); got != 2 {
		t.Errorf("%d requests after 2 s, want 2", got)
	}
}

func TestListsWireguardTunnelsWithTheirPeers(t *testing.T) {
	srv := firewall(t, "baseline")
	_, c := start(t, srv.URL)

	got, err := c.ListWireguardTunnels(t.Context(), &pfsenseapipb.ListWireguardTunnelsRequest{})
	if err != nil {
		t.Fatal(err)
	}

	if n := len(got.GetTunnels()); n != 2 {
		t.Fatalf("%d tunnels, want 2 (the VPN Tunnel's included)", n)
	}
	want := []*pfsenseapipb.WireguardTunnel{
		{
			Name: "tun_wg1", Description: "Remote Access", Status: "up", ListenPort: 51333,
			Peers: []*pfsenseapipb.WireguardPeer{
				// Never connected: wg reports endpoint "(none)" and handshake 0.
				{PublicKey: "BEYg670lDINWndm6HepeqvFcG1Vtu9i213Oz43fsmP4=", Description: "Phone", AllowedIps: []string{"198.18.40.2/32"}},
				{PublicKey: "3DSY1uvmxh/dJZxoYlkRe4+x/ziorV+tX79ngdO2dR4=", Description: "Laptop", AllowedIps: []string{"198.18.40.3/32"}},
			},
		},
		{
			Name: "tun_wg2", Description: "VPN provider", Status: "up", ListenPort: 51820, RxBytes: 15801980136, TxBytes: 392017332,
			Peers: []*pfsenseapipb.WireguardPeer{{
				PublicKey: "o822xaDT9sDiY64O140R4zsB3eQYmMTjjQVuXcvw2Lc=", Description: "KILL_SWITCH AT#38", Endpoint: "203.0.113.195:51820",
				AllowedIps: []string{"0.0.0.0/0"}, LatestHandshake: timestamppb.New(time.Unix(1792516692, 0)), RxBytes: 15801980136, TxBytes: 392017480,
			}},
		},
	}
	if diff := cmp.Diff(want, got.GetTunnels(), protocmp.Transform()); diff != "" {
		t.Errorf("tunnels (-want +got):\n%s", diff)
	}
}

func TestListsTheFirewallRulesOnOneInterfaceInOrder(t *testing.T) {
	srv := firewall(t, "baseline")
	_, c := start(t, srv.URL)

	got, err := c.ListFirewallRules(t.Context(), &pfsenseapipb.ListFirewallRulesRequest{Interface: "opt1"})
	if err != nil {
		t.Fatal(err)
	}

	var descriptions []string
	for _, r := range got.GetRules() {
		descriptions = append(descriptions, r.GetDescription())
	}
	wantOrder := []string{"Anti Lockout rule", "WG clients - internet via VPN", "", "", "WG clients to WAN subnet", "WG clients to firewall", "VPN kill switch - WG clients never via WAN"}
	if diff := cmp.Diff(wantOrder, descriptions); diff != "" {
		t.Fatalf("rules on opt1 (-want +got):\n%s", diff)
	}
	want := []*pfsenseapipb.FirewallRule{
		{Type: "pass", Interfaces: []string{"opt1"}, IpProtocol: "inet", Protocol: "tcp", Source: "any", Destination: "lan", DestinationPort: "443", Description: "Anti Lockout rule"},
		// pfSense sends null for a rule on any protocol and port.
		{Type: "pass", Interfaces: []string{"opt1"}, IpProtocol: "inet", Source: "any", Destination: "!RFC1918", Description: "WG clients - internet via VPN"},
	}
	if diff := cmp.Diff(want, got.GetRules()[:2], protocmp.Transform()); diff != "" {
		t.Errorf("rules (-want +got):\n%s", diff)
	}
}

// The homelab has no disabled rule: the disabled-rule scenario has one.
func TestADisabledRuleIsListedAsDisabled(t *testing.T) {
	srv := firewall(t, "disabled-rule")
	_, c := start(t, srv.URL)

	got, err := c.ListFirewallRules(t.Context(), &pfsenseapipb.ListFirewallRulesRequest{Interface: "opt1"})
	if err != nil {
		t.Fatal(err)
	}

	if !got.GetRules()[3].GetDisabled() {
		t.Errorf("%q not disabled", got.GetRules()[3].GetDescription())
	}
	if got.GetRules()[2].GetDisabled() {
		t.Errorf("%q disabled, want enabled", got.GetRules()[2].GetDescription())
	}
}

func TestListsKeasLeasesWithStaticAndDynamicEntries(t *testing.T) {
	srv := firewall(t, "baseline")
	_, c := start(t, srv.URL)

	got, err := c.ListLeases(t.Context(), &pfsenseapipb.ListLeasesRequest{})
	if err != nil {
		t.Fatal(err)
	}

	// Kea's dynamic leases carry no interface (null).
	want := &pfsenseapipb.ListLeasesResponse{Leases: []*pfsenseapipb.Lease{
		{Ip: "198.18.1.102", Mac: "02:e6:29:fa:fc:39", Hostname: "laptop-2", Interface: "lan", ActiveStatus: "static", OnlineStatus: "idle/offline"},
		{Ip: "198.18.1.101", Mac: "02:61:c0:a3:8a:16", Hostname: "homeassistant", Interface: "lan", ActiveStatus: "static", OnlineStatus: "active/online"},
		{Ip: "198.18.1.12", Mac: "02:75:79:44:12:e3", Hostname: "operator-pc", Interface: "lan", ActiveStatus: "static", OnlineStatus: "active/online"},
		{Ip: "198.18.1.11", Mac: "02:b9:d2:5c:b9:9f", Hostname: "s22-von-operator", Interface: "lan", ActiveStatus: "static", OnlineStatus: "active/online"},
		{Ip: "198.18.1.10", Mac: "02:1c:d5:5e:3d:e5", Hostname: "LexmarkCX317dn", Interface: "lan", ActiveStatus: "static", OnlineStatus: "active/online"},
		{Ip: "198.18.1.7", Mac: "02:ff:c4:52:c5:c3", Hostname: "einstein", Interface: "lan", ActiveStatus: "static", OnlineStatus: "active/online"},
		{Ip: "198.18.1.6", Mac: "02:4a:a6:ab:ab:d4", Hostname: "AndroidTV", Interface: "lan", ActiveStatus: "static", OnlineStatus: "active/online"},
		{Ip: "198.18.1.5", Mac: "02:08:c2:85:3d:a1", Interface: "lan", ActiveStatus: "static", OnlineStatus: "active/online", Description: "Amazon Alexa"},
		{Ip: "198.18.1.4", Mac: "02:eb:2f:cc:08:07", Hostname: "lwip", Interface: "lan", ActiveStatus: "static", OnlineStatus: "active/online"},
		{Ip: "198.18.1.3", Mac: "02:56:74:bd:32:5b", Hostname: "w1", Interface: "lan", ActiveStatus: "static", OnlineStatus: "active/online", Description: "Access Point (OpenWrt)"},
		{Ip: "198.18.1.2", Mac: "02:5e:92:9d:f0:a6", Hostname: "w0", Interface: "lan", ActiveStatus: "static", OnlineStatus: "active/online"},
		{Ip: "198.18.1.26", Mac: "02:2d:14:a9:09:b6", Hostname: "a25-von-member", Starts: "2026/10/20 18:52:49", Ends: "2026/10/20 20:52:49", ActiveStatus: "active", OnlineStatus: "active/online"},
		{Ip: "198.18.1.25", Mac: "02:2f:c2:65:95:10", Starts: "2026/10/20 17:21:06", Ends: "2026/10/20 19:21:06", ActiveStatus: "active", OnlineStatus: "idle/offline"},
		{Ip: "198.18.1.22", Mac: "02:aa:97:4f:49:e7", Starts: "2026/10/20 19:11:33", Ends: "2026/10/20 21:11:33", ActiveStatus: "active", OnlineStatus: "active/online"},
	}}
	if diff := cmp.Diff(want, got, protocmp.Transform()); diff != "" {
		t.Errorf("ListLeases (-want +got):\n%s", diff)
	}
}

// The homelab's MACs are all lower case and none of its leases has expired:
// the dhcp-quirks scenario has an upper case MAC and an expired lease.
func TestListsKeasLeasesWithStaticEntriesAndLowerCaseMACs(t *testing.T) {
	srv := firewall(t, "dhcp-quirks")
	_, c := start(t, srv.URL)

	got, err := c.ListLeases(t.Context(), &pfsenseapipb.ListLeasesRequest{})
	if err != nil {
		t.Fatal(err)
	}

	want := &pfsenseapipb.ListLeasesResponse{Leases: []*pfsenseapipb.Lease{
		{Ip: "198.18.1.20", Mac: "02:5e:00:00:10:01", Hostname: "living-room-tv", Interface: "lan", ActiveStatus: "static", OnlineStatus: "active/online", Description: "Living room TV"},
		{Ip: "198.18.1.21", Mac: "02:5e:00:00:10:02", Hostname: "work-laptop", Interface: "lan", ActiveStatus: "static", OnlineStatus: "active/online", Description: "Work laptop"},
		{Ip: "198.18.1.22", Mac: "02:5e:00:00:10:03", Hostname: "printer", Interface: "lan", ActiveStatus: "static", OnlineStatus: "idle/offline"},
		{Ip: "198.18.1.150", Mac: "02:5e:00:00:20:01", Hostname: "galaxy-s24", Starts: "2026/10/20 08:02:11", Ends: "2026/10/20 10:02:11", ActiveStatus: "active", OnlineStatus: "active/online"},
		{Ip: "198.18.1.151", Mac: "02:5e:00:00:20:02", Starts: "2026/10/19 21:40:55", Ends: "2026/10/19 23:40:55", ActiveStatus: "expired", OnlineStatus: "idle/offline"},
	}}
	if diff := cmp.Diff(want, got, protocmp.Transform()); diff != "" {
		t.Errorf("ListLeases (-want +got):\n%s", diff)
	}
}

func TestListsDHCPServersWithTheirRangesAndStaticMappings(t *testing.T) {
	srv := firewall(t, "baseline")
	_, c := start(t, srv.URL)

	got, err := c.ListDhcpServers(t.Context(), &pfsenseapipb.ListDhcpServersRequest{})
	if err != nil {
		t.Fatal(err)
	}

	// A server without a range sends it as empty strings: no pool.
	want := &pfsenseapipb.ListDhcpServersResponse{Servers: []*pfsenseapipb.DhcpServer{
		{
			Interface: "lan",
			Enabled:   true,
			Pools:     []*pfsenseapipb.AddressRange{{From: "198.18.1.20", To: "198.18.1.99"}},
			StaticMappings: []*pfsenseapipb.StaticMapping{
				{Interface: "lan", Mac: "02:5e:92:9d:f0:a6", Ip: "198.18.1.2", Hostname: "w0"},
				{Interface: "lan", Mac: "02:56:74:bd:32:5b", Ip: "198.18.1.3", Hostname: "w1", Description: "Access Point (OpenWrt)"},
				{Interface: "lan", Mac: "02:eb:2f:cc:08:07", Ip: "198.18.1.4", Hostname: "lwip"},
				{Interface: "lan", Mac: "02:08:c2:85:3d:a1", Ip: "198.18.1.5", Description: "Amazon Alexa"},
				{Interface: "lan", Mac: "02:4a:a6:ab:ab:d4", Ip: "198.18.1.6", Hostname: "AndroidTV"},
				{Interface: "lan", Mac: "02:ff:c4:52:c5:c3", Ip: "198.18.1.7", Hostname: "einstein"},
				{Interface: "lan", Mac: "02:1c:d5:5e:3d:e5", Ip: "198.18.1.10", Hostname: "LexmarkCX317dn"},
				{Interface: "lan", Mac: "02:b9:d2:5c:b9:9f", Ip: "198.18.1.11", Hostname: "s22-von-operator"},
				{Interface: "lan", Mac: "02:75:79:44:12:e3", Ip: "198.18.1.12", Hostname: "operator-pc"},
				{Interface: "lan", Mac: "02:61:c0:a3:8a:16", Ip: "198.18.1.101", Hostname: "homeassistant"},
				{Interface: "lan", Mac: "02:e6:29:fa:fc:39", Ip: "198.18.1.102", Hostname: "laptop-2"},
			},
		},
		{Interface: "wan"},
		{Interface: "opt1"},
		{Interface: "opt3"},
	}}
	if diff := cmp.Diff(want, got, protocmp.Transform()); diff != "" {
		t.Errorf("ListDhcpServers (-want +got):\n%s", diff)
	}
}

// The homelab's DHCP server has no extra pool: the dhcp-quirks scenario has
// one, and an upper case MAC.
func TestListsDHCPServersWithTheirPoolsAndStaticMappings(t *testing.T) {
	srv := firewall(t, "dhcp-quirks")
	_, c := start(t, srv.URL)

	got, err := c.ListDhcpServers(t.Context(), &pfsenseapipb.ListDhcpServersRequest{})
	if err != nil {
		t.Fatal(err)
	}

	want := &pfsenseapipb.ListDhcpServersResponse{Servers: []*pfsenseapipb.DhcpServer{
		{
			Interface: "lan",
			Enabled:   true,
			Pools: []*pfsenseapipb.AddressRange{
				{From: "198.18.1.100", To: "198.18.1.199"},
				{From: "198.18.1.230", To: "198.18.1.239"},
			},
			StaticMappings: []*pfsenseapipb.StaticMapping{
				{Interface: "lan", Mac: "02:5e:00:00:10:01", Ip: "198.18.1.20", Hostname: "living-room-tv", Description: "Living room TV"},
				{Interface: "lan", Mac: "02:5e:00:00:10:02", Ip: "198.18.1.21", Hostname: "work-laptop", Description: "Work laptop"},
				{Interface: "lan", Mac: "02:5e:00:00:10:03", Ip: "198.18.1.22", Hostname: "printer"},
			},
		},
		{Interface: "opt1"},
	}}
	if diff := cmp.Diff(want, got, protocmp.Transform()); diff != "" {
		t.Errorf("ListDhcpServers (-want +got):\n%s", diff)
	}
}

func TestListsAliasesAndOffersThemAsChoices(t *testing.T) {
	srv := firewall(t, "baseline")
	_, c := start(t, srv.URL)

	got, err := c.ListAliases(t.Context(), &pfsenseapipb.ListAliasesRequest{})
	if err != nil {
		t.Fatal(err)
	}

	var names []string
	for _, a := range got.GetAliases() {
		names = append(names, a.GetName())
	}
	wantNames := []string{"DNS_SERVERS", "FRITZBOX_IP6", "DNS_SERVER", "RFC1918", "VPN_BYPASS", "WAN_SUBNETWORK", "VictronAllow", "KILL_SWITCH", "DDASH_SMOKE"}
	if diff := cmp.Diff(wantNames, names); diff != "" {
		t.Fatalf("aliases (-want +got):\n%s", diff)
	}
	want := []*pfsenseapipb.Alias{
		{Name: "DNS_SERVER", Type: "host", Addresses: []string{"198.18.1.202", "198.18.1.1", "198.18.30.1"}, Details: []string{"Adguard DNS Server", "pfsense DNS Server", "KILL_SWITCH DNS"}},
		{Name: "RFC1918", Type: "network", Description: "RFC1918", Addresses: []string{"203.0.113.95/16", "203.0.113.11/12", "203.0.113.111/8"}, Details: []string{"Entry added Sat, 23 Dec 2023 23:46:03 +0100", "Entry added Sat, 23 Dec 2023 23:46:03 +0100", "Entry added Sun, 24 Dec 2023 10:38:49 +0100"}},
		bypassAlias(),
	}
	if diff := cmp.Diff(want, got.GetAliases()[2:5], protocmp.Transform()); diff != "" {
		t.Errorf("aliases (-want +got):\n%s", diff)
	}
	// An alias without members.
	if diff := cmp.Diff(&pfsenseapipb.Alias{Name: "DDASH_SMOKE", Type: "host", Description: "TEST"}, got.GetAliases()[8], protocmp.Transform()); diff != "" {
		t.Errorf("DDASH_SMOKE (-want +got):\n%s", diff)
	}
	// Every alias here holds addresses, so every one is a choice.
	var choices []*configpb.Choice
	for _, n := range wantNames {
		label := n
		switch n {
		case "RFC1918":
			label = "RFC1918 — RFC1918"
		case "DDASH_SMOKE":
			label = "DDASH_SMOKE — TEST"
		}
		choices = append(choices, &configpb.Choice{Value: n, Label: label})
	}
	if diff := cmp.Diff(&configpb.Choices{Choices: choices}, got.GetChoices(), protocmp.Transform()); diff != "" {
		t.Errorf("choices (-want +got):\n%s", diff)
	}
}

// The homelab has no port alias: the port-alias scenario has one.
func TestListsAliasesAndOffersTheAddressAliasesAsChoices(t *testing.T) {
	srv := firewall(t, "port-alias")
	_, c := start(t, srv.URL)

	got, err := c.ListAliases(t.Context(), &pfsenseapipb.ListAliasesRequest{})
	if err != nil {
		t.Fatal(err)
	}

	want := &pfsenseapipb.ListAliasesResponse{
		Aliases: []*pfsenseapipb.Alias{
			{Name: "VPN_BYPASS", Type: "host", Description: "Leaves through the WAN, not the VPN", Addresses: []string{"198.18.1.21", "198.18.1.150", "198.18.2.10"}, Details: []string{"Work laptop", "Phone", "Media server"}},
			{Name: "KILL_SWITCH", Type: "host", Description: "Never leaves through the WAN", Addresses: []string{"198.18.1.20", "198.18.1.22"}, Details: []string{"TV", "Printer"}},
			{Name: "SERVERS", Type: "network", Addresses: []string{"198.18.2.0/28"}, Details: []string{"Server rack"}},
			{Name: "WEB_PORTS", Type: "port", Description: "HTTP and HTTPS", Addresses: []string{"80", "443"}, Details: []string{"", ""}},
		},
		// A port alias can't hold a Device.
		Choices: &configpb.Choices{Choices: []*configpb.Choice{
			{Value: "VPN_BYPASS", Label: "VPN_BYPASS — Leaves through the WAN, not the VPN"},
			{Value: "KILL_SWITCH", Label: "KILL_SWITCH — Never leaves through the WAN"},
			{Value: "SERVERS", Label: "SERVERS"},
		}},
	}
	if diff := cmp.Diff(want, got, protocmp.Transform()); diff != "" {
		t.Errorf("ListAliases (-want +got):\n%s", diff)
	}
}

func TestTheURLMustBeHTTPOrHTTPS(t *testing.T) {
	h := sdktest.New(t, pfsenseapi.Definition())

	err := h.Start(&pfsenseapipb.PfsenseApiConfig{Url: "tcp://198.18.1.1", ApiKey: apiKey})

	errs := sdk.FieldErrorsOf(err)
	if len(errs) != 1 || errs[0].Path != "url" {
		t.Errorf("Start = %v, want one error on url", err)
	}
}
