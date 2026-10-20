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
		{MAC: "02:5e:92:9d:f0:a6", IP: "198.18.1.2", Hostname: "w0"},
		{MAC: "02:56:74:bd:32:5b", IP: "198.18.1.3", Hostname: "w1", Description: "Access Point (OpenWrt)"},
		{MAC: "02:eb:2f:cc:08:07", IP: "198.18.1.4", Hostname: "lwip"},
		{MAC: "02:08:c2:85:3d:a1", IP: "198.18.1.5", Description: "Amazon Alexa"},
		{MAC: "02:4a:a6:ab:ab:d4", IP: "198.18.1.6", Hostname: "AndroidTV"},
		{MAC: "02:ff:c4:52:c5:c3", IP: "198.18.1.7", Hostname: "einstein"},
		{MAC: "02:1c:d5:5e:3d:e5", IP: "198.18.1.10", Hostname: "LexmarkCX317dn"},
		{MAC: "02:b9:d2:5c:b9:9f", IP: "198.18.1.11", Hostname: "s22-von-operator"},
		{MAC: "02:75:79:44:12:e3", IP: "198.18.1.12", Hostname: "operator-pc"},
		{MAC: "02:61:c0:a3:8a:16", IP: "198.18.1.101", Hostname: "homeassistant"},
		{MAC: "02:e6:29:fa:fc:39", IP: "198.18.1.102", Hostname: "laptop-2"},
	}
}

// quirkMappings is the LAN's static mappings in the dhcp-quirks scenario,
// the first with an upper case MAC (the homelab's are all lower case).
func quirkMappings() []pfsensefake.Mapping {
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

	if maps := got.GetServers()[0].GetStaticMappings(); len(maps) != 12 || maps[11].GetMac() != "02:5e:00:00:20:01" {
		t.Errorf("LAN static mappings after the write = %v, want the new one last", maps)
	}
}

// The homelab's MACs are all lower case: the dhcp-quirks scenario has an
// upper case one.
func TestRenamingAStaticMappingChangesOnlyItsDescription(t *testing.T) {
	fw := firewall(t, "dhcp-quirks")
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
	end := quirkMappings()
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

	if got := fw.StaticMappings("lan"); len(got) != 12 || got[11].Description != "Anna's phone" || got[1].Description != "Access Point (OpenWrt)" {
		t.Errorf("the LAN's static mappings = %v, want the phone renamed and the access point untouched", got)
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
	if got := len(fw.StaticMappings("lan")); got != 15 {
		t.Errorf("%d static mappings, want 15", got)
	}
	if fw.Pending() {
		t.Error("DHCP changes still pending, want applied")
	}
}

func TestCreateRefusesAMACOrAnAddressAlreadyReserved(t *testing.T) {
	for name, m := range map[string]*pfsenseapipb.StaticMapping{
		"MAC":     {Interface: "lan", Mac: "02:4a:a6:ab:ab:d4", Ip: "198.18.1.23", Description: "TV again"},
		"address": {Interface: "lan", Mac: "02:5e:00:00:20:01", Ip: "198.18.1.12", Description: "Phone"},
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
