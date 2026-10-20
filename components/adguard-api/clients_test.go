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
		{Name: "Living room TV", Ids: []string{"198.18.1.20"}, FilteringEnabled: true},
		{Name: "Servers", Ids: []string{"198.18.2.0/28"}, UseGlobalSettings: true},
	}}
	if diff := cmp.Diff(want, got, protocmp.Transform()); diff != "" {
		t.Errorf("ListClients (-want +got):\n%s", diff)
	}
}

func TestTurningAdBlockingOffUpdatesTheClientOfTheAddressAndKeepsItsOtherSettings(t *testing.T) {
	fake := adguard(t, "baseline")
	_, c := start(t, fake.URL)
	before := fake.PersistentClient("Living room TV")

	got := setFiltering(t, c, "198.18.1.20", "TV", false)

	want := &adguardapipb.SetClientFilteringResponse{Client: &adguardapipb.Client{Name: "Living room TV", Ids: []string{"198.18.1.20"}}}
	if diff := cmp.Diff(want, got, protocmp.Transform()); diff != "" {
		t.Errorf("SetClientFiltering (-want +got):\n%s", diff)
	}
	// AdGuard's update replaces the whole client: everything but the two
	// flags must have been sent back as it was.
	before["use_global_settings"], before["filtering_enabled"] = false, false
	if diff := cmp.Diff(before, fake.PersistentClient("Living room TV")); diff != "" {
		t.Errorf("the TV's client (-want +got):\n%s", diff)
	}
	if n := len(fake.Clients()); n != 2 {
		t.Errorf("%d clients, want still 2", n)
	}
	if diff := cmp.Diff([]string{readClients, update}, clientCalls(fake)); diff != "" {
		t.Errorf("calls (-want +got):\n%s", diff)
	}
}

func TestTurningAdBlockingOffForAnAddressWithoutAClientCreatesOneNamedAfterTheDevice(t *testing.T) {
	fake := adguard(t, "baseline")
	_, c := start(t, fake.URL)

	got := setFiltering(t, c, "198.18.1.150", "Anna's phone", false)

	want := &adguardapipb.SetClientFilteringResponse{Created: true, Client: &adguardapipb.Client{Name: "Anna's phone", Ids: []string{"198.18.1.150"}}}
	if diff := cmp.Diff(want, got, protocmp.Transform()); diff != "" {
		t.Errorf("SetClientFiltering (-want +got):\n%s", diff)
	}
	phone := fake.PersistentClient("Anna's phone")
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
	setFiltering(t, c, "198.18.1.150", "Anna's phone", false)

	got := setFiltering(t, c, "198.18.1.150", "Phone renamed meanwhile", true)

	if got.GetCreated() || got.GetClient().GetName() != "Anna's phone" || !got.GetClient().GetFilteringEnabled() || got.GetClient().GetUseGlobalSettings() {
		t.Errorf("SetClientFiltering = %v, want Anna's phone's own settings filtering again", got)
	}
	if n := len(fake.Clients()); n != 3 {
		t.Errorf("%d clients, want 3: one created, once", n)
	}
	list, err := c.ListClients(t.Context(), &adguardapipb.ListClientsRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if last := list.GetClients()[2]; last.GetName() != "Anna's phone" || !last.GetFilteringEnabled() {
		t.Errorf("ListClients after the writes = %v, want the phone filtering", list)
	}
}

func TestTurningAdBlockingOnForAnAddressWithoutAClientWritesNothing(t *testing.T) {
	fake := adguard(t, "baseline")
	_, c := start(t, fake.URL)

	got := setFiltering(t, c, "198.18.1.150", "Anna's phone", true)

	if got.GetClient() != nil || got.GetCreated() {
		t.Errorf("SetClientFiltering = %v, want nothing written", got)
	}
	if diff := cmp.Diff([]string{readClients}, clientCalls(fake)); diff != "" {
		t.Errorf("calls (-want +got):\n%s", diff)
	}
}

func TestAClientForANetworkIsNotTheClientOfOneAddressInIt(t *testing.T) {
	fake := adguard(t, "baseline")
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

	got := setFiltering(t, c, "198.18.1.150", "Living room TV", false)

	if got.GetClient().GetName() != "Living room TV (198.18.1.150)" {
		t.Errorf("created = %v, want the name with the address", got.GetClient())
	}
}

func TestAClientCreatedWithoutANameIsNamedAfterItsAddress(t *testing.T) {
	_, c := start(t, adguard(t, "baseline").URL)

	if got := setFiltering(t, c, "198.18.1.151", " ", false); got.GetClient().GetName() != "198.18.1.151" {
		t.Errorf("created = %v, want it named 198.18.1.151", got.GetClient())
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
