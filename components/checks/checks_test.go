package checks_test

import (
	"net"
	"slices"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	"github.com/itsKontra/ddash/components/checks"
	"github.com/itsKontra/ddash/components/checks/checkspb"
	ddashpb "github.com/itsKontra/ddash/proto/ddash"
	"github.com/itsKontra/ddash/sdk/sdktest"
)

// start runs checks in a harness and returns its client.
func start(t *testing.T, opts ...sdktest.Option) (*sdktest.Harness, checkspb.ChecksServiceClient) {
	t.Helper()
	h := sdktest.New(t, checks.Definition(), opts...)
	h.MustStart(&checkspb.ChecksConfig{})
	return h, checkspb.NewChecksServiceClient(h.Conn)
}

func create(t *testing.T, c checkspb.ChecksServiceClient, spec *checkspb.CheckSpec) *checkspb.Check {
	t.Helper()
	resp, err := c.CreateCheck(t.Context(), &checkspb.CreateCheckRequest{Spec: spec})
	if err != nil {
		t.Fatalf("CreateCheck: %v", err)
	}
	return resp.GetCheck()
}

func list(t *testing.T, c checkspb.ChecksServiceClient) []*checkspb.Check {
	t.Helper()
	resp, err := c.ListChecks(t.Context(), &checkspb.ListChecksRequest{})
	if err != nil {
		t.Fatalf("ListChecks: %v", err)
	}
	return resp.GetChecks()
}

func find(t *testing.T, c checkspb.ChecksServiceClient, id int64) *checkspb.Check {
	t.Helper()
	for _, ch := range list(t, c) {
		if ch.GetId() == id {
			return ch
		}
	}
	t.Fatalf("Check %d isn't listed", id)
	return nil
}

// firstBeat waits, in real time, until Check id was probed once.
func firstBeat(t *testing.T, c checkspb.ChecksServiceClient, id int64) *checkspb.Check {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if ch := find(t, c, id); ch.GetLastBeat() != nil {
			return ch
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("Check %d wasn't probed within 15 s", id)
	return nil
}

// listening returns the address of a local TCP port that accepts.
func listening(t *testing.T) string {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	return l.Addr().String()
}

// closed returns the address of a local TCP port nothing listens on.
func closed(t *testing.T) string {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	l.Close()
	return addr
}

func TestCheckStatesAreAFastGetter(t *testing.T) {
	m := checkspb.File_checks_checks_proto.Services().ByName("ChecksService").Methods().ByName("ListChecks")

	g, _ := proto.GetExtension(m.Options(), ddashpb.E_Getter).(*ddashpb.Getter)
	if g.GetRefresh() != ddashpb.Getter_FAST {
		t.Errorf("ListChecks refresh = %v, want FAST", g.GetRefresh())
	}
}

func TestCreateRefusesWhatCantBeProbed(t *testing.T) {
	_, c := start(t)

	for _, tc := range []struct {
		spec *checkspb.CheckSpec
		want string
	}{
		{&checkspb.CheckSpec{Target: "198.18.1.10:22"}, "type must be TCP, ICMP or DNS"},
		{&checkspb.CheckSpec{Type: checkspb.CheckType_CHECK_TYPE_TCP, Target: "198.18.1.10"}, "target must be host:port, e.g. 198.18.1.10:22"},
		{&checkspb.CheckSpec{Type: checkspb.CheckType_CHECK_TYPE_TCP, Target: "nas:99999"}, "target must be host:port, e.g. 198.18.1.10:22"},
		{&checkspb.CheckSpec{Type: checkspb.CheckType_CHECK_TYPE_ICMP, Target: "https://nas.home.example"}, "target must be a host name or IP address"},
		{&checkspb.CheckSpec{Type: checkspb.CheckType_CHECK_TYPE_DNS, Target: "", Dns: &checkspb.DnsQuery{Name: "nas.home.example"}}, "target must be the DNS server's address, e.g. 198.18.1.1 or 198.18.1.1:5353"},
		{&checkspb.CheckSpec{Type: checkspb.CheckType_CHECK_TYPE_DNS, Target: "198.18.1.1"}, "dns.name must be the name to resolve, e.g. jellyfin.home.example"},
	} {
		_, err := c.CreateCheck(t.Context(), &checkspb.CreateCheckRequest{Spec: tc.spec})
		if st := status.Convert(err); st.Code() != codes.InvalidArgument || st.Message() != tc.want {
			t.Errorf("%v: %v, want InvalidArgument %q", tc.spec, err, tc.want)
		}
	}
	if n := len(list(t, c)); n != 0 {
		t.Errorf("%d Checks created, want none", n)
	}
}

func TestCreateAcceptsEachTypesTargets(t *testing.T) {
	_, c := start(t)

	for _, spec := range []*checkspb.CheckSpec{
		{Type: checkspb.CheckType_CHECK_TYPE_TCP, Target: "nas.home.example:22"},
		{Type: checkspb.CheckType_CHECK_TYPE_TCP, Target: "[2001:db8::10]:443"},
		{Type: checkspb.CheckType_CHECK_TYPE_ICMP, Target: "198.18.1.1"},
		{Type: checkspb.CheckType_CHECK_TYPE_ICMP, Target: "2001:db8::1"},
		{Type: checkspb.CheckType_CHECK_TYPE_DNS, Target: "198.18.1.1:5353", Dns: &checkspb.DnsQuery{Name: "nas.home.example.", RecordType: checkspb.RecordType_RECORD_TYPE_AAAA}},
	} {
		if _, err := c.CreateCheck(t.Context(), &checkspb.CreateCheckRequest{Spec: spec}); err != nil {
			t.Errorf("%v: %v", spec, err)
		}
	}
}

func TestChecksAreListedByName(t *testing.T) {
	_, c := start(t)
	create(t, c, &checkspb.CheckSpec{Name: "router", Type: checkspb.CheckType_CHECK_TYPE_ICMP, Target: "198.18.1.1"})
	create(t, c, &checkspb.CheckSpec{Type: checkspb.CheckType_CHECK_TYPE_ICMP, Target: "nas.home.example"})
	create(t, c, &checkspb.CheckSpec{Name: "AdGuard DNS", Type: checkspb.CheckType_CHECK_TYPE_DNS, Target: "198.18.1.2", Dns: &checkspb.DnsQuery{Name: "home.example"}})

	var got []string
	for _, ch := range list(t, c) {
		got = append(got, ch.GetSpec().GetName()+"|"+ch.GetSpec().GetTarget())
	}

	want := []string{"AdGuard DNS|198.18.1.2", "|nas.home.example", "router|198.18.1.1"}
	if !slices.Equal(got, want) {
		t.Errorf("listed %v, want %v", got, want)
	}
}

func TestATCPCheckIsUpWhenItConnects(t *testing.T) {
	_, c := start(t)

	ch := firstBeat(t, c, create(t, c, &checkspb.CheckSpec{Name: "nas", Type: checkspb.CheckType_CHECK_TYPE_TCP, Target: listening(t)}).GetId())

	if ch.GetState() != checkspb.State_STATE_UP || ch.GetReason() != "" {
		t.Errorf("state %v (%q), want up", ch.GetState(), ch.GetReason())
	}
	if ch.GetLatencyMs() <= 0 {
		t.Errorf("latency %v ms, want the connect time", ch.GetLatencyMs())
	}
}

func TestATCPCheckFailsWhenRefused(t *testing.T) {
	_, c := start(t)

	ch := firstBeat(t, c, create(t, c, &checkspb.CheckSpec{Type: checkspb.CheckType_CHECK_TYPE_TCP, Target: closed(t)}).GetId())

	if ch.GetState() != checkspb.State_STATE_PENDING || ch.GetFailures() != 1 || ch.GetReason() != "connection refused" {
		t.Errorf("state %v, %d failures (%q), want pending after 1 failure: connection refused", ch.GetState(), ch.GetFailures(), ch.GetReason())
	}
}
