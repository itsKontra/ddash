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

// bypass is VPN_BYPASS in the baseline fixtures: nine hosts.
func bypass() pfsensefake.Alias {
	return pfsensefake.Alias{
		Name:      "VPN_BYPASS",
		Addresses: []string{"198.18.1.208", "198.18.1.1", "198.18.1.6", "198.18.1.10", "198.18.1.102", "198.18.1.16", "198.18.1.12", "198.18.1.122", "198.18.1.54"},
		Details:   []string{"Cloudflare Tunnel", "pfSense", "AndroidTV", "Drucker", "ArbeitsLaptop", "ArchLinux", "operator PC", "devSetup", "member Handy"},
	}
}

// bypassAlias is VPN_BYPASS as ListAliases answers it; it has no description.
func bypassAlias() *pfsenseapipb.Alias {
	b := bypass()
	return &pfsenseapipb.Alias{Name: b.Name, Type: "host", Addresses: b.Addresses, Details: b.Details}
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
	wantAlias := &pfsenseapipb.Alias{Name: "VPN_BYPASS", Type: "host", Addresses: want.Addresses, Details: want.Details}
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

	got, err := c.AddAliasAddress(t.Context(), &pfsenseapipb.AddAliasAddressRequest{Alias: "VPN_BYPASS", Address: "198.18.1.208", Detail: "Laptop"})
	if err != nil {
		t.Fatal(err)
	}

	if diff := cmp.Diff(bypass(), alias(fw, "VPN_BYPASS")); diff != "" {
		t.Errorf("VPN_BYPASS (-want +got):\n%s", diff)
	}
	if got.GetAlias().GetDetails()[0] != "Cloudflare Tunnel" {
		t.Errorf("answer = %v, want the alias unchanged", got.GetAlias())
	}
	if diff := cmp.Diff([]string{readAliases}, aliasWrites(fw)); diff != "" {
		t.Errorf("calls (-want +got):\n%s", diff)
	}
}

func TestRemovingAnAddressTakesItsDescriptionAlongAndKeepsTheOthers(t *testing.T) {
	fw := firewall(t, "baseline")
	_, c := start(t, fw.URL)

	got, err := c.RemoveAliasAddress(t.Context(), &pfsenseapipb.RemoveAliasAddressRequest{Alias: "VPN_BYPASS", Address: "198.18.1.6"})
	if err != nil {
		t.Fatal(err)
	}

	want := bypass()
	want.Addresses = slices.Delete(want.Addresses, 2, 3)
	want.Details = slices.Delete(want.Details, 2, 3)
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
	// KILL_SWITCH holds one host, described by the date pfSense added it.
	const added = "Entry added Sat, 17 Oct 2026 19:15:47 +0200"
	for _, a := range []string{"198.18.1.31", "198.18.1.32"} {
		if _, err := c.AddAliasAddress(t.Context(), &pfsenseapipb.AddAliasAddressRequest{Alias: "KILL_SWITCH", Address: a, Detail: added}); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := c.RemoveAliasAddress(t.Context(), &pfsenseapipb.RemoveAliasAddressRequest{Alias: "KILL_SWITCH", Address: "198.18.1.31"}); err != nil {
		t.Fatal(err)
	}
	want := pfsensefake.Alias{Name: "KILL_SWITCH", Addresses: []string{"198.18.1.212", "198.18.1.32"}, Details: []string{added, added}}
	if diff := cmp.Diff(want, alias(fw, "KILL_SWITCH")); diff != "" {
		t.Errorf("KILL_SWITCH after one removal (-want +got):\n%s", diff)
	}

	for _, a := range []string{"198.18.1.212", "198.18.1.32"} {
		if _, err := c.RemoveAliasAddress(t.Context(), &pfsenseapipb.RemoveAliasAddressRequest{Alias: "KILL_SWITCH", Address: a}); err != nil {
			t.Fatal(err)
		}
	}
	if got := alias(fw, "KILL_SWITCH"); len(got.Addresses) != 0 || len(got.Details) != 0 {
		t.Errorf("KILL_SWITCH after removing every member = %v, want empty", got)
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

	if a := got.GetAliases()[slices.IndexFunc(got.GetAliases(), func(a *pfsenseapipb.Alias) bool { return a.GetName() == "VPN_BYPASS" })]; !slices.Contains(a.GetAddresses(), "198.18.1.23") {
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
			_, err := c.RemoveAliasAddress(t.Context(), &pfsenseapipb.RemoveAliasAddressRequest{Alias: "DNS_SERVER", Address: []string{"198.18.1.202", "198.18.1.1", "198.18.30.1", "198.18.1.202"}[i]})
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
	if got := alias(fw, "VPN_BYPASS").Addresses; len(got) != 13 {
		t.Errorf("VPN_BYPASS = %v, want 13 members", got)
	}
	if got := alias(fw, "DNS_SERVER"); len(got.Addresses) != 0 || len(got.Details) != 0 {
		t.Errorf("DNS_SERVER = %v, want empty", got)
	}
	if fw.FirewallPending() || fw.Pending() {
		t.Error("changes still pending, want applied")
	}
}

func TestMovingAnAddressWritesBothAliasesAndAppliesOnce(t *testing.T) {
	fw := firewall(t, "baseline")
	_, c := start(t, fw.URL)

	got, err := c.MoveAliasAddress(t.Context(), &pfsenseapipb.MoveAliasAddressRequest{Address: "198.18.1.6", From: []string{"VPN_BYPASS"}, To: "KILL_SWITCH", Detail: "AndroidTV"})
	if err != nil {
		t.Fatal(err)
	}

	wantBypass := bypass()
	wantBypass.Addresses = slices.Delete(wantBypass.Addresses, 2, 3)
	wantBypass.Details = slices.Delete(wantBypass.Details, 2, 3)
	if diff := cmp.Diff(wantBypass, alias(fw, "VPN_BYPASS")); diff != "" {
		t.Errorf("VPN_BYPASS (-want +got):\n%s", diff)
	}
	wantKill := pfsensefake.Alias{Name: "KILL_SWITCH", Addresses: []string{"198.18.1.212", "198.18.1.6"}, Details: []string{"Entry added Sat, 17 Oct 2026 19:15:47 +0200", "AndroidTV"}}
	if diff := cmp.Diff(wantKill, alias(fw, "KILL_SWITCH")); diff != "" {
		t.Errorf("KILL_SWITCH (-want +got):\n%s", diff)
	}
	var names []string
	for _, a := range got.GetAliases() {
		names = append(names, a.GetName())
	}
	if !slices.Equal(names, []string{"VPN_BYPASS", "KILL_SWITCH"}) {
		t.Errorf("answer names %v, want the two aliases written", names)
	}
	// One lookup, then both writes, the last applying them.
	want := []string{readAliases, patchAlias, patchAlias, pfsensefake.AppliedFirewall}
	if diff := cmp.Diff(want, aliasWrites(fw)); diff != "" {
		t.Errorf("calls (-want +got):\n%s", diff)
	}
}

// pfSense answers POST firewall/apply before the filter reloads, so the
// move applies with its last write, which answers once applied.
func TestAMoveIsAppliedWhenItAnswers(t *testing.T) {
	fw := firewallWith(t, "baseline", pfsensefake.Options{FirewallApplyLag: time.Minute})
	_, c := start(t, fw.URL)

	if _, err := c.MoveAliasAddress(t.Context(), &pfsenseapipb.MoveAliasAddressRequest{Address: "198.18.1.6", From: []string{"VPN_BYPASS"}, To: "KILL_SWITCH", Detail: "AndroidTV"}); err != nil {
		t.Fatal(err)
	}

	if fw.FirewallPending() {
		t.Error("alias changes still pending, want applied")
	}
}

func TestMovingAnAddressWithNoTargetTakesItOutOfEveryAliasHoldingIt(t *testing.T) {
	fw := firewall(t, "baseline")
	_, c := start(t, fw.URL)

	// VictronAllow and DNS_SERVER both hold 198.18.1.202; VPN_BYPASS doesn't.
	if _, err := c.MoveAliasAddress(t.Context(), &pfsenseapipb.MoveAliasAddressRequest{Address: "198.18.1.202", From: []string{"VictronAllow", "VPN_BYPASS", "DNS_SERVER"}}); err != nil {
		t.Fatal(err)
	}

	for _, name := range []string{"VictronAllow", "DNS_SERVER"} {
		if a := alias(fw, name); slices.Contains(a.Addresses, "198.18.1.202") || len(a.Details) != len(a.Addresses) {
			t.Errorf("%s = %v, want 198.18.1.202 gone with its description", name, a)
		}
	}
	if diff := cmp.Diff(bypass(), alias(fw, "VPN_BYPASS")); diff != "" {
		t.Errorf("VPN_BYPASS (-want +got):\n%s", diff)
	}
	want := []string{readAliases, patchAlias, patchAlias, pfsensefake.AppliedFirewall}
	if diff := cmp.Diff(want, aliasWrites(fw)); diff != "" {
		t.Errorf("calls (-want +got):\n%s", diff)
	}
}

func TestMovingAnAddressAlreadyWhereItBelongsWritesNothing(t *testing.T) {
	fw := firewall(t, "baseline")
	_, c := start(t, fw.URL)

	if _, err := c.MoveAliasAddress(t.Context(), &pfsenseapipb.MoveAliasAddressRequest{Address: "198.18.1.6", From: []string{"KILL_SWITCH"}, To: "VPN_BYPASS"}); err != nil {
		t.Fatal(err)
	}

	if diff := cmp.Diff([]string{readAliases}, aliasWrites(fw)); diff != "" {
		t.Errorf("calls (-want +got):\n%s", diff)
	}
}

func TestAMoveNamingAnAliasTheFirewallLacksWritesNothing(t *testing.T) {
	fw := firewall(t, "baseline")
	_, c := start(t, fw.URL)

	_, err := c.MoveAliasAddress(t.Context(), &pfsenseapipb.MoveAliasAddressRequest{Address: "198.18.1.6", From: []string{"VPN_BYPASS"}, To: "NO_SUCH"})

	if status.Code(err) != codes.NotFound || !strings.Contains(err.Error(), "NO_SUCH") {
		t.Errorf("Move = %v, want NotFound naming the alias", err)
	}
	if diff := cmp.Diff([]string{readAliases}, aliasWrites(fw)); diff != "" {
		t.Errorf("calls (-want +got):\n%s", diff)
	}
}

func TestAMoveWhoseLastWriteIsRefusedStillAppliesTheOnesBefore(t *testing.T) {
	fw := firewall(t, "baseline")
	_, c := start(t, fw.URL)
	if _, err := c.AddAliasAddress(t.Context(), &pfsenseapipb.AddAliasAddressRequest{Alias: "VPN_BYPASS", Address: "2001:db8::6"}); err != nil {
		t.Fatal(err)
	}

	// A network alias takes no bare IPv6 address.
	_, err := c.MoveAliasAddress(t.Context(), &pfsenseapipb.MoveAliasAddressRequest{Address: "2001:db8::6", From: []string{"VPN_BYPASS"}, To: "WAN_SUBNETWORK"})

	if status.Code(err) != codes.FailedPrecondition || !strings.Contains(err.Error(), "not a valid CIDR") {
		t.Errorf("Move = %v, want pfSense's refusal", err)
	}
	if diff := cmp.Diff(bypass(), alias(fw, "VPN_BYPASS")); diff != "" {
		t.Errorf("VPN_BYPASS (-want +got):\n%s", diff)
	}
	if fw.FirewallPending() {
		t.Error("alias changes still pending, want the first write applied")
	}
	if got := aliasWrites(fw); !slices.Contains(got, firewallPost) {
		t.Errorf("calls = %v, want POST firewall/apply after the refusal", got)
	}
}
