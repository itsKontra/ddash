package pfsenseapi_test

import (
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/testing/protocmp"

	"github.com/itsKontra/ddash/components/pfsense-api/pfsenseapipb"
	"github.com/itsKontra/ddash/components/pfsense-api/pfsensefake"
)

const (
	readAliases  = "GET /api/v2/firewall/aliases"
	patchAlias   = "PATCH /api/v2/firewall/alias"
	firewallPost = "POST /api/v2/firewall/apply"
)

// aliasWrites is the firewall's log of alias reads, writes and applies.
func aliasWrites(fw *fakeFirewall) []string {
	var out []string
	for _, e := range fw.Log() {
		if e == readAliases || e == patchAlias || e == firewallPost || e == pfsensefake.AppliedFirewall {
			out = append(out, e)
		}
	}
	return out
}

// bypass is VPN_BYPASS in the baseline fixtures: two Devices and a server.
func bypass() pfsensefake.Alias {
	return pfsensefake.Alias{
		Name:      "VPN_BYPASS",
		Addresses: []string{"198.18.1.21", "198.18.1.150", "198.18.2.10"},
		Details:   []string{"Work laptop", "Phone", "Media server"},
	}
}

func alias(fw *fakeFirewall, name string) pfsensefake.Alias {
	i := slices.IndexFunc(fw.Aliases(), func(a pfsensefake.Alias) bool { return a.Name == name })
	if i < 0 {
		return pfsensefake.Alias{}
	}
	return fw.Aliases()[i]
}

func TestAddingAnAddressAppendsItWithItsDescriptionAndApplies(t *testing.T) {
	fw := firewall(t, "baseline")
	_, c := start(t, fw.URL)

	got, err := c.AddAliasAddress(t.Context(), &pfsenseapipb.AddAliasAddressRequest{Alias: "VPN_BYPASS", Address: "198.18.1.23", Detail: "Tablet"})
	if err != nil {
		t.Fatal(err)
	}

	want := bypass()
	want.Addresses = append(want.Addresses, "198.18.1.23")
	want.Details = append(want.Details, "Tablet")
	if diff := cmp.Diff(want, alias(fw, "VPN_BYPASS")); diff != "" {
		t.Errorf("VPN_BYPASS (-want +got):\n%s", diff)
	}
	wantAlias := &pfsenseapipb.Alias{Name: "VPN_BYPASS", Type: "host", Description: "Leaves through the WAN, not the VPN", Addresses: want.Addresses, Details: want.Details}
	if diff := cmp.Diff(wantAlias, got.GetAlias(), protocmp.Transform()); diff != "" {
		t.Errorf("answer (-want +got):\n%s", diff)
	}
	if fw.FirewallPending() {
		t.Error("alias changes still pending, want applied")
	}
	// The id is looked up right before the write, which applies itself.
	if diff := cmp.Diff([]string{readAliases, patchAlias, pfsensefake.AppliedFirewall}, aliasWrites(fw)); diff != "" {
		t.Errorf("calls (-want +got):\n%s", diff)
	}
}

func TestAddingAnAddressTheAliasHoldsWritesNothing(t *testing.T) {
	fw := firewall(t, "baseline")
	_, c := start(t, fw.URL)

	got, err := c.AddAliasAddress(t.Context(), &pfsenseapipb.AddAliasAddressRequest{Alias: "VPN_BYPASS", Address: "198.18.1.21", Detail: "Laptop"})
	if err != nil {
		t.Fatal(err)
	}

	if diff := cmp.Diff(bypass(), alias(fw, "VPN_BYPASS")); diff != "" {
		t.Errorf("VPN_BYPASS (-want +got):\n%s", diff)
	}
	if got.GetAlias().GetDetails()[0] != "Work laptop" {
		t.Errorf("answer = %v, want the alias unchanged", got.GetAlias())
	}
	if diff := cmp.Diff([]string{readAliases}, aliasWrites(fw)); diff != "" {
		t.Errorf("calls (-want +got):\n%s", diff)
	}
}

func TestRemovingAnAddressTakesItsDescriptionAlongAndKeepsTheOthers(t *testing.T) {
	fw := firewall(t, "baseline")
	_, c := start(t, fw.URL)

	got, err := c.RemoveAliasAddress(t.Context(), &pfsenseapipb.RemoveAliasAddressRequest{Alias: "VPN_BYPASS", Address: "198.18.1.150"})
	if err != nil {
		t.Fatal(err)
	}

	want := pfsensefake.Alias{Name: "VPN_BYPASS", Addresses: []string{"198.18.1.21", "198.18.2.10"}, Details: []string{"Work laptop", "Media server"}}
	if diff := cmp.Diff(want, alias(fw, "VPN_BYPASS")); diff != "" {
		t.Errorf("VPN_BYPASS (-want +got):\n%s", diff)
	}
	if !slices.Equal(got.GetAlias().GetAddresses(), want.Addresses) {
		t.Errorf("answer = %v, want the alias after the write", got.GetAlias())
	}
	if fw.FirewallPending() {
		t.Error("alias changes still pending, want applied")
	}
	if diff := cmp.Diff([]string{readAliases, patchAlias, pfsensefake.AppliedFirewall}, aliasWrites(fw)); diff != "" {
		t.Errorf("calls (-want +got):\n%s", diff)
	}
}

// pfSense's own "remove" can't do these: it skips a list equal to the
// stored one, and drops every description equal to the removed one's.
func TestRemovingTheOnlyMemberOrOneSharingItsDescriptionIsExact(t *testing.T) {
	fw := firewall(t, "baseline")
	_, c := start(t, fw.URL)
	for _, a := range []string{"198.18.1.31", "198.18.1.32"} {
		if _, err := c.AddAliasAddress(t.Context(), &pfsenseapipb.AddAliasAddressRequest{Alias: "SERVERS", Address: a + "/32", Detail: "Server rack"}); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := c.RemoveAliasAddress(t.Context(), &pfsenseapipb.RemoveAliasAddressRequest{Alias: "SERVERS", Address: "198.18.1.31/32"}); err != nil {
		t.Fatal(err)
	}
	want := pfsensefake.Alias{Name: "SERVERS", Addresses: []string{"198.18.2.0/28", "198.18.1.32/32"}, Details: []string{"Server rack", "Server rack"}}
	if diff := cmp.Diff(want, alias(fw, "SERVERS")); diff != "" {
		t.Errorf("SERVERS after one removal (-want +got):\n%s", diff)
	}

	for _, a := range []string{"198.18.2.0/28", "198.18.1.32/32"} {
		if _, err := c.RemoveAliasAddress(t.Context(), &pfsenseapipb.RemoveAliasAddressRequest{Alias: "SERVERS", Address: a}); err != nil {
			t.Fatal(err)
		}
	}
	if got := alias(fw, "SERVERS"); len(got.Addresses) != 0 || len(got.Details) != 0 {
		t.Errorf("SERVERS after removing every member = %v, want empty", got)
	}
}

func TestRemovingAnAddressTheAliasLacksWritesNothing(t *testing.T) {
	fw := firewall(t, "baseline")
	_, c := start(t, fw.URL)

	if _, err := c.RemoveAliasAddress(t.Context(), &pfsenseapipb.RemoveAliasAddressRequest{Alias: "VPN_BYPASS", Address: "198.18.1.23"}); err != nil {
		t.Fatal(err)
	}

	if diff := cmp.Diff(bypass(), alias(fw, "VPN_BYPASS")); diff != "" {
		t.Errorf("VPN_BYPASS (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]string{readAliases}, aliasWrites(fw)); diff != "" {
		t.Errorf("calls (-want +got):\n%s", diff)
	}
}

func TestAnAliasWriteShowsInTheNextReadOfTheAliases(t *testing.T) {
	fw := firewall(t, "baseline")
	_, c := start(t, fw.URL)
	if _, err := c.ListAliases(t.Context(), &pfsenseapipb.ListAliasesRequest{}); err != nil {
		t.Fatal(err)
	}

	if _, err := c.AddAliasAddress(t.Context(), &pfsenseapipb.AddAliasAddressRequest{Alias: "VPN_BYPASS", Address: "198.18.1.23", Detail: "Tablet"}); err != nil {
		t.Fatal(err)
	}
	got, err := c.ListAliases(t.Context(), &pfsenseapipb.ListAliasesRequest{})
	if err != nil {
		t.Fatal(err)
	}

	if a := got.GetAliases()[0]; !slices.Contains(a.GetAddresses(), "198.18.1.23") {
		t.Errorf("VPN_BYPASS after the write = %v, want the new address", a)
	}
}

func TestAliasWritesRefuseAnAliasTheFirewallLacks(t *testing.T) {
	fw := firewall(t, "baseline")
	_, c := start(t, fw.URL)

	_, err := c.AddAliasAddress(t.Context(), &pfsenseapipb.AddAliasAddressRequest{Alias: "NO_SUCH", Address: "198.18.1.23"})
	if status.Code(err) != codes.NotFound || !strings.Contains(err.Error(), "NO_SUCH") {
		t.Errorf("Add = %v, want NotFound naming the alias", err)
	}
	_, err = c.RemoveAliasAddress(t.Context(), &pfsenseapipb.RemoveAliasAddressRequest{Alias: "NO_SUCH", Address: "198.18.1.23"})
	if status.Code(err) != codes.NotFound || !strings.Contains(err.Error(), "NO_SUCH") {
		t.Errorf("Remove = %v, want NotFound naming the alias", err)
	}
}

func TestAddingAnEmptyAddressIsRefusedWithoutWriting(t *testing.T) {
	fw := firewall(t, "baseline")
	_, c := start(t, fw.URL)

	_, err := c.AddAliasAddress(t.Context(), &pfsenseapipb.AddAliasAddressRequest{Alias: "VPN_BYPASS", Address: "  "})

	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("Add = %v, want InvalidArgument", err)
	}
	if got := aliasWrites(fw); len(got) != 0 {
		t.Errorf("calls = %v, want none", got)
	}
}

func TestAnAliasWritePfSenseRefusesFailsWithWhatPfSenseSaid(t *testing.T) {
	fw := firewall(t, "baseline")
	_, c := start(t, fw.URL)

	_, err := c.AddAliasAddress(t.Context(), &pfsenseapipb.AddAliasAddressRequest{Alias: "VPN_BYPASS", Address: "not an address!"})

	if status.Code(err) != codes.FailedPrecondition || !strings.Contains(err.Error(), "not a valid IP") {
		t.Errorf("Add = %v, want FailedPrecondition with pfSense's message", err)
	}
	if diff := cmp.Diff(bypass(), alias(fw, "VPN_BYPASS")); diff != "" {
		t.Errorf("VPN_BYPASS changed (-want +got):\n%s", diff)
	}
}

// One queue for every pfSense write: alias writes and static mapping
// writes never overlap.
func TestAliasAndStaticMappingWritesShareOneQueue(t *testing.T) {
	fw := firewallWith(t, "baseline", pfsensefake.Options{WriteDelay: 30 * time.Millisecond})
	_, c := start(t, fw.URL)

	var wg sync.WaitGroup
	errs := make(chan error, 12)
	for i := range 4 {
		wg.Go(func() {
			_, err := c.AddAliasAddress(t.Context(), &pfsenseapipb.AddAliasAddressRequest{Alias: "VPN_BYPASS", Address: fmt.Sprintf("198.18.1.%d", 40+i), Detail: fmt.Sprintf("Device %d", i)})
			errs <- err
		})
		wg.Go(func() {
			_, err := c.RemoveAliasAddress(t.Context(), &pfsenseapipb.RemoveAliasAddressRequest{Alias: "KILL_SWITCH", Address: []string{"198.18.1.20", "198.18.1.22", "198.18.1.20", "198.18.1.22"}[i]})
			errs <- err
		})
		wg.Go(func() {
			m := &pfsenseapipb.StaticMapping{Interface: "lan", Mac: fmt.Sprintf("02:5e:00:00:30:%02x", i), Ip: fmt.Sprintf("198.18.1.%d", 30+i), Description: fmt.Sprintf("Device %d", i)}
			_, err := c.CreateStaticMapping(t.Context(), &pfsenseapipb.CreateStaticMappingRequest{Mapping: m})
			errs <- err
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Error(err)
		}
	}

	if got := fw.MaxConcurrentWrites(); got != 1 {
		t.Errorf("%d writes in flight at once, want 1", got)
	}
	// Each alias write's lookup sits right before it: nothing interleaves.
	log := fw.Log()
	for i, e := range log {
		if e == patchAlias && log[i-1] != readAliases {
			t.Errorf("before alias write %d: %s, want the lookup", i, log[i-1])
		}
	}
	if got := alias(fw, "VPN_BYPASS").Addresses; len(got) != 7 {
		t.Errorf("VPN_BYPASS = %v, want 7 members", got)
	}
	if got := alias(fw, "KILL_SWITCH"); len(got.Addresses) != 0 || len(got.Details) != 0 {
		t.Errorf("KILL_SWITCH = %v, want empty", got)
	}
	if fw.FirewallPending() || fw.Pending() {
		t.Error("changes still pending, want applied")
	}
}
