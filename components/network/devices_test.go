package network_test

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/testing/protocmp"

	"github.com/itsKontra/ddash/components/network"
	"github.com/itsKontra/ddash/components/network/networkpb"
	"github.com/itsKontra/ddash/components/pfsense-api/pfsenseapipb"
	"github.com/itsKontra/ddash/sdk"
	"github.com/itsKontra/ddash/sdk/sdktest"
)

func (f *firewall) ListLeases(context.Context, *pfsenseapipb.ListLeasesRequest, ...grpc.CallOption) (*pfsenseapipb.ListLeasesResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return nil, f.err
	}
	return &pfsenseapipb.ListLeasesResponse{Leases: f.leases}, nil
}

func (f *firewall) ListDhcpServers(context.Context, *pfsenseapipb.ListDhcpServersRequest, ...grpc.CallOption) (*pfsenseapipb.ListDhcpServersResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return nil, f.err
	}
	return &pfsenseapipb.ListDhcpServersResponse{Servers: proto.CloneOf(&pfsenseapipb.ListDhcpServersResponse{Servers: f.dhcp()}).Servers}, nil
}

// dhcp is the DHCP servers the firewall holds, the home ones until set;
// f.mu is held.
func (f *firewall) dhcp() []*pfsenseapipb.DhcpServer {
	if f.servers == nil {
		f.servers = homeServers()
	}
	return f.servers
}

// CreateStaticMapping adds the mapping to its interface's server, as
// pfsense-api does on the firewall.
func (f *firewall) CreateStaticMapping(_ context.Context, req *pfsenseapipb.CreateStaticMappingRequest, _ ...grpc.CallOption) (*pfsenseapipb.CreateStaticMappingResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.writes = append(f.writes, req)
	if f.err != nil {
		return nil, f.err
	}
	m := proto.CloneOf(req.GetMapping())
	for _, s := range f.dhcp() {
		if s.GetInterface() == m.GetInterface() {
			s.StaticMappings = append(s.StaticMappings, m)
			return &pfsenseapipb.CreateStaticMappingResponse{Mapping: m}, nil
		}
	}
	return nil, status.Errorf(codes.NotFound, "pfSense has no DHCP server on %q", m.GetInterface())
}

// RenameStaticMapping sets the description of the MAC's mapping.
func (f *firewall) RenameStaticMapping(_ context.Context, req *pfsenseapipb.RenameStaticMappingRequest, _ ...grpc.CallOption) (*pfsenseapipb.RenameStaticMappingResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.writes = append(f.writes, req)
	if f.err != nil {
		return nil, f.err
	}
	for _, s := range f.dhcp() {
		for _, m := range s.GetStaticMappings() {
			if s.GetInterface() == req.GetInterface() && m.GetMac() == req.GetMac() {
				m.Description = req.GetDescription()
				return &pfsenseapipb.RenameStaticMappingResponse{Mapping: proto.CloneOf(m)}, nil
			}
		}
	}
	return nil, status.Errorf(codes.NotFound, "%s has no static mapping on %s", req.GetMac(), req.GetInterface())
}

// written returns the write requests the firewall received, in order.
func (f *firewall) written() []proto.Message {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.writes)
}

func (f *firewall) ListAliases(context.Context, *pfsenseapipb.ListAliasesRequest, ...grpc.CallOption) (*pfsenseapipb.ListAliasesResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return nil, f.err
	}
	if f.aliases == nil {
		return &pfsenseapipb.ListAliasesResponse{Aliases: homeAliases()}, nil
	}
	return &pfsenseapipb.ListAliasesResponse{Aliases: f.aliases}, nil
}

// homeConfig is network's config on the home firewall: Adopt reserves
// from .20-.49, below the DHCP pool.
func homeConfig() *networkpb.NetworkConfig {
	return &networkpb.NetworkConfig{BypassAlias: "VPN_BYPASS", ReservedRange: "198.18.1.20-198.18.1.49"}
}

// homeServers is the LAN's DHCP server: a pool of .100-.199 plus .230-.239,
// and three static mappings. opt1's server is off.
func homeServers() []*pfsenseapipb.DhcpServer {
	return []*pfsenseapipb.DhcpServer{
		{
			Interface: "lan",
			Enabled:   true,
			Pools:     []*pfsenseapipb.AddressRange{{From: "198.18.1.100", To: "198.18.1.199"}, {From: "198.18.1.230", To: "198.18.1.239"}},
			StaticMappings: []*pfsenseapipb.StaticMapping{
				{Interface: "lan", Mac: "02:5e:00:00:10:01", Ip: "198.18.1.20", Hostname: "living-room-tv", Description: "Living room TV"},
				{Interface: "lan", Mac: "02:5e:00:00:10:02", Ip: "198.18.1.21", Hostname: "work-laptop", Description: "Work laptop"},
				{Interface: "lan", Mac: "02:5e:00:00:10:03", Ip: "198.18.1.22", Hostname: "printer"},
			},
		},
		{Interface: "opt1", Pools: []*pfsenseapipb.AddressRange{{From: "198.18.1.20", To: "198.18.1.29"}}},
	}
}

// homeAliases: the bypass alias holds two Devices and a server; the
// kill-switch two Devices; SERVERS a network; MOBILE nests in PHONES.
func homeAliases() []*pfsenseapipb.Alias {
	return []*pfsenseapipb.Alias{
		{Name: "VPN_BYPASS", Type: "host", Addresses: []string{"198.18.1.21", "198.18.1.150", "198.18.2.10"}, Details: []string{"Work laptop", "Phone", "Media server"}},
		{Name: "KILL_SWITCH", Type: "host", Description: "Never leaves through the WAN", Addresses: []string{"198.18.1.20", "198.18.1.22"}},
		{Name: "SERVERS", Type: "network", Addresses: []string{"198.18.2.0/28"}},
		{Name: "MOBILE", Type: "host", Addresses: []string{"198.18.1.140-198.18.1.160"}},
		{Name: "PHONES", Type: "host", Description: "Phones and tablets", Addresses: []string{"MOBILE"}},
		{Name: "WEB_PORTS", Type: "port", Addresses: []string{"80", "443"}},
	}
}

// homeLeases is what Kea reports: the three static mappings (the printer
// is off), a phone on a dynamic lease, and an expired lease.
func homeLeases() []*pfsenseapipb.Lease {
	return []*pfsenseapipb.Lease{
		{Ip: "198.18.1.20", Mac: "02:5e:00:00:10:01", Hostname: "living-room-tv", Interface: "lan", ActiveStatus: "static", OnlineStatus: "active/online", Description: "Living room TV"},
		{Ip: "198.18.1.21", Mac: "02:5e:00:00:10:02", Hostname: "work-laptop", Interface: "lan", ActiveStatus: "static", OnlineStatus: "active/online", Description: "Work laptop"},
		{Ip: "198.18.1.22", Mac: "02:5e:00:00:10:03", Hostname: "printer", Interface: "lan", ActiveStatus: "static", OnlineStatus: "idle/offline"},
		{Ip: "198.18.1.150", Mac: "02:5e:00:00:20:01", Hostname: "galaxy-s24", Starts: "2026/10/20 08:02:11", Ends: "2026/10/20 10:02:11", ActiveStatus: "active", OnlineStatus: "active/online"},
		{Ip: "198.18.1.151", Mac: "02:5e:00:00:20:02", Starts: "2026/10/19 21:40:55", Ends: "2026/10/19 23:40:55", ActiveStatus: "expired", OnlineStatus: "idle/offline"},
	}
}

func devices(t *testing.T, c networkpb.NetworkServiceClient) *networkpb.ListDevicesResponse {
	t.Helper()
	resp, err := c.ListDevices(t.Context(), &networkpb.ListDevicesRequest{})
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func TestDevicesJoinLeasesStaticMappingsAndTheBypassAliasByMAC(t *testing.T) {
	_, c := start(t, &firewall{leases: homeLeases()})

	got := devices(t, c)

	want := &networkpb.ListDevicesResponse{
		BypassAlias: "VPN_BYPASS",
		Devices: []*networkpb.Device{
			{
				Mac: "02:5e:00:00:10:01", Name: "Living room TV", Ip: "198.18.1.20", Online: true, Adopted: true,
				Lease:       &networkpb.DeviceLease{Ip: "198.18.1.20", Hostname: "living-room-tv", Interface: "lan", Status: "static", OnlineStatus: "active/online"},
				Reservation: &networkpb.Reservation{Interface: "lan", Ip: "198.18.1.20", Hostname: "living-room-tv", Description: "Living room TV"},
				Aliases:     []*networkpb.DeviceAlias{{Name: "KILL_SWITCH", Description: "Never leaves through the WAN"}},
			},
			{
				Mac: "02:5e:00:00:10:02", Name: "Work laptop", Ip: "198.18.1.21", Online: true, Adopted: true, Direct: true,
				Lease:       &networkpb.DeviceLease{Ip: "198.18.1.21", Hostname: "work-laptop", Interface: "lan", Status: "static", OnlineStatus: "active/online"},
				Reservation: &networkpb.Reservation{Interface: "lan", Ip: "198.18.1.21", Hostname: "work-laptop", Description: "Work laptop"},
			},
			{
				// No description: the static mapping's hostname names it.
				Mac: "02:5e:00:00:10:03", Name: "printer", Ip: "198.18.1.22", Adopted: true,
				Lease:       &networkpb.DeviceLease{Ip: "198.18.1.22", Hostname: "printer", Interface: "lan", Status: "static", OnlineStatus: "idle/offline"},
				Reservation: &networkpb.Reservation{Interface: "lan", Ip: "198.18.1.22", Hostname: "printer"},
				Aliases:     []*networkpb.DeviceAlias{{Name: "KILL_SWITCH", Description: "Never leaves through the WAN"}},
			},
			{
				// A New Device: a dynamic lease and no static mapping.
				Mac: "02:5e:00:00:20:01", Name: "galaxy-s24", Ip: "198.18.1.150", Online: true, Direct: true,
				Lease: &networkpb.DeviceLease{Ip: "198.18.1.150", Hostname: "galaxy-s24", Starts: "2026/10/20 08:02:11", Ends: "2026/10/20 10:02:11", Status: "active", OnlineStatus: "active/online"},
				// In MOBILE by its range, and so in PHONES, which nests MOBILE.
				Aliases: []*networkpb.DeviceAlias{{Name: "MOBILE"}, {Name: "PHONES", Description: "Phones and tablets"}},
			},
			{
				Mac: "02:5e:00:00:20:02", Ip: "198.18.1.151",
				Lease:   &networkpb.DeviceLease{Ip: "198.18.1.151", Starts: "2026/10/19 21:40:55", Ends: "2026/10/19 23:40:55", Status: "expired", OnlineStatus: "idle/offline"},
				Aliases: []*networkpb.DeviceAlias{{Name: "MOBILE"}, {Name: "PHONES", Description: "Phones and tablets"}},
			},
		},
	}
	if diff := cmp.Diff(want, got, protocmp.Transform()); diff != "" {
		t.Errorf("ListDevices (-want +got):\n%s", diff)
	}
}

func TestAServerWithAFixedAddressIsNoDeviceAndNeverShown(t *testing.T) {
	// 198.18.2.10 is in the bypass alias and SERVERS but has no lease and
	// no static mapping: it set its own address.
	_, c := start(t, &firewall{leases: homeLeases()})

	got := devices(t, c)

	b, err := protojson.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	for _, hidden := range []string{"198.18.2.10", "Media server", "SERVERS", "198.18.2.0/28"} {
		if strings.Contains(string(b), hidden) {
			t.Errorf("ListDevices shows %q: %s", hidden, b)
		}
	}
}

func TestADeviceAdoptedOnItsOldLeaseShowsItsReservedAddressAndTheLease(t *testing.T) {
	fw := &firewall{leases: []*pfsenseapipb.Lease{
		// Adopted at .23 a minute ago; it moves on lease renewal.
		{Ip: "198.18.1.23", Mac: "02:5e:00:00:20:01", Hostname: "galaxy-s24", Interface: "lan", ActiveStatus: "static", OnlineStatus: "idle/offline"},
		{Ip: "198.18.1.150", Mac: "02:5E:00:00:20:01", Hostname: "galaxy-s24", Starts: "2026/10/20 08:02:11", Ends: "2026/10/20 10:02:11", ActiveStatus: "active", OnlineStatus: "active/online"},
	}}
	fw.servers = homeServers()
	fw.servers[0].StaticMappings = []*pfsenseapipb.StaticMapping{{Interface: "lan", Mac: "02:5e:00:00:20:01", Ip: "198.18.1.23", Hostname: "phone", Description: "Phone"}}
	_, c := start(t, fw)

	got := devices(t, c).GetDevices()

	want := []*networkpb.Device{{
		Mac: "02:5e:00:00:20:01", Name: "Phone", Ip: "198.18.1.23", Online: true, Adopted: true,
		Lease:       &networkpb.DeviceLease{Ip: "198.18.1.150", Hostname: "galaxy-s24", Starts: "2026/10/20 08:02:11", Ends: "2026/10/20 10:02:11", Status: "active", OnlineStatus: "active/online"},
		Reservation: &networkpb.Reservation{Interface: "lan", Ip: "198.18.1.23", Hostname: "phone", Description: "Phone"},
	}}
	if diff := cmp.Diff(want, got, protocmp.Transform()); diff != "" {
		t.Errorf("devices (-want +got):\n%s", diff)
	}
}

func TestDevicesFailNamingTheBypassAliasWhenItIsGone(t *testing.T) {
	fw := &firewall{leases: homeLeases()}
	_, c := start(t, fw)
	fw.mu.Lock()
	fw.aliases = homeAliases()[1:]
	fw.mu.Unlock()

	_, err := c.ListDevices(t.Context(), &networkpb.ListDevicesRequest{})

	if status.Code(err) != codes.FailedPrecondition || !strings.Contains(err.Error(), "VPN_BYPASS") {
		t.Errorf("ListDevices = %v, want FailedPrecondition naming the missing bypass alias", err)
	}
}

func TestDevicesFailNamingPfsenseAPIsError(t *testing.T) {
	fw := &firewall{err: status.Error(codes.Unavailable, "pfSense unreachable: connection refused")}
	_, c := start(t, fw)

	_, err := c.ListDevices(t.Context(), &networkpb.ListDevicesRequest{})

	if status.Code(err) != codes.Unavailable || !strings.Contains(err.Error(), "pfSense unreachable") {
		t.Errorf("ListDevices = %v, want Unavailable naming pfsense-api's error", err)
	}
}

// startWith starts network on fw with cfg and returns Start's error.
func startWith(t *testing.T, fw *firewall, cfg *networkpb.NetworkConfig) error {
	h := sdktest.New(t, network.Definition(), sdktest.WithClient[pfsenseapipb.PfsenseApiServiceClient](fw))
	return h.Start(cfg)
}

func TestStartRefusesABypassAliasTheFirewallDoesNotHave(t *testing.T) {
	cfg := homeConfig()
	cfg.BypassAlias = "NO_SUCH"

	errs := sdk.FieldErrorsOf(startWith(t, &firewall{}, cfg))

	if len(errs) != 1 || errs[0].Path != "bypass_alias" || !strings.Contains(errs[0].Message, "NO_SUCH") {
		t.Errorf("Start errors = %v, want one on bypass_alias naming NO_SUCH", errs)
	}
}

func TestStartRefusesAPortAliasAsTheBypassAlias(t *testing.T) {
	cfg := homeConfig()
	cfg.BypassAlias = "WEB_PORTS"

	errs := sdk.FieldErrorsOf(startWith(t, &firewall{}, cfg))

	if len(errs) != 1 || errs[0].Path != "bypass_alias" || !strings.Contains(errs[0].Message, "port alias") {
		t.Errorf("Start errors = %v, want one on bypass_alias: a port alias", errs)
	}
}

func TestStartRefusesAReservedRangeOverlappingAnEnabledDHCPPool(t *testing.T) {
	for _, tc := range []struct{ rng, pool string }{
		{"198.18.1.190-198.18.1.209", "198.18.1.100-198.18.1.199"},
		{"198.18.1.235-198.18.1.235", "198.18.1.230-198.18.1.239"},
		{"198.18.1.0-198.18.1.255", "198.18.1.100-198.18.1.199"},
	} {
		t.Run(tc.rng, func(t *testing.T) {
			cfg := homeConfig()
			cfg.ReservedRange = tc.rng

			errs := sdk.FieldErrorsOf(startWith(t, &firewall{}, cfg))

			if len(errs) != 1 || errs[0].Path != "reserved_range" || !strings.Contains(errs[0].Message, tc.pool) {
				t.Errorf("Start errors = %v, want one on reserved_range naming the pool %s", errs, tc.pool)
			}
		})
	}
}

func TestStartAcceptsAReservedRangeOutsideThePoolsOfEnabledServers(t *testing.T) {
	// opt1's disabled server has a pool over .20-.29; it hands nothing out.
	if err := startWith(t, &firewall{}, homeConfig()); err != nil {
		t.Errorf("Start = %v, want ok", err)
	}
}

func TestStartCantCheckAndSoAcceptsWhilePfsenseAPIFails(t *testing.T) {
	fw := &firewall{err: status.Error(codes.Unavailable, "pfSense unreachable: connection refused")}
	cfg := homeConfig()
	cfg.BypassAlias = "NO_SUCH"

	if err := startWith(t, fw, cfg); err != nil {
		t.Errorf("Start = %v, want ok: a firewall that is down at boot must not unconfigure network", err)
	}
}

func TestStartRefusesAReservedRangeThatIsNoRange(t *testing.T) {
	cfg := homeConfig()
	cfg.ReservedRange = "198.18.1.49-198.18.1.20"

	errs := sdk.FieldErrorsOf(startWith(t, &firewall{}, cfg))

	if len(errs) != 1 || errs[0].Path != "reserved_range" {
		t.Errorf("Start errors = %v, want one on reserved_range", errs)
	}
}
