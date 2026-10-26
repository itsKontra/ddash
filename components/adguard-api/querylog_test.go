package adguardapi_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/testing/protocmp"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/itsKontra/ddash/components/adguard-api/adguardapipb"
	"github.com/itsKontra/ddash/components/adguard-api/adguardfake"
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

// at is the time of the query log line i seconds before the newest.
func at(i int) string {
	return time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC).Add(-time.Duration(i) * time.Second).Format(time.RFC3339Nano)
}

// line is the query log line i seconds before the newest.
func line(i int, client, domain, qtype, reason string) map[string]any {
	return map[string]any{
		"time": at(i), "client": client, "reason": reason, "status": "NOERROR",
		"question": map[string]any{"name": domain, "type": qtype},
	}
}

// readTo says whether r read lines lines back to the line at time want.
func readTo(r *adguardapipb.ListQueriesResponse, lines int, want string) bool {
	t, err := time.Parse(time.RFC3339Nano, want)
	return err == nil && int(r.GetLinesRead()) == lines && r.GetReached().AsTime().Equal(t)
}

const blocked, allowed = adguardapipb.QueryMode_QUERY_MODE_BLOCKED, adguardapipb.QueryMode_QUERY_MODE_ALLOWED

func TestTheQueryLogReadsTheNewestQueryAsAdGuardLoggedIt(t *testing.T) {
	_, c := start(t, adguard(t, "baseline").URL)

	got := queries(t, c, &adguardapipb.ListQueriesRequest{Client: "198.18.1.102", Limit: 1})

	want := &adguardapipb.Query{
		Time:       timestamppb.New(time.Date(2026, 10, 20, 16, 7, 32, 485464089, time.UTC)),
		Domain:     "_ldap._tcp.dc._msdcs.home.example",
		Type:       "SRV",
		Client:     "198.18.1.102",
		ClientName: "Laptop",
		Reason:     "NotFilteredNotFound",
		Status:     "NOERROR",
		ElapsedMs:  0.026257,
		Upstream:   "tls://pfsense.home.example:853",
		Cached:     true,
	}
	if diff := cmp.Diff([]*adguardapipb.Query{want}, got.GetQueries(), protocmp.Transform()); diff != "" {
		t.Errorf("newest query (-want +got):\n%s", diff)
	}
}

func TestTheQueryLogOfOneClientIsItsQueriesNewestFirst(t *testing.T) {
	// The homelab's recorded log holds one client's unblocked queries; this
	// scenario's mixes three clients with blocked queries.
	_, c := start(t, adguard(t, "several-clients").URL)

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

	// 198.18.1.10 is a substring of .102, whose queries it isn't.
	if got := queries(t, c, &adguardapipb.ListQueriesRequest{Client: "198.18.1.10"}); len(got.GetQueries()) != 0 {
		t.Errorf("queries of 198.18.1.10 = %s, want none", domains(got))
	}
}

func TestTheQueryLogPagesOlderThanTheLastPagesOldest(t *testing.T) {
	_, c := start(t, adguard(t, "baseline").URL)

	first := queries(t, c, &adguardapipb.ListQueriesRequest{Client: "198.18.1.102", Limit: 3})
	second := queries(t, c, &adguardapipb.ListQueriesRequest{Client: "198.18.1.102", Limit: 3, OlderThan: first.GetOldest()})

	if domains(first) != "_ldap._tcp.dc._msdcs.home.example _ldap._tcp.dc._msdcs.home.example site196.example" || domains(second) != "site200.example site204.example site205.example" {
		t.Errorf("pages = %q then %q", domains(first), domains(second))
	}
}

func TestTheQueryLogShowsBlockedOrAllowedQueriesAsAdGuardFiltersThem(t *testing.T) {
	// The homelab's recorded log has no blocked query.
	srv := adguard(t, "several-clients")
	_, c := start(t, srv.URL)

	b := queries(t, c, &adguardapipb.ListQueriesRequest{Client: "198.18.1.150", Mode: blocked})
	a := queries(t, c, &adguardapipb.ListQueriesRequest{Client: "198.18.1.150", Mode: allowed})

	if domains(b) != "site16.example site19.example" {
		t.Errorf("blocked queries = %s, want site16 and site19", domains(b))
	}
	if domains(a) != "site1.example site4.example site7.example site10.example site13.example" {
		t.Errorf("allowed queries = %s, want the other five", domains(a))
	}
	if n := len(slices.DeleteFunc(srv.Log(), func(r string) bool { return r != "GET /control/querylog" })); n != 2 {
		t.Errorf("%d query log requests, want one per mode", n)
	}
}

func TestAnAdGuardTooOldToFilterByReasonStillShowsOnlyTheModesQueries(t *testing.T) {
	// AdGuard before v0.107.77 ignores reason.
	fake := adguard(t, "several-clients")
	old := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		q.Del("reason")
		r.URL.RawQuery = q.Encode()
		fake.ServeHTTP(w, r)
	}))
	t.Cleanup(old.Close)
	_, c := start(t, old.URL)

	b := queries(t, c, &adguardapipb.ListQueriesRequest{Client: "198.18.1.150", Mode: blocked})
	a := queries(t, c, &adguardapipb.ListQueriesRequest{Client: "198.18.1.150", Mode: allowed, Type: "aaaa"})

	if domains(b) != "site16.example site19.example" {
		t.Errorf("blocked queries = %s, want site16 and site19", domains(b))
	}
	if domains(a) != "site10.example" {
		t.Errorf("allowed AAAA queries = %s, want site10", domains(a))
	}
}

func TestBlockedQueriesFillTheLimitEvenWhenTheyAreSparse(t *testing.T) {
	// The Device Page's last 5 blocked: 6 among 300 lines, while AdGuard
	// reads only 10 lines a request unless asked for an exact count.
	blockedAt := []int{3, 40, 90, 150, 220, 290}
	var log []map[string]any
	for i := range 300 {
		reason := "NotFilteredNotFound"
		if slices.Contains(blockedAt, i) {
			reason = "FilteredBlackList"
		}
		log = append(log, line(i, "198.18.1.9", fmt.Sprintf("site%d.example", i), "A", reason))
	}
	_, c := start(t, adguardWith(t, adguardfake.Options{QueryLog: log, ScanCap: 10}).URL)

	got := queries(t, c, &adguardapipb.ListQueriesRequest{Client: "198.18.1.9", Mode: blocked, Limit: 5})

	if want := "site3.example site40.example site90.example site150.example site220.example"; domains(got) != want {
		t.Errorf("last 5 blocked = %s, want %s", domains(got), want)
	}
	if !readTo(got, 5, at(220)) || got.GetOldest() != at(220) {
		t.Errorf("read %d lines back to %v, cursor %q; want 5 back to line 220", got.GetLinesRead(), got.GetReached(), got.GetOldest())
	}
}

func TestATextOrTypeSearchMatchesEveryWordInTheDomainOrRule(t *testing.T) {
	_, c := start(t, adguard(t, "several-clients").URL)

	// "site1" is in five domains, "^" only in the blocked ones' rules.
	words := queries(t, c, &adguardapipb.ListQueriesRequest{Client: "198.18.1.150", Text: "  SITE1   ^ "})
	aaaa := queries(t, c, &adguardapipb.ListQueriesRequest{Client: "198.18.1.150", Type: "AAAA"})
	blockedA := queries(t, c, &adguardapipb.ListQueriesRequest{Client: "198.18.1.150", Mode: blocked, Type: "A", Text: "site1"})
	// As in matches(), a word matches without its : and - too.
	bare := queries(t, c, &adguardapipb.ListQueriesRequest{Client: "198.18.1.150", Text: "site-16"})

	if domains(bare) != "site16.example" {
		t.Errorf(`"site-16" = %s, want site16`, domains(bare))
	}
	if domains(words) != "site16.example site19.example" {
		t.Errorf(`"site1 ^" = %s, want site16 and site19`, domains(words))
	}
	if domains(aaaa) != "site10.example" {
		t.Errorf("AAAA = %s, want site10", domains(aaaa))
	}
	if domains(blockedA) != "site16.example site19.example" {
		t.Errorf("blocked A site1 = %s, want site16 and site19", domains(blockedA))
	}
	if words.GetLinesRead() != 7 || words.GetOldest() != "" {
		t.Errorf("read %d lines, cursor %q; want the client's 7 and the end of the log", words.GetLinesRead(), words.GetOldest())
	}
}

// busy is a log of 2,500 lines of 198.18.1.9's, each after one of
// 198.18.1.2's, where 198.18.1.9's line i asks for site<i>.example, but
// lines 100, 1500 and 2200 for needle<i>.example.
func busy(t *testing.T) *fakeAdGuard {
	t.Helper()
	var log []map[string]any
	for i := range 2500 {
		log = append(log, line(2*i, "198.18.1.2", "needle.other.example", "A", "NotFilteredNotFound"))
		domain := fmt.Sprintf("site%d.example", i)
		if i == 100 || i == 1500 || i == 2200 {
			domain = fmt.Sprintf("needle%d.example", i)
		}
		log = append(log, line(2*i+1, "198.18.1.9", domain, "A", "NotFilteredNotFound"))
	}
	return adguardWith(t, adguardfake.Options{QueryLog: log})
}

func TestASparseSearchStopsAt2000LinesAndResumesWhereItStopped(t *testing.T) {
	_, c := start(t, busy(t).URL)

	first := queries(t, c, &adguardapipb.ListQueriesRequest{Client: "198.18.1.9", Text: "needle"})
	next := queries(t, c, &adguardapipb.ListQueriesRequest{Client: "198.18.1.9", Text: "needle", OlderThan: first.GetOldest()})

	if domains(first) != "needle100.example needle1500.example" {
		t.Errorf("first search = %s, want needle100 and needle1500", domains(first))
	}
	// The client's 2,000th line is the log's 4,000th.
	if !readTo(first, 2000, at(3999)) || first.GetOldest() != at(3999) {
		t.Errorf("first search read %d lines back to %v, cursor %q; want 2000 back to the client's 2,000th", first.GetLinesRead(), first.GetReached(), first.GetOldest())
	}
	if domains(next) != "needle2200.example" || !readTo(next, 500, at(4999)) || next.GetOldest() != "" {
		t.Errorf("resumed = %s, %d lines back to %v, cursor %q; want needle2200, the last 500 and the end of the log", domains(next), next.GetLinesRead(), next.GetReached(), next.GetOldest())
	}
}

func TestADenseSearchStopsAt50MatchesAndResumesWithTheNext(t *testing.T) {
	_, c := start(t, busy(t).URL)

	first := queries(t, c, &adguardapipb.ListQueriesRequest{Client: "198.18.1.9", Text: "site", Limit: 500})
	next := queries(t, c, &adguardapipb.ListQueriesRequest{Client: "198.18.1.9", Text: "site", OlderThan: first.GetOldest()})

	if !strings.HasPrefix(domains(first), "site0.example site1.example ") || !strings.HasSuffix(domains(first), " site49.example") || !readTo(first, 50, at(99)) {
		t.Errorf("first search = %s from %d lines, want site0 to site49 from 50", domains(first), first.GetLinesRead())
	}
	if !strings.HasPrefix(domains(next), "site50.example ") || !strings.HasSuffix(domains(next), " site99.example") || !readTo(next, 50, at(199)) {
		t.Errorf("resumed = %s from %d lines, want site50 to site99 from 50", domains(next), next.GetLinesRead())
	}
}

func TestASearchEndsWhenAdGuardsCursorStopsMoving(t *testing.T) {
	// A broken AdGuard: the same empty page with the same cursor, always.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/control/querylog/config" {
			_, _ = w.Write([]byte(`{"anonymize_client_ip": false}`))
			return
		}
		_, _ = w.Write([]byte(`{"data": [], "oldest": "2026-10-20T12:00:00Z"}`))
	}))
	t.Cleanup(srv.Close)
	_, c := start(t, srv.URL)

	got := queries(t, c, &adguardapipb.ListQueriesRequest{Client: "198.18.1.9", Text: "needle"})

	if len(got.GetQueries()) != 0 || got.GetOldest() != "" {
		t.Errorf("search = %s, cursor %q; want nothing and no cursor", domains(got), got.GetOldest())
	}
}

func TestTheQueryLogSaysWhenAdGuardAnonymizesClientAddresses(t *testing.T) {
	_, c := start(t, adguard(t, "anonymized").URL)

	got := queries(t, c, &adguardapipb.ListQueriesRequest{Client: "198.18.1.102"})

	if !got.GetAnonymizeClientIp() || len(got.GetQueries()) != 0 {
		t.Errorf("anonymized = %v with %d queries, want true and none", got.GetAnonymizeClientIp(), len(got.GetQueries()))
	}
	_, c = start(t, adguard(t, "baseline").URL)
	if queries(t, c, &adguardapipb.ListQueriesRequest{Client: "198.18.1.102", Limit: 1}).GetAnonymizeClientIp() {
		t.Errorf("baseline anonymized = true, want false")
	}
}

func TestTheQueryLogRefusesAClientThatIsNoAddress(t *testing.T) {
	_, c := start(t, adguard(t, "baseline").URL)

	_, err := c.ListQueries(t.Context(), &adguardapipb.ListQueriesRequest{Client: "galaxy"})
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("ListQueries(galaxy) = %v, want InvalidArgument", err)
	}
}
