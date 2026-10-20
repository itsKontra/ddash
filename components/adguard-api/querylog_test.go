package adguardapi_test

import (
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/testing/protocmp"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/itsKontra/ddash/components/adguard-api/adguardapipb"
)

func queries(t *testing.T, c adguardapipb.AdguardApiServiceClient, req *adguardapipb.ListQueriesRequest) *adguardapipb.ListQueriesResponse {
	t.Helper()
	got, err := c.ListQueries(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func domains(r *adguardapipb.ListQueriesResponse) string {
	var out []string
	for _, q := range r.GetQueries() {
		out = append(out, q.GetDomain())
	}
	return strings.Join(out, " ")
}

func TestTheQueryLogOfOneClientIsItsQueriesNewestFirst(t *testing.T) {
	_, c := start(t, adguard(t, "baseline").URL)

	got := queries(t, c, &adguardapipb.ListQueriesRequest{Client: "198.18.1.150"})

	if want := "site1.example site4.example site7.example site10.example site13.example site16.example site19.example"; domains(got) != want {
		t.Errorf("queries = %s, want %s", domains(got), want)
	}
	first := &adguardapipb.Query{
		Time:       timestamppb.New(time.Date(2026, 10, 20, 9, 59, 50, 123456000, time.UTC)),
		Domain:     "site1.example",
		Type:       "A",
		Client:     "198.18.1.150",
		ClientName: "galaxy-s24",
		Reason:     "NotFilteredNotFound",
		Status:     "NOERROR",
		ElapsedMs:  4.5,
		Upstream:   "https://dns.upstream.example:443/dns-query",
	}
	if diff := cmp.Diff(first, got.GetQueries()[0], protocmp.Transform()); diff != "" {
		t.Errorf("newest query (-want +got):\n%s", diff)
	}
	blocked := got.GetQueries()[5]
	if !blocked.GetBlocked() || blocked.GetRule() != "||site16.example^" || blocked.GetReason() != "FilteredBlackList" {
		t.Errorf("site16 = %v, want blocked by its rule", blocked)
	}
}

func TestTheQueryLogMatchesTheClientsAddressExactly(t *testing.T) {
	_, c := start(t, adguard(t, "baseline").URL)

	// 198.18.1.2 is a substring of .20 and .21, whose queries it isn't.
	if got := queries(t, c, &adguardapipb.ListQueriesRequest{Client: "198.18.1.2"}); len(got.GetQueries()) != 0 {
		t.Errorf("queries of 198.18.1.2 = %s, want none", domains(got))
	}
}

func TestTheQueryLogPagesOlderThanTheLastPagesOldest(t *testing.T) {
	_, c := start(t, adguard(t, "baseline").URL)

	first := queries(t, c, &adguardapipb.ListQueriesRequest{Client: "198.18.1.150", Limit: 3})
	second := queries(t, c, &adguardapipb.ListQueriesRequest{Client: "198.18.1.150", Limit: 3, OlderThan: first.GetOldest()})

	if domains(first) != "site1.example site4.example site7.example" || domains(second) != "site10.example site13.example site16.example" {
		t.Errorf("pages = %q then %q", domains(first), domains(second))
	}
}

func TestTheQueryLogShowsOnlyBlockedQueriesWhenAsked(t *testing.T) {
	_, c := start(t, adguard(t, "baseline").URL)

	got := queries(t, c, &adguardapipb.ListQueriesRequest{Client: "198.18.1.150", BlockedOnly: true})

	if domains(got) != "site16.example site19.example" {
		t.Errorf("blocked queries = %s, want site16 and site19", domains(got))
	}
}

func TestTheQueryLogRefusesAClientThatIsNoAddress(t *testing.T) {
	_, c := start(t, adguard(t, "baseline").URL)

	_, err := c.ListQueries(t.Context(), &adguardapipb.ListQueriesRequest{Client: "galaxy"})
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("ListQueries(galaxy) = %v, want InvalidArgument", err)
	}
}
