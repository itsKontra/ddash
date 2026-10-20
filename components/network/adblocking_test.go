package network_test

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/google/go-cmp/cmp"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/testing/protocmp"

	"github.com/itsKontra/ddash/components/adguard-api/adguardapipb"
	"github.com/itsKontra/ddash/components/network/networkpb"
	"github.com/itsKontra/ddash/components/pfsense-api/pfsenseapipb"
)

// adguard fakes adguard-api's generated client: its persistent clients,
// changed by SetClientFiltering as adguard-api changes AdGuard's.
type adguard struct {
	adguardapipb.AdguardApiServiceClient

	mu      sync.Mutex
	clients []*adguardapipb.Client
	err     error
	// calls is every SetClientFiltering request, in order.
	calls []*adguardapipb.SetClientFilteringRequest
}

func (a *adguard) ListClients(context.Context, *adguardapipb.ListClientsRequest, ...grpc.CallOption) (*adguardapipb.ListClientsResponse, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.err != nil {
		return nil, a.err
	}
	return proto.CloneOf(&adguardapipb.ListClientsResponse{Clients: a.clients}), nil
}

func (a *adguard) SetClientFiltering(_ context.Context, req *adguardapipb.SetClientFilteringRequest, _ ...grpc.CallOption) (*adguardapipb.SetClientFilteringResponse, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.calls = append(a.calls, req)
	if a.err != nil {
		return nil, a.err
	}
	for _, c := range a.clients {
		for _, id := range c.GetIds() {
			if id == req.GetIp() {
				c.UseGlobalSettings, c.FilteringEnabled = false, req.GetEnabled()
				return &adguardapipb.SetClientFilteringResponse{Client: proto.CloneOf(c)}, nil
			}
		}
	}
	if req.GetEnabled() {
		return &adguardapipb.SetClientFilteringResponse{}, nil
	}
	c := &adguardapipb.Client{Name: req.GetName(), Ids: []string{req.GetIp()}}
	a.clients = append(a.clients, c)
	return &adguardapipb.SetClientFilteringResponse{Client: proto.CloneOf(c), Created: true}, nil
}

func (a *adguard) fail(err error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.err = err
}

func (a *adguard) setCalls() []*adguardapipb.SetClientFilteringRequest {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.calls
}

// homeClients: the TV has its own settings without filtering, the laptop
// its own with filtering, and the servers' network follows the global ones.
func homeClients() []*adguardapipb.Client {
	return []*adguardapipb.Client{
		{Name: "Living room TV", Ids: []string{"198.18.1.20"}},
		{Name: "Work laptop", Ids: []string{"198.18.1.21"}, FilteringEnabled: true},
		{Name: "Servers", Ids: []string{"198.18.1.0/24"}, UseGlobalSettings: true},
	}
}

func adBlockingOff(got *networkpb.ListDevicesResponse) map[string]bool {
	out := map[string]bool{}
	for _, d := range got.GetDevices() {
		out[d.GetIp()] = d.GetAdBlockingOff()
	}
	return out
}

func TestDevicesShowAdBlockingOffWhereTheirAddresssClientFiltersNothing(t *testing.T) {
	_, c := startWithAdGuard(t, &firewall{leases: homeLeases()}, &adguard{clients: homeClients()})

	got := devices(t, c)

	// A network's client is not a Device's own.
	want := map[string]bool{"198.18.1.20": true, "198.18.1.21": false, "198.18.1.22": false, "198.18.1.150": false, "198.18.1.151": false}
	if diff := cmp.Diff(want, adBlockingOff(got)); diff != "" {
		t.Errorf("ad-blocking off by IP (-want +got):\n%s", diff)
	}
	if got.GetAdguardError() != "" {
		t.Errorf("adguard error = %q, want none", got.GetAdguardError())
	}
}

func TestDevicesStillListWhileAdGuardCantBeReadSayingWhy(t *testing.T) {
	ag := &adguard{clients: homeClients(), err: status.Error(codes.Unavailable, "AdGuard Home unreachable: connection refused")}
	_, c := startWithAdGuard(t, &firewall{leases: homeLeases()}, ag)

	got := devices(t, c)

	if len(got.GetDevices()) != 5 || !strings.Contains(got.GetAdguardError(), "AdGuard Home unreachable") {
		t.Errorf("ListDevices = %d Devices, adguard error %q; want all 5 and adguard-api's error", len(got.GetDevices()), got.GetAdguardError())
	}
}

func setAdBlocking(t *testing.T, c networkpb.NetworkServiceClient, mac string, enabled bool) *networkpb.Device {
	t.Helper()
	got, err := c.SetAdBlocking(t.Context(), &networkpb.SetAdBlockingRequest{Mac: mac, Enabled: enabled})
	if err != nil {
		t.Fatal(err)
	}
	return got.GetDevice()
}

func TestTurningAdBlockingOffForAnAdoptedDeviceSetsItsClientByItsReservedAddress(t *testing.T) {
	fw, ag := &firewall{leases: homeLeases()}, &adguard{clients: homeClients()}
	_, c := startWithAdGuard(t, fw, ag)
	devices(t, c) // cached now

	got := setAdBlocking(t, c, "02:5E:00:00:10:03", false)

	want := []*adguardapipb.SetClientFilteringRequest{{Ip: "198.18.1.22", Name: "printer", Enabled: false}}
	if diff := cmp.Diff(want, ag.setCalls(), protocmp.Transform()); diff != "" {
		t.Errorf("SetClientFiltering calls (-want +got):\n%s", diff)
	}
	if !got.GetAdBlockingOff() || got.GetMac() != "02:5e:00:00:10:03" {
		t.Errorf("Device = %v, want the printer with ad-blocking off", got)
	}
	if w := fw.written(); len(w) != 0 {
		t.Errorf("firewall writes = %v, want none for an Adopted Device", w)
	}
	// ListDevices shows it at once, not after the SLOW cache expires.
	if off := adBlockingOff(devices(t, c)); !off["198.18.1.22"] {
		t.Errorf("ListDevices after the toggle: %v, want the printer off", off)
	}
}

func TestTurningAdBlockingBackOnSetsTheClientFilteringAgain(t *testing.T) {
	ag := &adguard{clients: homeClients()}
	_, c := startWithAdGuard(t, &firewall{leases: homeLeases()}, ag)

	got := setAdBlocking(t, c, "02:5e:00:00:10:01", true)

	want := []*adguardapipb.SetClientFilteringRequest{{Ip: "198.18.1.20", Name: "Living room TV", Enabled: true}}
	if diff := cmp.Diff(want, ag.setCalls(), protocmp.Transform()); diff != "" {
		t.Errorf("SetClientFiltering calls (-want +got):\n%s", diff)
	}
	if got.GetAdBlockingOff() {
		t.Errorf("Device = %v, want ad-blocking on", got)
	}
}

func TestTheToggleOnANewDeviceAdoptsItFirstAtTheLowestFreeAddress(t *testing.T) {
	fw, ag := &firewall{leases: homeLeases()}, &adguard{clients: homeClients()}
	_, c := startWithAdGuard(t, fw, ag)

	got := setAdBlocking(t, c, "02:5e:00:00:20:01", false)

	adopt := &pfsenseapipb.CreateStaticMappingRequest{Mapping: &pfsenseapipb.StaticMapping{
		Interface: "lan", Mac: "02:5e:00:00:20:01", Ip: "198.18.1.23", Hostname: "galaxy-s24", Description: "galaxy-s24",
	}}
	if diff := cmp.Diff([]proto.Message{adopt}, fw.written(), protocmp.Transform()); diff != "" {
		t.Errorf("firewall writes (-want +got):\n%s", diff)
	}
	// The AdGuard client goes on the address it is moving to.
	want := []*adguardapipb.SetClientFilteringRequest{{Ip: "198.18.1.23", Name: "galaxy-s24", Enabled: false}}
	if diff := cmp.Diff(want, ag.setCalls(), protocmp.Transform()); diff != "" {
		t.Errorf("SetClientFiltering calls (-want +got):\n%s", diff)
	}
	if !got.GetAdopted() || got.GetIp() != "198.18.1.23" || !got.GetAdBlockingOff() {
		t.Errorf("Device = %v, want Adopted at .23 with ad-blocking off", got)
	}
}

func TestANewDeviceWithoutANameIsAdoptedUnderItsMAC(t *testing.T) {
	fw := &firewall{leases: homeLeases()}
	_, c := startWithAdGuard(t, fw, &adguard{})

	setAdBlocking(t, c, "02:5e:00:00:20:02", false)

	if w := fw.written(); len(w) != 1 || w[0].(*pfsenseapipb.CreateStaticMappingRequest).GetMapping().GetDescription() != "02:5e:00:00:20:02" {
		t.Errorf("firewall writes = %v, want one Adopt named by the MAC", w)
	}
}

func TestTheToggleRefusesAnUnknownDeviceWritingNothing(t *testing.T) {
	fw, ag := &firewall{leases: homeLeases()}, &adguard{clients: homeClients()}
	_, c := startWithAdGuard(t, fw, ag)

	_, err := c.SetAdBlocking(t.Context(), &networkpb.SetAdBlockingRequest{Mac: "02:5e:00:00:99:99", Enabled: false})

	if status.Code(err) != codes.NotFound || !strings.Contains(err.Error(), "02:5e:00:00:99:99") {
		t.Errorf("SetAdBlocking = %v, want NotFound naming the MAC", err)
	}
	if len(fw.written()) != 0 || len(ag.setCalls()) != 0 {
		t.Errorf("writes = %v / %v, want none", fw.written(), ag.setCalls())
	}
}

func TestTheTogglePassesOnAdGuardAPIsError(t *testing.T) {
	ag := &adguard{clients: homeClients()}
	_, c := startWithAdGuard(t, &firewall{leases: homeLeases()}, ag)
	ag.fail(status.Error(codes.Unavailable, "AdGuard Home unreachable: connection refused"))

	_, err := c.SetAdBlocking(t.Context(), &networkpb.SetAdBlockingRequest{Mac: "02:5e:00:00:10:01", Enabled: false})

	if status.Code(err) != codes.Unavailable || !strings.Contains(err.Error(), "AdGuard Home unreachable") {
		t.Errorf("SetAdBlocking = %v, want Unavailable naming adguard-api's error", err)
	}
}
