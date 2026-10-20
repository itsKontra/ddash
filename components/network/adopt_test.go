package network_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/testing/protocmp"

	"github.com/itsKontra/ddash/components/network/networkpb"
	"github.com/itsKontra/ddash/components/pfsense-api/pfsenseapipb"
)

func nextAddress(t *testing.T, c networkpb.NetworkServiceClient) string {
	t.Helper()
	resp, err := c.NextReservedAddress(t.Context(), &networkpb.NextReservedAddressRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if resp.GetReservedRange() != "198.18.1.20-198.18.1.49" {
		t.Errorf("reserved range = %q, want the configured one", resp.GetReservedRange())
	}
	return resp.GetIp()
}

func TestAdoptOffersTheLowestAddressInTheReservedRangeNoMappingHolds(t *testing.T) {
	// The home mappings hold .20, .21 and .22.
	_, c := start(t, &firewall{leases: homeLeases()})

	if got := nextAddress(t, c); got != "198.18.1.23" {
		t.Errorf("next reserved address = %q, want 198.18.1.23", got)
	}
}

func TestAdoptOffersAGapAndSkipsAddressesAnotherServerOrALeaseHolds(t *testing.T) {
	fw := &firewall{leases: append(homeLeases(),
		// A Device that set .20 up by hand still answers DHCP for it.
		&pfsenseapipb.Lease{Ip: "198.18.1.24", Mac: "02:5e:00:00:20:09", ActiveStatus: "active", OnlineStatus: "active/online"},
		// An expired lease doesn't hold its address.
		&pfsenseapipb.Lease{Ip: "198.18.1.25", Mac: "02:5e:00:00:20:0a", ActiveStatus: "expired", OnlineStatus: "idle/offline"},
	)}
	fw.servers = homeServers()
	fw.servers[0].StaticMappings = append(fw.servers[0].StaticMappings[1:], &pfsenseapipb.StaticMapping{Interface: "lan", Mac: "02:5e:00:00:10:09", Ip: "198.18.1.23"})
	fw.servers[1].StaticMappings = []*pfsenseapipb.StaticMapping{{Interface: "opt1", Mac: "02:5e:00:00:10:0a", Ip: "198.18.1.20"}}
	_, c := start(t, fw)

	if got := nextAddress(t, c); got != "198.18.1.25" {
		t.Errorf("next reserved address = %q, want 198.18.1.25 (.20 opt1's, .21-.23 mapped, .24 leased)", got)
	}
}

func TestAFullReservedRangeOffersNoAddress(t *testing.T) {
	fw := &firewall{}
	fw.servers = homeServers()
	for i := 23; i <= 49; i++ {
		fw.servers[0].StaticMappings = append(fw.servers[0].StaticMappings, &pfsenseapipb.StaticMapping{Interface: "lan", Mac: fmt.Sprintf("02:5e:00:00:30:%02d", i), Ip: fmt.Sprintf("198.18.1.%d", i)})
	}
	_, c := start(t, fw)

	if got := nextAddress(t, c); got != "" {
		t.Errorf("next reserved address = %q, want none", got)
	}
	_, err := c.AdoptDevice(t.Context(), &networkpb.AdoptDeviceRequest{Mac: "02:5e:00:00:20:01", Name: "Phone"})
	if status.Code(err) != codes.FailedPrecondition || !strings.Contains(err.Error(), "full") {
		t.Errorf("Adopt without an address = %v, want FailedPrecondition: the range is full", err)
	}
}

func TestAdoptReservesTheAddressUnderTheNameOnTheLAN(t *testing.T) {
	fw := &firewall{leases: homeLeases()}
	_, c := start(t, fw)

	got, err := c.AdoptDevice(t.Context(), &networkpb.AdoptDeviceRequest{Mac: "02:5e:00:00:20:01", Name: "  Anna's phone ", Ip: "198.18.1.30"})
	if err != nil {
		t.Fatal(err)
	}

	// The name is the mapping's description; the hostname is what the
	// Device sent.
	wantWrite := &pfsenseapipb.CreateStaticMappingRequest{Mapping: &pfsenseapipb.StaticMapping{
		Interface: "lan", Mac: "02:5e:00:00:20:01", Ip: "198.18.1.30", Hostname: "galaxy-s24", Description: "Anna's phone",
	}}
	if diff := cmp.Diff([]proto.Message{wantWrite}, fw.written(), protocmp.Transform()); diff != "" {
		t.Errorf("writes (-want +got):\n%s", diff)
	}
	d := got.GetDevice()
	if d.GetName() != "Anna's phone" || !d.GetAdopted() || d.GetIp() != "198.18.1.30" || d.GetLease().GetIp() != "198.18.1.150" {
		t.Errorf("Adopted Device = %v, want named, Adopted at .30, still on its lease until it renews", d)
	}
	// ListDevices shows it at once, not after the SLOW cache expires.
	for _, d := range devices(t, c).GetDevices() {
		if d.GetMac() == "02:5e:00:00:20:01" && (!d.GetAdopted() || d.GetName() != "Anna's phone") {
			t.Errorf("ListDevices after Adopt has %v", d)
		}
	}
}

func TestAdoptWithoutAnAddressTakesTheLowestFree(t *testing.T) {
	fw := &firewall{leases: homeLeases()}
	_, c := start(t, fw)

	got, err := c.AdoptDevice(t.Context(), &networkpb.AdoptDeviceRequest{Mac: "02:5E:00:00:20:02", Name: "Tablet"})
	if err != nil {
		t.Fatal(err)
	}

	// It sent no hostname, so the mapping has none.
	wantWrite := &pfsenseapipb.CreateStaticMappingRequest{Mapping: &pfsenseapipb.StaticMapping{Interface: "lan", Mac: "02:5e:00:00:20:02", Ip: "198.18.1.23", Description: "Tablet"}}
	if diff := cmp.Diff([]proto.Message{wantWrite}, fw.written(), protocmp.Transform()); diff != "" {
		t.Errorf("writes (-want +got):\n%s", diff)
	}
	if got.GetDevice().GetIp() != "198.18.1.23" {
		t.Errorf("Adopted at %q, want 198.18.1.23", got.GetDevice().GetIp())
	}
	if next := nextAddress(t, c); next != "198.18.1.24" {
		t.Errorf("next reserved address after Adopt = %q, want 198.18.1.24", next)
	}
}

func TestAdoptLeavesOutAHostnamePfSenseWouldRefuse(t *testing.T) {
	fw := &firewall{leases: []*pfsenseapipb.Lease{{Ip: "198.18.1.152", Mac: "02:5e:00:00:20:03", Hostname: "Anna's iPad", ActiveStatus: "active", OnlineStatus: "active/online"}}}
	_, c := start(t, fw)

	if _, err := c.AdoptDevice(t.Context(), &networkpb.AdoptDeviceRequest{Mac: "02:5e:00:00:20:03", Name: "iPad"}); err != nil {
		t.Fatal(err)
	}

	if w := fw.written(); len(w) != 1 || w[0].(*pfsenseapipb.CreateStaticMappingRequest).GetMapping().GetHostname() != "" {
		t.Errorf("writes = %v, want one without a hostname", w)
	}
}

func TestAdoptRefusals(t *testing.T) {
	for _, tc := range []struct {
		name string
		req  *networkpb.AdoptDeviceRequest
		code codes.Code
		says string
	}{
		{"no name", &networkpb.AdoptDeviceRequest{Mac: "02:5e:00:00:20:01", Name: "  ", Ip: "198.18.1.30"}, codes.InvalidArgument, "name"},
		{"outside the range", &networkpb.AdoptDeviceRequest{Mac: "02:5e:00:00:20:01", Name: "Phone", Ip: "198.18.1.120"}, codes.InvalidArgument, "198.18.1.20-198.18.1.49"},
		{"no address", &networkpb.AdoptDeviceRequest{Mac: "02:5e:00:00:20:01", Name: "Phone", Ip: "phone"}, codes.InvalidArgument, "phone"},
		{"address reserved", &networkpb.AdoptDeviceRequest{Mac: "02:5e:00:00:20:01", Name: "Phone", Ip: "198.18.1.21"}, codes.AlreadyExists, "Work laptop"},
		{"already Adopted", &networkpb.AdoptDeviceRequest{Mac: "02:5e:00:00:10:01", Name: "TV", Ip: "198.18.1.30"}, codes.FailedPrecondition, "already Adopted"},
		{"no Device", &networkpb.AdoptDeviceRequest{Mac: "02:5e:00:00:99:99", Name: "Ghost", Ip: "198.18.1.30"}, codes.NotFound, "02:5e:00:00:99:99"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fw := &firewall{leases: homeLeases()}
			_, c := start(t, fw)

			_, err := c.AdoptDevice(t.Context(), tc.req)

			if status.Code(err) != tc.code || !strings.Contains(err.Error(), tc.says) {
				t.Errorf("Adopt = %v, want %v saying %q", err, tc.code, tc.says)
			}
			if w := fw.written(); len(w) != 0 {
				t.Errorf("writes = %v, want none", w)
			}
		})
	}
}

func TestAdoptRefusesAnAddressNoDHCPServersSubnetHolds(t *testing.T) {
	fw := &firewall{leases: homeLeases(), interfaces: func() []*pfsenseapipb.Interface { return homelab()[2:] }}
	_, c := start(t, fw)

	_, err := c.AdoptDevice(t.Context(), &networkpb.AdoptDeviceRequest{Mac: "02:5e:00:00:20:01", Name: "Phone", Ip: "198.18.1.30"})

	if status.Code(err) != codes.FailedPrecondition || !strings.Contains(err.Error(), "198.18.1.30") {
		t.Errorf("Adopt = %v, want FailedPrecondition naming the address", err)
	}
}

func TestAdoptPassesOnPfsenseAPIsError(t *testing.T) {
	fw := &firewall{leases: homeLeases()}
	_, c := start(t, fw)
	fw.fail(status.Error(codes.Unavailable, "pfSense unreachable: connection refused"))

	_, err := c.AdoptDevice(t.Context(), &networkpb.AdoptDeviceRequest{Mac: "02:5e:00:00:20:01", Name: "Phone", Ip: "198.18.1.30"})

	if status.Code(err) != codes.Unavailable || !strings.Contains(err.Error(), "pfSense unreachable") {
		t.Errorf("Adopt = %v, want Unavailable naming pfsense-api's error", err)
	}
}

func TestRenameWritesTheNameIntoTheStaticMapping(t *testing.T) {
	fw := &firewall{leases: homeLeases()}
	_, c := start(t, fw)
	devices(t, c) // cached now

	got, err := c.RenameDevice(t.Context(), &networkpb.RenameDeviceRequest{Mac: "02:5E:00:00:10:03", Name: " Office printer"})
	if err != nil {
		t.Fatal(err)
	}

	wantWrite := &pfsenseapipb.RenameStaticMappingRequest{Interface: "lan", Mac: "02:5e:00:00:10:03", Description: "Office printer"}
	if diff := cmp.Diff([]proto.Message{wantWrite}, fw.written(), protocmp.Transform()); diff != "" {
		t.Errorf("writes (-want +got):\n%s", diff)
	}
	if d := got.GetDevice(); d.GetName() != "Office printer" || d.GetIp() != "198.18.1.22" {
		t.Errorf("renamed Device = %v, want Office printer at its address", d)
	}
	for _, d := range devices(t, c).GetDevices() {
		if d.GetMac() == "02:5e:00:00:10:03" && d.GetName() != "Office printer" {
			t.Errorf("ListDevices after the rename names it %q", d.GetName())
		}
	}
}

func TestRenameRefusals(t *testing.T) {
	for _, tc := range []struct {
		name string
		req  *networkpb.RenameDeviceRequest
		code codes.Code
		says string
	}{
		{"no name", &networkpb.RenameDeviceRequest{Mac: "02:5e:00:00:10:01", Name: ""}, codes.InvalidArgument, "name"},
		{"New Device", &networkpb.RenameDeviceRequest{Mac: "02:5e:00:00:20:01", Name: "Phone"}, codes.FailedPrecondition, "Adopt"},
		{"no Device", &networkpb.RenameDeviceRequest{Mac: "02:5e:00:00:99:99", Name: "Ghost"}, codes.NotFound, "02:5e:00:00:99:99"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fw := &firewall{leases: homeLeases()}
			_, c := start(t, fw)

			_, err := c.RenameDevice(t.Context(), tc.req)

			if status.Code(err) != tc.code || !strings.Contains(err.Error(), tc.says) {
				t.Errorf("Rename = %v, want %v saying %q", err, tc.code, tc.says)
			}
			if w := fw.written(); len(w) != 0 {
				t.Errorf("writes = %v, want none", w)
			}
		})
	}
}
