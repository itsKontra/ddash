package adguardapi_test

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/testing/protocmp"
	"google.golang.org/protobuf/types/known/durationpb"

	adguardapi "github.com/itsKontra/ddash/components/adguard-api"
	"github.com/itsKontra/ddash/components/adguard-api/adguardapipb"
	"github.com/itsKontra/ddash/components/adguard-api/adguardfake"
	"github.com/itsKontra/ddash/sdk"
	"github.com/itsKontra/ddash/sdk/sdktest"
)

const (
	username = "ddash"
	password = "correct horse battery staple"
)

// fakeAdGuard is AdGuard Home's API serving a scenario: the stateful fake
// for persistent clients and the query log, fixtures for the rest. Like
// AdGuard it refuses any other Basic auth credentials.
type fakeAdGuard struct {
	*httptest.Server
	*adguardfake.AdGuard
}

func adguard(t *testing.T, scenario string) *fakeAdGuard {
	fake, err := adguardfake.New("testdata/"+scenario, adguardfake.Options{
		Miss:     func(request string, err error) { t.Errorf("fixture: %v", err) },
		Username: username,
		Password: password,
	})
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeAdGuard{AdGuard: fake, Server: httptest.NewServer(fake)}
	t.Cleanup(f.Close)
	return f
}

func startAs(t *testing.T, url, pass string) (*sdktest.Harness, adguardapipb.AdguardApiServiceClient) {
	h := sdktest.New(t, adguardapi.Definition())
	h.MustStart(&adguardapipb.AdguardApiConfig{Url: url, Username: username, Password: pass})
	return h, adguardapipb.NewAdguardApiServiceClient(h.Conn)
}

func start(t *testing.T, url string) (*sdktest.Harness, adguardapipb.AdguardApiServiceClient) {
	return startAs(t, url, password)
}

func getStatus(t *testing.T, c adguardapipb.AdguardApiServiceClient) *adguardapipb.GetStatusResponse {
	t.Helper()
	got, err := c.GetStatus(t.Context(), &adguardapipb.GetStatusRequest{})
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestStatusSaysTheDNSServerRunsWithProtectionOn(t *testing.T) {
	_, c := start(t, adguard(t, "baseline").URL)

	want := &adguardapipb.GetStatusResponse{Running: true, ProtectionEnabled: true, Version: "v0.107.79"}
	if diff := cmp.Diff(want, getStatus(t, c), protocmp.Transform()); diff != "" {
		t.Errorf("GetStatus (-want +got):\n%s", diff)
	}
}

func TestStatusSaysWhenProtectionIsOffOrPausedAndForHowLong(t *testing.T) {
	_, c := start(t, adguard(t, "protection-off").URL)
	if got := getStatus(t, c); got.GetProtectionEnabled() || got.GetProtectionPausedFor() != nil {
		t.Errorf("protection off = %v, want off indefinitely", got)
	}

	_, c = start(t, adguard(t, "paused").URL)
	want := &adguardapipb.GetStatusResponse{Running: true, Version: "v0.107.79", ProtectionPausedFor: durationpb.New(10 * time.Minute)}
	if diff := cmp.Diff(want, getStatus(t, c), protocmp.Transform()); diff != "" {
		t.Errorf("GetStatus while paused (-want +got):\n%s", diff)
	}
}

func TestStatusSaysWhenTheDNSServerIsNotRunning(t *testing.T) {
	h, c := start(t, adguard(t, "not-running").URL)

	if got := getStatus(t, c); got.GetRunning() {
		t.Errorf("running = true, want false")
	}
	if got := h.Health(); got.State != sdk.OK {
		t.Errorf("health = %v, want ok: the API answers; the DNS server is the Widget's news", got)
	}
}

func TestAWrongPasswordIsDownAndNotAskedAgainUntilReconfigured(t *testing.T) {
	srv := adguard(t, "baseline")
	h, c := startAs(t, srv.URL, "wrong")

	_, err := c.GetStatus(t.Context(), &adguardapipb.GetStatusRequest{})
	if status.Code(err) != codes.Unavailable || !strings.Contains(err.Error(), "refused the username or password") {
		t.Errorf("GetStatus error = %v, want Unavailable: refused credentials", err)
	}
	if got := h.Health(); got.State != sdk.Down || !strings.Contains(got.Message, "refused the username or password") {
		t.Errorf("health = %v, want down naming the refused credentials", got)
	}
	_, _ = c.GetStats(t.Context(), &adguardapipb.GetStatsRequest{})
	_, _ = c.ListClients(t.Context(), &adguardapipb.ListClientsRequest{})

	// AdGuard locks out an address after repeated failures: one is enough.
	if got := srv.Log(); len(got) != 1 {
		t.Errorf("requests after a refusal = %v, want only the first", got)
	}
}

func TestAnUnreachableAdGuardIsDown(t *testing.T) {
	srv := adguard(t, "baseline")
	srv.Close()
	h, c := start(t, srv.URL)

	_, err := c.GetStatus(t.Context(), &adguardapipb.GetStatusRequest{})
	if status.Code(err) != codes.Unavailable || !strings.Contains(err.Error(), "unreachable") {
		t.Errorf("GetStatus error = %v, want Unavailable: unreachable", err)
	}
	if got := h.Health(); got.State != sdk.Down {
		t.Errorf("health = %v, want down", got)
	}
}

func TestStartRefusesAURLThatIsNoWebAddress(t *testing.T) {
	h := sdktest.New(t, adguardapi.Definition())
	err := h.Start(&adguardapipb.AdguardApiConfig{Url: "198.18.1.3", Username: username, Password: password})
	if fe := sdk.FieldErrorsOf(err); len(fe) != 1 || fe[0].Path != "url" {
		t.Errorf("Start = %v, want a field error on url", err)
	}
}

func TestStatsAreTheLast24HoursTotalsSeriesAndTopLists(t *testing.T) {
	_, c := start(t, adguard(t, "baseline").URL)

	got, err := c.GetStats(t.Context(), &adguardapipb.GetStatsRequest{})
	if err != nil {
		t.Fatal(err)
	}

	if got.GetDnsQueries() != 18432 || got.GetBlockedFiltering() != 2765 || got.GetReplacedSafebrowsing() != 3 || got.GetReplacedSafesearch() != 12 || got.GetReplacedParental() != 0 {
		t.Errorf("totals = %v", got)
	}
	if got.GetAvgProcessingMs() != 18.4 {
		t.Errorf("avg processing = %v ms, want 18.4", got.GetAvgProcessingMs())
	}
	if got.GetTimeUnit() != "hours" || len(got.GetDnsQueriesSeries()) != 24 || got.GetDnsQueriesSeries()[0] != 612 || got.GetBlockedFilteringSeries()[23] != 105 {
		t.Errorf("series = %s %v %v, want 24 hourly buckets", got.GetTimeUnit(), got.GetDnsQueriesSeries(), got.GetBlockedFilteringSeries())
	}
	wantBlocked := []*adguardapipb.TopEntry{{Name: "site2.example", Count: 610}, {Name: "site5.example", Count: 402}, {Name: "site9.example", Count: 133}}
	if diff := cmp.Diff(wantBlocked, got.GetTopBlockedDomains(), protocmp.Transform()); diff != "" {
		t.Errorf("top blocked (-want +got):\n%s", diff)
	}
	if first := got.GetTopClients()[0]; first.GetName() != "198.18.1.150" || first.GetCount() != 6120 {
		t.Errorf("top client = %v", first)
	}
}
