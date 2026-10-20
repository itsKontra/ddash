package pfsenseapi_test

import (
	"fmt"
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
	readServers  = "GET /api/v2/services/dhcp_servers"
	createMap    = "POST /api/v2/services/dhcp_server/static_mapping"
	patchMap     = "PATCH /api/v2/services/dhcp_server/static_mapping"
	applyRequest = "POST /api/v2/services/dhcp_server/apply"
)

// baselineMappings is the LAN's static mappings in the baseline fixtures.
func baselineMappings() []pfsensefake.Mapping {
	return []pfsensefake.Mapping{
		{MAC: "02:5E:00:00:10:01", IP: "198.18.1.20", Hostname: "living-room-tv", Description: "Living room TV"},
		{MAC: "02:5e:00:00:10:02", IP: "198.18.1.21", Hostname: "work-laptop", Description: "Work laptop"},
		{MAC: "02:5e:00:00:10:03", IP: "198.18.1.22", Hostname: "printer"},
	}
}

func phone() *pfsenseapipb.StaticMapping {
	return &pfsenseapipb.StaticMapping{Interface: "lan", Mac: "02:5e:00:00:20:01", Ip: "198.18.1.23", Hostname: "galaxy-s24", Description: "Phone"}
}

// writes is the firewall's log without the reads served from fixtures.
func writes(fw *fakeFirewall) []string {
	var out []string
	for _, e := range fw.Log() {
		if e == readServers || e == createMap || e == patchMap || e == applyRequest || e == pfsensefake.Applied {
			out = append(out, e)
		}
	}
	return out
}

func TestCreatingAStaticMappingReservesTheAddressAndAppliesItWithTheWrite(t *testing.T) {
	fw := firewall(t, "baseline")
	_, c := start(t, fw.URL)

	got, err := c.CreateStaticMapping(t.Context(), &pfsenseapipb.CreateStaticMappingRequest{Mapping: phone()})
	if err != nil {
		t.Fatal(err)
	}

	if diff := cmp.Diff(phone(), got.GetMapping(), protocmp.Transform()); diff != "" {
		t.Errorf("created (-want +got):\n%s", diff)
	}
	want := append(baselineMappings(), pfsensefake.Mapping{MAC: "02:5e:00:00:20:01", IP: "198.18.1.23", Hostname: "galaxy-s24", Description: "Phone"})
	if diff := cmp.Diff(want, fw.StaticMappings("lan")); diff != "" {
		t.Errorf("the LAN's static mappings (-want +got):\n%s", diff)
	}
	if fw.Pending() {
		t.Error("DHCP changes still pending, want applied")
	}
	// The ids are looked up right before writing, and the write applies
	// itself: no apply before it, and no separate apply after.
	if diff := cmp.Diff([]string{readServers, createMap, pfsensefake.Applied}, writes(fw)); diff != "" {
		t.Errorf("calls (-want +got):\n%s", diff)
	}
}

func TestAStaticMappingCreatedShowsInTheNextReadOfTheServers(t *testing.T) {
	fw := firewall(t, "baseline")
	_, c := start(t, fw.URL)
	if _, err := c.ListDhcpServers(t.Context(), &pfsenseapipb.ListDhcpServersRequest{}); err != nil {
		t.Fatal(err)
	}

	if _, err := c.CreateStaticMapping(t.Context(), &pfsenseapipb.CreateStaticMappingRequest{Mapping: phone()}); err != nil {
		t.Fatal(err)
	}
	got, err := c.ListDhcpServers(t.Context(), &pfsenseapipb.ListDhcpServersRequest{})
	if err != nil {
		t.Fatal(err)
	}

	if maps := got.GetServers()[0].GetStaticMappings(); len(maps) != 4 || maps[3].GetMac() != "02:5e:00:00:20:01" {
		t.Errorf("LAN static mappings after the write = %v, want the new one last", maps)
	}
}

func TestRenamingAStaticMappingChangesOnlyItsDescription(t *testing.T) {
	fw := firewall(t, "baseline")
	_, c := start(t, fw.URL)

	// Upper case in the fixture, any case asked: MACs match regardless.
	got, err := c.RenameStaticMapping(t.Context(), &pfsenseapipb.RenameStaticMappingRequest{Interface: "lan", Mac: "02:5e:00:00:10:01", Description: "Kitchen TV"})
	if err != nil {
		t.Fatal(err)
	}

	want := &pfsenseapipb.StaticMapping{Interface: "lan", Mac: "02:5e:00:00:10:01", Ip: "198.18.1.20", Hostname: "living-room-tv", Description: "Kitchen TV"}
	if diff := cmp.Diff(want, got.GetMapping(), protocmp.Transform()); diff != "" {
		t.Errorf("renamed (-want +got):\n%s", diff)
	}
	end := baselineMappings()
	end[0].Description = "Kitchen TV"
	if diff := cmp.Diff(end, fw.StaticMappings("lan")); diff != "" {
		t.Errorf("the LAN's static mappings (-want +got):\n%s", diff)
	}
	if fw.Pending() {
		t.Error("DHCP changes still pending, want applied")
	}
	if diff := cmp.Diff([]string{readServers, patchMap, pfsensefake.Applied}, writes(fw)); diff != "" {
		t.Errorf("calls (-want +got):\n%s", diff)
	}
}

// pfSense's ids are positions: a mapping created or removed before the
// rename shifts them, so each write looks its id up again.
func TestRenameLooksTheIdUpRightBeforeWriting(t *testing.T) {
	fw := firewall(t, "baseline")
	_, c := start(t, fw.URL)
	if _, err := c.ListDhcpServers(t.Context(), &pfsenseapipb.ListDhcpServersRequest{}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.CreateStaticMapping(t.Context(), &pfsenseapipb.CreateStaticMappingRequest{Mapping: phone()}); err != nil {
		t.Fatal(err)
	}

	if _, err := c.RenameStaticMapping(t.Context(), &pfsenseapipb.RenameStaticMappingRequest{Interface: "lan", Mac: "02:5e:00:00:20:01", Description: "Anna's phone"}); err != nil {
		t.Fatal(err)
	}

	if got := fw.StaticMappings("lan"); len(got) != 4 || got[3].Description != "Anna's phone" || got[0].Description != "Living room TV" {
		t.Errorf("the LAN's static mappings = %v, want the phone renamed and the TV untouched", got)
	}
}

func TestWritesRunOneAtATimeUnderConcurrentCalls(t *testing.T) {
	fw := firewallWith(t, "baseline", pfsensefake.Options{WriteDelay: 30 * time.Millisecond})
	_, c := start(t, fw.URL)

	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := range 4 {
		wg.Go(func() {
			m := &pfsenseapipb.StaticMapping{Interface: "lan", Mac: fmt.Sprintf("02:5e:00:00:30:%02x", i), Ip: fmt.Sprintf("198.18.1.%d", 30+i), Description: fmt.Sprintf("Device %d", i)}
			_, err := c.CreateStaticMapping(t.Context(), &pfsenseapipb.CreateStaticMappingRequest{Mapping: m})
			errs <- err
		})
		wg.Go(func() {
			mac := baselineMappings()[i%3].MAC
			_, err := c.RenameStaticMapping(t.Context(), &pfsenseapipb.RenameStaticMappingRequest{Interface: "lan", Mac: mac, Description: fmt.Sprintf("Renamed %d", i)})
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
	// Each write's lookup sits right before it: nothing interleaves.
	var lookups []string
	for i, e := range writes(fw) {
		if e == createMap || e == patchMap {
			lookups = append(lookups, writes(fw)[i-1])
		}
	}
	if want := strings.Repeat(readServers+",", 8); strings.Join(lookups, ",")+"," != want {
		t.Errorf("before each write: %v, want the lookup", lookups)
	}
	if got := len(fw.StaticMappings("lan")); got != 7 {
		t.Errorf("%d static mappings, want 7", got)
	}
	if fw.Pending() {
		t.Error("DHCP changes still pending, want applied")
	}
}

func TestCreateRefusesAMACOrAnAddressAlreadyReserved(t *testing.T) {
	for name, m := range map[string]*pfsenseapipb.StaticMapping{
		"MAC":     {Interface: "lan", Mac: "02:5e:00:00:10:01", Ip: "198.18.1.23", Description: "TV again"},
		"address": {Interface: "lan", Mac: "02:5e:00:00:20:01", Ip: "198.18.1.21", Description: "Phone"},
	} {
		t.Run(name, func(t *testing.T) {
			fw := firewall(t, "baseline")
			_, c := start(t, fw.URL)

			_, err := c.CreateStaticMapping(t.Context(), &pfsenseapipb.CreateStaticMappingRequest{Mapping: m})

			if status.Code(err) != codes.AlreadyExists {
				t.Errorf("Create = %v, want AlreadyExists", err)
			}
			if diff := cmp.Diff(baselineMappings(), fw.StaticMappings("lan")); diff != "" {
				t.Errorf("static mappings changed (-want +got):\n%s", diff)
			}
		})
	}
}

func TestRenameRefusesAMACWithoutAStaticMapping(t *testing.T) {
	fw := firewall(t, "baseline")
	_, c := start(t, fw.URL)

	_, err := c.RenameStaticMapping(t.Context(), &pfsenseapipb.RenameStaticMappingRequest{Interface: "lan", Mac: "02:5e:00:00:20:01", Description: "Phone"})

	if status.Code(err) != codes.NotFound || !strings.Contains(err.Error(), "02:5e:00:00:20:01") {
		t.Errorf("Rename = %v, want NotFound naming the MAC", err)
	}
	if got := writes(fw); len(got) != 1 {
		t.Errorf("calls = %v, want only the lookup", got)
	}
}

func TestWritesRefuseAnInterfaceWithoutADHCPServer(t *testing.T) {
	fw := firewall(t, "baseline")
	_, c := start(t, fw.URL)
	m := phone()
	m.Interface = "opt9"

	_, err := c.CreateStaticMapping(t.Context(), &pfsenseapipb.CreateStaticMappingRequest{Mapping: m})

	if status.Code(err) != codes.NotFound || !strings.Contains(err.Error(), "opt9") {
		t.Errorf("Create = %v, want NotFound naming the interface", err)
	}
}

func TestCreateRefusesABadMACOrAddressWithoutWriting(t *testing.T) {
	for name, m := range map[string]*pfsenseapipb.StaticMapping{
		"MAC":     {Interface: "lan", Mac: "phone", Ip: "198.18.1.23"},
		"address": {Interface: "lan", Mac: "02:5e:00:00:20:01", Ip: "198.18.1"},
	} {
		t.Run(name, func(t *testing.T) {
			fw := firewall(t, "baseline")
			_, c := start(t, fw.URL)

			_, err := c.CreateStaticMapping(t.Context(), &pfsenseapipb.CreateStaticMappingRequest{Mapping: m})

			if status.Code(err) != codes.InvalidArgument {
				t.Errorf("Create = %v, want InvalidArgument", err)
			}
			if got := writes(fw); len(got) != 0 {
				t.Errorf("calls = %v, want none", got)
			}
		})
	}
}

func TestAWritePfSenseRefusesFailsWithWhatPfSenseSaid(t *testing.T) {
	fw := firewall(t, "baseline")
	_, c := start(t, fw.URL)
	m := phone()
	m.Hostname = "not a hostname"

	_, err := c.CreateStaticMapping(t.Context(), &pfsenseapipb.CreateStaticMappingRequest{Mapping: m})

	if status.Code(err) != codes.FailedPrecondition || !strings.Contains(err.Error(), "valid hostname") {
		t.Errorf("Create = %v, want FailedPrecondition with pfSense's message", err)
	}
}
