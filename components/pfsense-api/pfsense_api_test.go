package pfsenseapi_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/google/go-cmp/cmp"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/testing/protocmp"

	pfsenseapi "github.com/itsKontra/ddash/components/pfsense-api"
	"github.com/itsKontra/ddash/components/pfsense-api/pfsenseapipb"
	configpb "github.com/itsKontra/ddash/proto/ddash/config"
	"github.com/itsKontra/ddash/sdk"
	"github.com/itsKontra/ddash/sdk/fixture"
	"github.com/itsKontra/ddash/sdk/sdktest"
)

const apiKey = "0123456789abcdef0123456789abcdef"

// fakeFirewall is pfSense's REST API serving a scenario's fixtures.
type fakeFirewall struct {
	*httptest.Server
	hits atomic.Int32
}

// firewall fakes pfSense's REST API serving a scenario. Like the real one
// it refuses a request without the configured key in X-API-Key.
func firewall(t *testing.T, scenario string) *fakeFirewall {
	scenarioHandler := fixture.Scenario("testdata/"+scenario, func(request string, err error) { t.Errorf("fixture: %v", err) })
	f := &fakeFirewall{}
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
		{Name: "WAN_DHCP", Status: "online", Substatus: "none", DelayMs: proto.Float64(8.412), StddevMs: proto.Float64(0.731), LossPercent: proto.Float64(0), MonitorIp: "203.0.113.1", SourceIp: "203.0.113.10"},
		{Name: "WG_VPN", Status: "online", Substatus: "none", DelayMs: proto.Float64(24.107), StddevMs: proto.Float64(1.92), LossPercent: proto.Float64(0), MonitorIp: "198.18.30.1", SourceIp: "198.18.30.2"},
		{Name: "OVPN_VPN", Status: "online", Substatus: "none", DelayMs: proto.Float64(31.85), StddevMs: proto.Float64(2.4), LossPercent: proto.Float64(0), MonitorIp: "198.18.31.1", SourceIp: "198.18.31.6"},
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
		Description: "WireGuard first, OpenVPN fallback",
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

func TestListsKeasLeasesWithStaticEntriesAndLowerCaseMACs(t *testing.T) {
	srv := firewall(t, "baseline")
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

func TestListsDHCPServersWithTheirPoolsAndStaticMappings(t *testing.T) {
	srv := firewall(t, "baseline")
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

func TestListsAliasesAndOffersTheAddressAliasesAsChoices(t *testing.T) {
	srv := firewall(t, "baseline")
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
