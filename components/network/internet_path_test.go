package network_test

import (
	"context"
	"net/netip"
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/testing/protocmp"

	"github.com/itsKontra/ddash/components/network/networkpb"
	"github.com/itsKontra/ddash/components/pfsense-api/pfsenseapipb"
)

// MoveAliasAddress takes the address out of the from aliases and appends
// it to to, as pfsense-api does on the firewall.
func (f *firewall) MoveAliasAddress(_ context.Context, req *pfsenseapipb.MoveAliasAddressRequest, _ ...grpc.CallOption) (*pfsenseapipb.MoveAliasAddressResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.writes = append(f.writes, req)
	if f.err != nil {
		return nil, f.err
	}
	ip := netip.MustParseAddr(req.GetAddress())
	for _, name := range append(slices.Clone(req.GetFrom()), req.GetTo()) {
		if name != "" && f.alias(name) == nil {
			return nil, status.Errorf(codes.NotFound, "pfSense has no alias %q", name)
		}
	}
	for _, name := range req.GetFrom() {
		a := f.alias(name)
		for i := len(a.Addresses) - 1; i >= 0; i-- {
			if m, err := netip.ParseAddr(a.Addresses[i]); err == nil && m == ip {
				a.Addresses = slices.Delete(a.Addresses, i, i+1)
				if i < len(a.Details) {
					a.Details = slices.Delete(a.Details, i, i+1)
				}
			}
		}
	}
	if a := f.alias(req.GetTo()); a != nil && !slices.Contains(a.Addresses, req.GetAddress()) {
		a.Addresses = append(a.Addresses, req.GetAddress())
		a.Details = append(a.Details, req.GetDetail())
	}
	return &pfsenseapipb.MoveAliasAddressResponse{}, nil
}

// alias is the alias named name, nil when there is none; f.mu is held.
func (f *firewall) alias(name string) *pfsenseapipb.Alias {
	for _, a := range f.aliasList() {
		if a.GetName() == name {
			return a
		}
	}
	return nil
}

// members returns the alias's addresses as the firewall holds them now.
func (f *firewall) members(name string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.alias(name).GetAddresses())
}

func setPath(t *testing.T, c networkpb.NetworkServiceClient, mac string, path networkpb.InternetPath) *networkpb.Device {
	t.Helper()
	got, err := c.SetInternetPath(t.Context(), &networkpb.SetInternetPathRequest{Mac: mac, Path: path})
	if err != nil {
		t.Fatal(err)
	}
	return got.GetDevice()
}

func listed(t *testing.T, c networkpb.NetworkServiceClient, mac string) *networkpb.Device {
	t.Helper()
	for _, d := range devices(t, c).GetDevices() {
		if d.GetMac() == mac {
			return d
		}
	}
	t.Fatalf("ListDevices has no %s", mac)
	return nil
}

func TestDirectMovesTheAdoptedDevicesAddressIntoTheBypassAliasUnderItsName(t *testing.T) {
	fw := &firewall{leases: homeLeases()}
	_, c := start(t, fw)

	d := setPath(t, c, "02:5e:00:00:10:01", direct)

	want := []proto.Message{&pfsenseapipb.MoveAliasAddressRequest{Address: "198.18.1.20", To: "VPN_BYPASS", Detail: "Living room TV"}}
	if diff := cmp.Diff(want, fw.written(), protocmp.Transform()); diff != "" {
		t.Errorf("writes (-want +got):\n%s", diff)
	}
	if d.GetInternetPath() != direct {
		t.Errorf("Device after Direct = %v, want Direct", d)
	}
	// ListDevices shows it at once, not after the SLOW cache expires.
	if listed(t, c, "02:5e:00:00:10:01").GetInternetPath() != direct {
		t.Error("ListDevices after Direct has the TV Tunnelled")
	}
}

func TestTunnelledTakesOnlyTheDevicesAddressOutOfTheBypassAlias(t *testing.T) {
	fw := &firewall{leases: homeLeases()}
	_, c := start(t, fw)

	d := setPath(t, c, "02:5e:00:00:10:02", tunnelled)

	want := []proto.Message{&pfsenseapipb.MoveAliasAddressRequest{Address: "198.18.1.21", From: []string{"VPN_BYPASS"}, Detail: "Work laptop"}}
	if diff := cmp.Diff(want, fw.written(), protocmp.Transform()); diff != "" {
		t.Errorf("writes (-want +got):\n%s", diff)
	}
	if d.GetInternetPath() != tunnelled {
		t.Errorf("Device after Tunnelled = %v, want Tunnelled", d)
	}
	// The other Device and the server, which is no Device, stay.
	if got := fw.members("VPN_BYPASS"); !slices.Equal(got, []string{"198.18.1.150", "198.18.2.10"}) {
		t.Errorf("VPN_BYPASS = %v, want the phone and the server left", got)
	}
}

func TestNoInternetMovesTheAddressFromTheBypassAliasToTheBlockAliasInOneWrite(t *testing.T) {
	fw := &firewall{leases: homeLeases()}
	_, c := startConfigured(t, fw, blockingConfig())

	d := setPath(t, c, "02:5e:00:00:10:02", noInternet)

	want := []proto.Message{&pfsenseapipb.MoveAliasAddressRequest{Address: "198.18.1.21", From: []string{"VPN_BYPASS"}, To: "KILL_SWITCH", Detail: "Work laptop"}}
	if diff := cmp.Diff(want, fw.written(), protocmp.Transform()); diff != "" {
		t.Errorf("writes (-want +got):\n%s", diff)
	}
	if d.GetInternetPath() != noInternet {
		t.Errorf("Device after No Internet = %v, want No Internet", d)
	}
}

func TestAPathTheDeviceIsAlreadyOnWritesNothing(t *testing.T) {
	for name, tc := range map[string]struct {
		mac  string
		path networkpb.InternetPath
		// extra is a bypass alias member that is no Device's address.
		extra string
	}{
		"Direct on a Direct Device":                   {"02:5e:00:00:10:02", direct, ""},
		"Tunnelled on a Tunnelled Device":             {"02:5e:00:00:10:03", tunnelled, ""},
		"Direct on a Device Direct through a network": {"02:5e:00:00:10:01", direct, "198.18.1.20/32"},
	} {
		t.Run(name, func(t *testing.T) {
			fw := &firewall{leases: homeLeases(), aliases: homeAliases()}
			if tc.extra != "" {
				fw.aliases[0].Addresses = append(fw.aliases[0].Addresses, tc.extra)
				fw.aliases[0].Details = append(fw.aliases[0].Details, "Other")
			}
			_, c := start(t, fw)

			if d := setPath(t, c, tc.mac, tc.path); d.GetInternetPath() != tc.path {
				t.Errorf("Device = %v, want %v", d, tc.path)
			}
			if w := fw.written(); len(w) != 0 {
				t.Errorf("writes = %v, want none", w)
			}
		})
	}
}

func TestAPathOnANewDeviceAdoptsItFirstAtTheLowestFreeAddress(t *testing.T) {
	fw := &firewall{leases: homeLeases()}
	_, c := start(t, fw)

	// The tablet's expired lease sent no hostname, so its MAC names it.
	d := setPath(t, c, "02:5e:00:00:20:02", direct)

	want := []proto.Message{
		&pfsenseapipb.CreateStaticMappingRequest{Mapping: &pfsenseapipb.StaticMapping{Interface: "lan", Mac: "02:5e:00:00:20:02", Ip: "198.18.1.23", Description: "02:5e:00:00:20:02"}},
		&pfsenseapipb.MoveAliasAddressRequest{Address: "198.18.1.23", To: "VPN_BYPASS", Detail: "02:5e:00:00:20:02"},
	}
	if diff := cmp.Diff(want, fw.written(), protocmp.Transform()); diff != "" {
		t.Errorf("writes (-want +got):\n%s", diff)
	}
	if !d.GetAdopted() || d.GetInternetPath() != direct || d.GetIp() != "198.18.1.23" {
		t.Errorf("Device = %v, want Adopted at .23 and Direct", d)
	}
}

func TestTunnelledOnANewDeviceAdoptsItUnderItsHostname(t *testing.T) {
	fw := &firewall{leases: homeLeases()}
	_, c := start(t, fw)

	// The phone is Direct on its lease's address; its reserved one isn't
	// in the alias, so Adopting it is all it takes.
	d := setPath(t, c, "02:5e:00:00:20:01", tunnelled)

	want := []proto.Message{
		&pfsenseapipb.CreateStaticMappingRequest{Mapping: &pfsenseapipb.StaticMapping{Interface: "lan", Mac: "02:5e:00:00:20:01", Ip: "198.18.1.23", Hostname: "galaxy-s24", Description: "galaxy-s24"}},
	}
	if diff := cmp.Diff(want, fw.written(), protocmp.Transform()); diff != "" {
		t.Errorf("writes (-want +got):\n%s", diff)
	}
	if !d.GetAdopted() || d.GetInternetPath() != tunnelled {
		t.Errorf("Device = %v, want Adopted and Tunnelled", d)
	}
	// The lease's address is no Device's any more: it stays in the alias.
	if got := fw.members("VPN_BYPASS"); !slices.Contains(got, "198.18.1.150") {
		t.Errorf("VPN_BYPASS = %v, want .150 untouched", got)
	}
}

func TestADeviceInAnAliasThroughAMemberThatIsNoDeviceCantBeMovedOut(t *testing.T) {
	for _, tc := range []struct {
		alias string
		to    networkpb.InternetPath
		stays string
	}{
		{"VPN_BYPASS", tunnelled, "stays Direct"},
		{"KILL_SWITCH", tunnelled, "stays No Internet"},
		{"KILL_SWITCH", direct, "stays No Internet"},
	} {
		for kind, member := range map[string]string{"network": "198.18.1.16/28", "range": "198.18.1.18-198.18.1.25", "nested alias": "PHONES_TOO"} {
			t.Run(tc.alias+" through a "+kind+" to "+tc.to.String(), func(t *testing.T) {
				fw := &firewall{leases: homeLeases(), aliases: homeAliases()}
				fw.aliases = append(fw.aliases, &pfsenseapipb.Alias{Name: "PHONES_TOO", Type: "host", Addresses: []string{"198.18.1.21"}})
				a := fw.alias(tc.alias)
				a.Addresses = append(a.Addresses, member)
				_, c := startConfigured(t, fw, blockingConfig())

				_, err := c.SetInternetPath(t.Context(), &networkpb.SetInternetPathRequest{Mac: "02:5e:00:00:10:02", Path: tc.to})

				if status.Code(err) != codes.FailedPrecondition || !strings.Contains(err.Error(), member) || !strings.Contains(err.Error(), tc.stays) {
					t.Errorf("SetInternetPath = %v, want FailedPrecondition: %s, naming %s", err, tc.stays, member)
				}
				if w := fw.written(); len(w) != 0 {
					t.Errorf("writes = %v, want none", w)
				}
			})
		}
	}
}

func TestNoInternetForADeviceDirectThroughAnotherMemberMovesOnlyItsOwnAddress(t *testing.T) {
	fw := &firewall{leases: homeLeases(), aliases: homeAliases()}
	fw.aliases[0].Addresses = append(fw.aliases[0].Addresses, "198.18.1.16/28")
	_, c := startConfigured(t, fw, blockingConfig())

	// The block alias wins: the network keeping the laptop in VPN_BYPASS
	// doesn't keep it from No Internet.
	d := setPath(t, c, "02:5e:00:00:10:02", noInternet)

	want := []proto.Message{&pfsenseapipb.MoveAliasAddressRequest{Address: "198.18.1.21", From: []string{"VPN_BYPASS"}, To: "KILL_SWITCH", Detail: "Work laptop"}}
	if diff := cmp.Diff(want, fw.written(), protocmp.Transform()); diff != "" {
		t.Errorf("writes (-want +got):\n%s", diff)
	}
	if d.GetInternetPath() != noInternet {
		t.Errorf("Device = %v, want No Internet", d)
	}
}

func TestSetInternetPathRefusals(t *testing.T) {
	for name, tc := range map[string]struct {
		cfg  *networkpb.NetworkConfig
		mac  string
		path networkpb.InternetPath
		code codes.Code
		says string
	}{
		"a MAC no Device has":           {homeConfig(), "02:5e:00:00:99:99", direct, codes.NotFound, "02:5e:00:00:99:99"},
		"No Internet without its alias": {homeConfig(), "02:5e:00:00:10:01", noInternet, codes.FailedPrecondition, "No Internet alias"},
		"no path":                       {homeConfig(), "02:5e:00:00:10:01", networkpb.InternetPath_INTERNET_PATH_UNSPECIFIED, codes.InvalidArgument, "Tunnelled, Direct or No Internet"},
	} {
		t.Run(name, func(t *testing.T) {
			fw := &firewall{leases: homeLeases()}
			_, c := startConfigured(t, fw, tc.cfg)

			_, err := c.SetInternetPath(t.Context(), &networkpb.SetInternetPathRequest{Mac: tc.mac, Path: tc.path})

			if status.Code(err) != tc.code || !strings.Contains(err.Error(), tc.says) {
				t.Errorf("SetInternetPath = %v, want %v saying %q", err, tc.code, tc.says)
			}
			if w := fw.written(); len(w) != 0 {
				t.Errorf("writes = %v, want none", w)
			}
		})
	}
}

func TestSetInternetPathFailsNamingTheBypassAliasWhenItIsGone(t *testing.T) {
	fw := &firewall{leases: homeLeases()}
	_, c := start(t, fw)
	devices(t, c)
	fw.mu.Lock()
	fw.aliases = homeAliases()[1:] // VPN_BYPASS is gone
	fw.mu.Unlock()

	_, err := c.SetInternetPath(t.Context(), &networkpb.SetInternetPathRequest{Mac: "02:5E:00:00:10:01", Path: direct})

	if status.Code(err) == codes.OK || !strings.Contains(err.Error(), "VPN_BYPASS") {
		t.Errorf("SetInternetPath = %v, want an error naming the bypass alias", err)
	}
}
