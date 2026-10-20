package adguardapi_test

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/testing/protocmp"

	"github.com/itsKontra/ddash/components/adguard-api/adguardapipb"
)

const (
	readClients = "GET /control/clients"
	addClient   = "POST /control/clients/add"
	update      = "POST /control/clients/update"
)

// clientCalls is the fake's log of client reads and writes.
func clientCalls(f *fakeAdGuard) []string {
	var out []string
	for _, e := range f.Log() {
		if e == readClients || e == addClient || e == update {
			out = append(out, e)
		}
	}
	return out
}

func setFiltering(t *testing.T, c adguardapipb.AdguardApiServiceClient, ip, name string, enabled bool) *adguardapipb.SetClientFilteringResponse {
	t.Helper()
	got, err := c.SetClientFiltering(t.Context(), &adguardapipb.SetClientFilteringRequest{Ip: ip, Name: name, Enabled: enabled})
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestListsThePersistentClients(t *testing.T) {
	_, c := start(t, adguard(t, "baseline").URL)

	got, err := c.ListClients(t.Context(), &adguardapipb.ListClientsRequest{})
	if err != nil {
		t.Fatal(err)
	}

	want := &adguardapipb.ListClientsResponse{Clients: []*adguardapipb.Client{
		{Name: "AndroidTV", Ids: []string{"198.18.1.6"}, UseGlobalSettings: true},
		{Name: "Huawei Inv", Ids: []string{"198.18.1.4"}, UseGlobalSettings: true},
		{Name: "Laptop", Ids: []string{"198.18.1.102"}},
		{Name: "member", Ids: []string{"198.18.1.21"}},
		{Name: "operator Handy", Ids: []string{"198.18.1.11"}, UseGlobalSettings: true},
		{Name: "operator PC", Ids: []string{"198.18.1.12"}, UseGlobalSettings: true},
	}}
	if diff := cmp.Diff(want, got, protocmp.Transform()); diff != "" {
		t.Errorf("ListClients (-want +got):\n%s", diff)
	}
}

func TestTurningAdBlockingOffUpdatesTheClientOfTheAddressAndKeepsItsOtherSettings(t *testing.T) {
	fake := adguard(t, "baseline")
	_, c := start(t, fake.URL)
	before := fake.PersistentClient("AndroidTV")

	got := setFiltering(t, c, "198.18.1.6", "TV", false)

	want := &adguardapipb.SetClientFilteringResponse{Client: &adguardapipb.Client{Name: "AndroidTV", Ids: []string{"198.18.1.6"}}}
	if diff := cmp.Diff(want, got, protocmp.Transform()); diff != "" {
		t.Errorf("SetClientFiltering (-want +got):\n%s", diff)
	}
	// AdGuard's update replaces the whole client: everything but the two
	// flags must have been sent back as it was.
	before["use_global_settings"], before["filtering_enabled"] = false, false
	if diff := cmp.Diff(before, fake.PersistentClient("AndroidTV")); diff != "" {
		t.Errorf("the TV's client (-want +got):\n%s", diff)
	}
	if n := len(fake.Clients()); n != 6 {
		t.Errorf("%d clients, want still 6", n)
	}
	if diff := cmp.Diff([]string{readClients, update}, clientCalls(fake)); diff != "" {
		t.Errorf("calls (-want +got):\n%s", diff)
	}
}

func TestTurningAdBlockingOffForAnAddressWithoutAClientCreatesOneNamedAfterTheDevice(t *testing.T) {
	fake := adguard(t, "baseline")
	_, c := start(t, fake.URL)

	got := setFiltering(t, c, "198.18.1.26", "member's phone", false)

	want := &adguardapipb.SetClientFilteringResponse{Created: true, Client: &adguardapipb.Client{Name: "member's phone", Ids: []string{"198.18.1.26"}}}
	if diff := cmp.Diff(want, got, protocmp.Transform()); diff != "" {
		t.Errorf("SetClientFiltering (-want +got):\n%s", diff)
	}
	phone := fake.PersistentClient("member's phone")
	if phone == nil || phone["use_global_settings"] != false || phone["filtering_enabled"] != false || phone["use_global_blocked_services"] != true {
		t.Errorf("the phone's client = %v, want its own settings without filtering, blocked services still global", phone)
	}
	// Found by its address, else created; then read, modified and written.
	if diff := cmp.Diff([]string{readClients, addClient, readClients, update}, clientCalls(fake)); diff != "" {
		t.Errorf("calls (-want +got):\n%s", diff)
	}
}

func TestTheClientCreatedIsFoundAgainAndTurnedBackOn(t *testing.T) {
	fake := adguard(t, "baseline")
	_, c := start(t, fake.URL)
	setFiltering(t, c, "198.18.1.26", "member's phone", false)

	got := setFiltering(t, c, "198.18.1.26", "Phone renamed meanwhile", true)

	if got.GetCreated() || got.GetClient().GetName() != "member's phone" || !got.GetClient().GetFilteringEnabled() || got.GetClient().GetUseGlobalSettings() {
		t.Errorf("SetClientFiltering = %v, want member's phone's own settings filtering again", got)
	}
	if n := len(fake.Clients()); n != 7 {
		t.Errorf("%d clients, want 7: one created, once", n)
	}
	list, err := c.ListClients(t.Context(), &adguardapipb.ListClientsRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if last := list.GetClients()[6]; last.GetName() != "member's phone" || !last.GetFilteringEnabled() {
		t.Errorf("ListClients after the writes = %v, want the phone filtering", list)
	}
}

func TestTurningAdBlockingOnForAnAddressWithoutAClientWritesNothing(t *testing.T) {
	fake := adguard(t, "baseline")
	_, c := start(t, fake.URL)

	got := setFiltering(t, c, "198.18.1.26", "member's phone", true)

	if got.GetClient() != nil || got.GetCreated() {
		t.Errorf("SetClientFiltering = %v, want nothing written", got)
	}
	if diff := cmp.Diff([]string{readClients}, clientCalls(fake)); diff != "" {
		t.Errorf("calls (-want +got):\n%s", diff)
	}
}

func TestAClientForANetworkIsNotTheClientOfOneAddressInIt(t *testing.T) {
	// The homelab has no client for a network; this scenario's only client
	// is one.
	fake := adguard(t, "network-client")
	_, c := start(t, fake.URL)

	got := setFiltering(t, c, "198.18.2.5", "NAS", false)

	if !got.GetCreated() || got.GetClient().GetName() != "NAS" {
		t.Errorf("SetClientFiltering = %v, want a client created for the address", got)
	}
	if servers := fake.PersistentClient("Servers"); servers["use_global_settings"] != true {
		t.Errorf("Servers = %v, want untouched", servers)
	}
}

func TestAClientCreatedUnderANameTakenGetsTheAddressInItsName(t *testing.T) {
	fake := adguard(t, "baseline")
	_, c := start(t, fake.URL)

	got := setFiltering(t, c, "198.18.1.26", "AndroidTV", false)

	if got.GetClient().GetName() != "AndroidTV (198.18.1.26)" {
		t.Errorf("created = %v, want the name with the address", got.GetClient())
	}
}

func TestAClientCreatedWithoutANameIsNamedAfterItsAddress(t *testing.T) {
	_, c := start(t, adguard(t, "baseline").URL)

	if got := setFiltering(t, c, "198.18.1.22", " ", false); got.GetClient().GetName() != "198.18.1.22" {
		t.Errorf("created = %v, want it named 198.18.1.22", got.GetClient())
	}
}

func TestSetClientFilteringRefusesAnIPThatIsNoAddress(t *testing.T) {
	fake := adguard(t, "baseline")
	_, c := start(t, fake.URL)

	_, err := c.SetClientFiltering(t.Context(), &adguardapipb.SetClientFilteringRequest{Ip: "phone", Enabled: false})
	if status.Code(err) != codes.InvalidArgument || !strings.Contains(err.Error(), "phone") {
		t.Errorf("SetClientFiltering(phone) = %v, want InvalidArgument", err)
	}
	if calls := clientCalls(fake); len(calls) != 0 {
		t.Errorf("calls = %v, want none", calls)
	}
}
