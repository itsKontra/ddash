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
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/testing/protocmp"

	"github.com/itsKontra/ddash/components/network/networkpb"
	"github.com/itsKontra/ddash/components/pfsense-api/pfsenseapipb"
)

// AddAliasAddress appends the address to the alias unless it holds it, as
// pfsense-api does on the firewall.
func (f *firewall) AddAliasAddress(_ context.Context, req *pfsenseapipb.AddAliasAddressRequest, _ ...grpc.CallOption) (*pfsenseapipb.AddAliasAddressResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.writes = append(f.writes, req)
	if f.err != nil {
		return nil, f.err
	}
	a := f.alias(req.GetAlias())
	if a == nil {
		return nil, status.Errorf(codes.NotFound, "pfSense has no alias %q", req.GetAlias())
	}
	if !slices.Contains(a.Addresses, req.GetAddress()) {
		a.Addresses = append(a.Addresses, req.GetAddress())
		a.Details = append(a.Details, req.GetDetail())
	}
	return &pfsenseapipb.AddAliasAddressResponse{Alias: proto.CloneOf(a)}, nil
}

// RemoveAliasAddress removes the address from the alias.
func (f *firewall) RemoveAliasAddress(_ context.Context, req *pfsenseapipb.RemoveAliasAddressRequest, _ ...grpc.CallOption) (*pfsenseapipb.RemoveAliasAddressResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.writes = append(f.writes, req)
	if f.err != nil {
		return nil, f.err
	}
	a := f.alias(req.GetAlias())
	if a == nil {
		return nil, status.Errorf(codes.NotFound, "pfSense has no alias %q", req.GetAlias())
	}
	if i := slices.Index(a.Addresses, req.GetAddress()); i >= 0 {
		a.Addresses = slices.Delete(a.Addresses, i, i+1)
		if i < len(a.Details) {
			a.Details = slices.Delete(a.Details, i, i+1)
		}
	}
	return &pfsenseapipb.RemoveAliasAddressResponse{Alias: proto.CloneOf(a)}, nil
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

func setDirect(t *testing.T, c networkpb.NetworkServiceClient, mac string, direct bool) *networkpb.Device {
	t.Helper()
	got, err := c.SetDirect(t.Context(), &networkpb.SetDirectRequest{Mac: mac, Direct: direct})
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

func TestDirectOnAddsTheAdoptedDevicesAddressToTheBypassAliasUnderItsName(t *testing.T) {
	fw := &firewall{leases: homeLeases()}
	_, c := start(t, fw)

	d := setDirect(t, c, "02:5e:00:00:10:01", true)

	want := []proto.Message{&pfsenseapipb.AddAliasAddressRequest{Alias: "VPN_BYPASS", Address: "198.18.1.20", Detail: "Living room TV"}}
	if diff := cmp.Diff(want, fw.written(), protocmp.Transform()); diff != "" {
		t.Errorf("writes (-want +got):\n%s", diff)
	}
	if !d.GetDirect() {
		t.Errorf("Device after Direct on = %v, want Direct", d)
	}
	// ListDevices shows it at once, not after the SLOW cache expires.
	if !listed(t, c, "02:5e:00:00:10:01").GetDirect() {
		t.Error("ListDevices after Direct on has the TV Tunnelled")
	}
}

func TestDirectOffRemovesOnlyTheDevicesAddressFromTheBypassAlias(t *testing.T) {
	fw := &firewall{leases: homeLeases()}
	_, c := start(t, fw)

	d := setDirect(t, c, "02:5e:00:00:10:02", false)

	want := []proto.Message{&pfsenseapipb.RemoveAliasAddressRequest{Alias: "VPN_BYPASS", Address: "198.18.1.21"}}
	if diff := cmp.Diff(want, fw.written(), protocmp.Transform()); diff != "" {
		t.Errorf("writes (-want +got):\n%s", diff)
	}
	if d.GetDirect() {
		t.Errorf("Device after Direct off = %v, want Tunnelled", d)
	}
	// The other Device and the server, which is no Device, stay.
	if got := fw.members("VPN_BYPASS"); !slices.Equal(got, []string{"198.18.1.150", "198.18.2.10"}) {
		t.Errorf("VPN_BYPASS = %v, want the phone and the server left", got)
	}
	if listed(t, c, "02:5e:00:00:10:02").GetDirect() {
		t.Error("ListDevices after Direct off has the laptop Direct")
	}
}

func TestATogglesStateTheDeviceIsAlreadyInWritesNothing(t *testing.T) {
	for name, tc := range map[string]struct {
		mac    string
		direct bool
		// extra is a bypass alias member that is no Device's address.
		extra string
	}{
		"Direct on a Direct Device":                   {"02:5e:00:00:10:02", true, ""},
		"Tunnelled on a Tunnelled Device":             {"02:5e:00:00:10:03", false, ""},
		"Direct on a Device Direct through a network": {"02:5e:00:00:10:01", true, "198.18.1.20/32"},
	} {
		t.Run(name, func(t *testing.T) {
			fw := &firewall{leases: homeLeases(), aliases: homeAliases()}
			if tc.extra != "" {
				fw.aliases[0].Addresses = append(fw.aliases[0].Addresses, tc.extra)
				fw.aliases[0].Details = append(fw.aliases[0].Details, "Other")
			}
			_, c := start(t, fw)

			if d := setDirect(t, c, tc.mac, tc.direct); d.GetDirect() != tc.direct {
				t.Errorf("Device = %v, want direct=%v", d, tc.direct)
			}
			if w := fw.written(); len(w) != 0 {
				t.Errorf("writes = %v, want none", w)
			}
		})
	}
}

func TestATogglesOnANewDeviceAdoptsItFirstAtTheLowestFreeAddress(t *testing.T) {
	fw := &firewall{leases: homeLeases()}
	_, c := start(t, fw)

	// The tablet's expired lease sent no hostname, so its MAC names it.
	d := setDirect(t, c, "02:5e:00:00:20:02", true)

	want := []proto.Message{
		&pfsenseapipb.CreateStaticMappingRequest{Mapping: &pfsenseapipb.StaticMapping{Interface: "lan", Mac: "02:5e:00:00:20:02", Ip: "198.18.1.23", Description: "02:5e:00:00:20:02"}},
		&pfsenseapipb.AddAliasAddressRequest{Alias: "VPN_BYPASS", Address: "198.18.1.23", Detail: "02:5e:00:00:20:02"},
	}
	if diff := cmp.Diff(want, fw.written(), protocmp.Transform()); diff != "" {
		t.Errorf("writes (-want +got):\n%s", diff)
	}
	if !d.GetAdopted() || !d.GetDirect() || d.GetIp() != "198.18.1.23" {
		t.Errorf("Device = %v, want Adopted at .23 and Direct", d)
	}
}

func TestTunnelledOnANewDeviceAdoptsItUnderItsHostname(t *testing.T) {
	fw := &firewall{leases: homeLeases()}
	_, c := start(t, fw)

	// The phone is Direct on its lease's address; its reserved one isn't
	// in the alias, so Adopting it is all it takes.
	d := setDirect(t, c, "02:5e:00:00:20:01", false)

	want := []proto.Message{
		&pfsenseapipb.CreateStaticMappingRequest{Mapping: &pfsenseapipb.StaticMapping{Interface: "lan", Mac: "02:5e:00:00:20:01", Ip: "198.18.1.23", Hostname: "galaxy-s24", Description: "galaxy-s24"}},
	}
	if diff := cmp.Diff(want, fw.written(), protocmp.Transform()); diff != "" {
		t.Errorf("writes (-want +got):\n%s", diff)
	}
	if !d.GetAdopted() || d.GetDirect() {
		t.Errorf("Device = %v, want Adopted and Tunnelled", d)
	}
	// The lease's address is no Device's any more: it stays in the alias.
	if got := fw.members("VPN_BYPASS"); !slices.Contains(got, "198.18.1.150") {
		t.Errorf("VPN_BYPASS = %v, want .150 untouched", got)
	}
}

func TestTunnelledIsRefusedForADeviceDirectThroughAMemberThatIsNoDevice(t *testing.T) {
	for name, member := range map[string]string{"network": "198.18.1.16/28", "range": "198.18.1.18-198.18.1.25", "nested alias": "KILL_SWITCH"} {
		t.Run(name, func(t *testing.T) {
			fw := &firewall{leases: homeLeases(), aliases: homeAliases()}
			fw.aliases[0].Addresses = append(fw.aliases[0].Addresses, "198.18.1.20", member)
			fw.aliases[0].Details = append(fw.aliases[0].Details, "Living room TV", "Other")
			_, c := start(t, fw)

			_, err := c.SetDirect(t.Context(), &networkpb.SetDirectRequest{Mac: "02:5e:00:00:10:01", Direct: false})

			if status.Code(err) != codes.FailedPrecondition || !strings.Contains(err.Error(), member) {
				t.Errorf("SetDirect = %v, want FailedPrecondition naming %s", err, member)
			}
			if w := fw.written(); len(w) != 0 {
				t.Errorf("writes = %v, want none", w)
			}
		})
	}
}

func TestSetDirectRefusesAMACNoDeviceHas(t *testing.T) {
	fw := &firewall{leases: homeLeases()}
	_, c := start(t, fw)

	_, err := c.SetDirect(t.Context(), &networkpb.SetDirectRequest{Mac: "02:5e:00:00:99:99", Direct: true})

	if status.Code(err) != codes.NotFound || !strings.Contains(err.Error(), "02:5e:00:00:99:99") {
		t.Errorf("SetDirect = %v, want NotFound naming the MAC", err)
	}
}

func TestSetDirectFailsNamingTheBypassAliasWhenItIsGone(t *testing.T) {
	fw := &firewall{leases: homeLeases()}
	_, c := start(t, fw)
	devices(t, c)
	fw.mu.Lock()
	fw.aliases = homeAliases()[1:] // VPN_BYPASS is gone
	fw.mu.Unlock()

	_, err := c.SetDirect(t.Context(), &networkpb.SetDirectRequest{Mac: "02:5E:00:00:10:01", Direct: true})

	if status.Code(err) == codes.OK || !strings.Contains(err.Error(), "VPN_BYPASS") {
		t.Errorf("SetDirect = %v, want an error naming the bypass alias", err)
	}
}
