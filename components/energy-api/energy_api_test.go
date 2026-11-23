package energyapi_test

import (
	"math"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/google/go-cmp/cmp"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	energyapi "github.com/itsKontra/ddash/components/energy-api"
	pb "github.com/itsKontra/ddash/components/energy-api/energyapipb"
	"github.com/itsKontra/ddash/components/energy-api/energyfake"
	"github.com/itsKontra/ddash/sdk"
	"github.com/itsKontra/ddash/sdk/sdktest"
)

// now is the stopped clock of every test: a Saturday, noon in Vienna.
var now = time.Date(2026, 11, 14, 11, 0, 0, 0, time.UTC)

// glitch is when the fake's grid meter glitches: Thursday evening.
var glitch = time.Date(2026, 11, 12, 17, 50, 0, 0, time.UTC)

const vienna = "Europe/Vienna"

func serve(t *testing.T, opts energyfake.Options) (*energyfake.Server, string) {
	t.Helper()
	if opts.Now == nil {
		opts.Now = func() time.Time { return now }
	}
	fake := energyfake.New(opts)
	srv := httptest.NewServer(fake)
	t.Cleanup(srv.Close)
	return fake, srv.URL
}

func start(t *testing.T, cfg *pb.EnergyApiConfig) (*sdktest.Harness, pb.EnergyApiServiceClient) {
	t.Helper()
	h := sdktest.New(t, energyapi.DefineAt(now))
	h.MustStart(cfg)
	return h, pb.NewEnergyApiServiceClient(h.Conn)
}

// setUp is energy-api reading a fake with the given options, at the
// prices 0.2418 and 0.05786.
func setUp(t *testing.T, opts energyfake.Options) (*energyfake.Server, *sdktest.Harness, pb.EnergyApiServiceClient) {
	t.Helper()
	fake, addr := serve(t, opts)
	h, c := start(t, &pb.EnergyApiConfig{Url: addr, Token: "fake-token", BuyPrice: "0.2418", SellPrice: "0,05786"})
	return fake, h, c
}

func period(t *testing.T, c pb.EnergyApiServiceClient, span pb.Span, date string) *pb.GetPeriodResponse {
	t.Helper()
	resp, err := c.GetPeriod(t.Context(), &pb.GetPeriodRequest{Span: span, Date: date, TimeZone: vienna})
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func near(a, b float64) bool { return math.Abs(a-b) < 1e-6 }

func TestEmptyConfigIsNotSetUp(t *testing.T) {
	h, c := start(t, &pb.EnergyApiConfig{})

	flow, err := c.GetFlow(t.Context(), &pb.GetFlowRequest{})
	if err != nil || flow.GetSetUp() {
		t.Errorf("GetFlow = %v, %v; want not set up", flow, err)
	}
	p, err := c.GetPeriod(t.Context(), &pb.GetPeriodRequest{Span: pb.Span_SPAN_DAY})
	if err != nil || p.GetSetUp() {
		t.Errorf("GetPeriod = %v, %v; want not set up", p, err)
	}
	if got := h.Health(); got.State != sdk.OK || got.Message != "not set up" {
		t.Errorf("health = %v, want ok, not set up", got)
	}
}

func TestConfigIsChecked(t *testing.T) {
	for _, tc := range []struct {
		name string
		cfg  *pb.EnergyApiConfig
		want []string
	}{
		{"an address without a token", &pb.EnergyApiConfig{Url: "https://influxdb.home.example"}, []string{"token: is required with an address"}},
		{"an address that isn't http", &pb.EnergyApiConfig{Url: "influxdb:8086", Token: "t"}, []string{"url: must be an http or https URL"}},
		{"an empty bucket", &pb.EnergyApiConfig{Url: "https://i.example", Token: "t", Bucket: new(" ")}, []string{"bucket: is required"}},
		{"a price that isn't a number", &pb.EnergyApiConfig{Url: "https://i.example", Token: "t", BuyPrice: "24 ct"}, []string{"buy_price: must be € per kWh, e.g. 0.2418"}},
		{"a feed-in tariff without a price", &pb.EnergyApiConfig{SellPrice: "0.05"}, []string{"buy_price: is required with a feed-in tariff"}},
		{"a battery capacity that isn't kWh", &pb.EnergyApiConfig{Url: "https://i.example", Token: "t", BatteryCapacity: "10 kWh"}, []string{"battery_capacity: must be kWh, e.g. 10"}},
		{"a battery without capacity", &pb.EnergyApiConfig{Url: "https://i.example", Token: "t", BatteryCapacity: "0"}, []string{"battery_capacity: must be kWh, e.g. 10"}},
		{"a battery reserve that isn't a percentage", &pb.EnergyApiConfig{Url: "https://i.example", Token: "t", BatteryReserve: "15 %"}, []string{"battery_reserve: must be a percentage from 0 to 99, e.g. 15"}},
		{"a battery reserve of 100 percent", &pb.EnergyApiConfig{Url: "https://i.example", Token: "t", BatteryReserve: "100"}, []string{"battery_reserve: must be a percentage from 0 to 99, e.g. 15"}},
		{"a negative battery reserve", &pb.EnergyApiConfig{Url: "https://i.example", Token: "t", BatteryReserve: "-5"}, []string{"battery_reserve: must be a percentage from 0 to 99, e.g. 15"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := sdktest.New(t, energyapi.DefineAt(now))

			err := h.Start(tc.cfg)

			var got []string
			for _, fe := range sdk.FieldErrorsOf(err) {
				got = append(got, fe.Path+": "+fe.Message)
			}
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("field errors (-want +got):\n%s", diff)
			}
		})
	}
}

func TestFlowIsTheLatestReadings(t *testing.T) {
	_, h, c := setUp(t, energyfake.Options{})

	got, err := c.GetFlow(t.Context(), &pb.GetFlowRequest{})
	if err != nil {
		t.Fatal(err)
	}

	p := got.GetPower()
	if !got.GetSetUp() || got.GetAt().AsTime().Before(now.Add(-energyfake.Step)) {
		t.Fatalf("flow = %v, want set up with a reading of the last 5 minutes", got)
	}
	if p.Solar == nil || p.Grid == nil || p.Battery == nil || p.House == nil || p.HeatPump == nil || p.Loads == nil {
		t.Fatalf("power = %v, want every reading", p)
	}
	// Noon in November: the sun covers the house and charges the battery.
	if p.GetSolar() <= 0 || p.GetBattery() <= 0 {
		t.Errorf("solar, battery = %v, %v; want both positive at noon", p.GetSolar(), p.GetBattery())
	}
	if want := p.GetSolar() + p.GetGrid() - p.GetBattery(); !near(p.GetConsumption(), want) {
		t.Errorf("consumption = %v, want solar + grid - battery = %v", p.GetConsumption(), want)
	}
	if !near(p.GetLoads(), p.GetHouse()+p.GetHeatPump()) || p.GetConsumption() < p.GetLoads() {
		t.Errorf("loads = %v, consumption = %v; want loads = house + heat pump, under consumption", p.GetLoads(), p.GetConsumption())
	}
	if soc := got.GetBattery().GetSocPct(); soc < energyfake.Reserve || soc > 100 {
		t.Errorf("state of charge = %v, want the fake's reserve %v to 100", soc, energyfake.Reserve)
	}
	if got := h.Health(); got.State != sdk.OK {
		t.Errorf("health = %v, want ok", got)
	}
}

func TestFlowCarriesTheBatterysCapacityOnlyWhenConfigured(t *testing.T) {
	_, addr := serve(t, energyfake.Options{})
	for _, tc := range []struct {
		capacity string
		want     *float64
	}{{"", nil}, {"13,5", new(13.5)}} {
		_, c := start(t, &pb.EnergyApiConfig{Url: addr, Token: "fake-token", BatteryCapacity: tc.capacity})

		got, err := c.GetFlow(t.Context(), &pb.GetFlowRequest{})
		if err != nil {
			t.Fatal(err)
		}

		if diff := cmp.Diff(tc.want, got.GetBattery().CapacityKwh); diff != "" {
			t.Errorf("capacity %q: capacity_kwh (-want +got):\n%s", tc.capacity, diff)
		}
	}
}

func TestFlowCarriesTheBatterysReserveOnlyWhenConfigured(t *testing.T) {
	_, addr := serve(t, energyfake.Options{})
	for _, tc := range []struct {
		reserve string
		want    *float64
	}{{"", nil}, {" ", nil}, {"0", new(0.0)}, {"15", new(15.0)}, {"12,5", new(12.5)}} {
		_, c := start(t, &pb.EnergyApiConfig{Url: addr, Token: "fake-token", BatteryReserve: tc.reserve})

		got, err := c.GetFlow(t.Context(), &pb.GetFlowRequest{})
		if err != nil {
			t.Fatal(err)
		}

		if diff := cmp.Diff(tc.want, got.GetBattery().ReservePct); diff != "" {
			t.Errorf("reserve %q: reserve_pct (-want +got):\n%s", tc.reserve, diff)
		}
	}
}

func TestFlowWithoutRecentReadingsIsDegraded(t *testing.T) {
	// The fake's clock stopped a day ago: nothing from the last 5 minutes.
	_, h, c := setUp(t, energyfake.Options{Now: func() time.Time { return now.AddDate(0, 0, -1) }})

	got, err := c.GetFlow(t.Context(), &pb.GetFlowRequest{})
	if err != nil || got.GetAt() != nil || got.GetPower() != nil {
		t.Errorf("GetFlow = %v, %v; want set up without readings", got, err)
	}
	if got := h.Health(); got.State != sdk.Degraded || got.Message != "no reading in the last 5 minutes" {
		t.Errorf("health = %v, want degraded", got)
	}
}

func TestARefusedTokenIsPermissionDenied(t *testing.T) {
	_, addr := serve(t, energyfake.Options{})
	h, c := start(t, &pb.EnergyApiConfig{Url: addr, Token: "wrong-secret"})

	_, err := c.GetFlow(t.Context(), &pb.GetFlowRequest{})

	if status.Code(err) != codes.PermissionDenied || !strings.Contains(err.Error(), "401") {
		t.Errorf("GetFlow = %v, want PERMISSION_DENIED naming the 401", err)
	}
	got := h.Health()
	if got.State != sdk.Down || strings.Contains(got.Message, "wrong-secret") {
		t.Errorf("health = %v, want down, never naming the token", got)
	}
}

func TestAnUnknownBucketSaysSo(t *testing.T) {
	_, addr := serve(t, energyfake.Options{})
	_, c := start(t, &pb.EnergyApiConfig{Url: addr, Token: "fake-token", Bucket: new("Solar")})

	_, err := c.GetFlow(t.Context(), &pb.GetFlowRequest{})

	if status.Code(err) != codes.FailedPrecondition || status.Convert(err).Message() != "bucket Solar doesn't exist or the token can't read it" {
		t.Errorf("GetFlow = %v, want FAILED_PRECONDITION naming the bucket", err)
	}
}

func TestADayIsTwentyFourHours(t *testing.T) {
	_, _, c := setUp(t, energyfake.Options{})

	got := period(t, c, pb.Span_SPAN_DAY, "2026-11-10")

	loc, _ := time.LoadLocation(vienna)
	if from := got.GetFrom().AsTime().In(loc); from != time.Date(2026, 11, 10, 0, 0, 0, 0, loc) {
		t.Errorf("from = %v, want Tuesday's midnight in Vienna", from)
	}
	if len(got.GetBuckets()) != 24 || len(got.GetSamples()) != 288 {
		t.Errorf("%d buckets, %d samples; want 24 hours, a sample every 5 minutes", len(got.GetBuckets()), len(got.GetSamples()))
	}
	if got.GetPrevious() != "2026-11-09" || got.GetNext() != "2026-11-11" || got.GetFirstDay() != "2025-10-10" {
		t.Errorf("previous, next, first day = %s, %s, %s", got.GetPrevious(), got.GetNext(), got.GetFirstDay())
	}
}

// The totals add up: the buckets to the period, and each bucket's
// consumption to what solar, grid and battery supplied it.
func TestTotalsAddUp(t *testing.T) {
	_, _, c := setUp(t, energyfake.Options{})

	got := period(t, c, pb.Span_SPAN_DAY, "2026-11-10")

	var solar, consumption, cost float64
	for _, b := range got.GetBuckets() {
		x := b.GetTotals()
		solar += x.GetSolar()
		consumption += x.GetConsumption()
		cost += x.GetCost()
		if want := x.GetSolar() + x.GetGridImport() - x.GetGridExport() - x.GetBatteryCharged() + x.GetBatteryDischarged(); !near(x.GetConsumption(), max(0, want)) {
			t.Errorf("%v: consumption %v, want %v", b.GetFrom().AsTime(), x.GetConsumption(), want)
		}
	}
	x := got.GetTotals()
	if !near(x.GetSolar(), solar) || !near(x.GetConsumption(), consumption) || !near(x.GetCost(), cost) {
		t.Errorf("totals %v, want the buckets' sums", x)
	}
	if x.GetSolar() < 5 || x.GetHouse() < 5 || x.GetHeatPump() < 5 {
		t.Errorf("totals %v, want a November day's solar, house and heat pump", x)
	}
	if !near(x.GetCost(), x.GetGridImport()*0.2418) || !near(x.GetRevenue(), x.GetGridExport()*0.05786) {
		t.Errorf("cost, revenue = %v, %v; want import × 0.2418, export × 0.05786", x.GetCost(), x.GetRevenue())
	}
	if want := (x.GetConsumption()-x.GetGridImport())*0.2418 + x.GetRevenue(); math.Abs(x.GetSavings()-want) > 0.01 {
		t.Errorf("savings = %v, want %v", x.GetSavings(), want)
	}
	if p := got.GetPrices(); p.GetBuy() != 0.2418 || p.GetSell() != 0.05786 {
		t.Errorf("prices = %v", p)
	}
}

// The day of the glitch counts the same as without it, and its curve
// leaves the sentinel out.
func TestTheGlitchIsLeftOut(t *testing.T) {
	_, _, c := setUp(t, energyfake.Options{Glitch: glitch})
	_, _, clean := setUp(t, energyfake.Options{Glitch: glitch.AddDate(-2, 0, 0)})

	got := period(t, c, pb.Span_SPAN_DAY, "2026-11-12")
	want := period(t, clean, pb.Span_SPAN_DAY, "2026-11-12")

	g, w := got.GetTotals(), want.GetTotals()
	if !near(g.GetGridImport(), w.GetGridImport()) || !near(g.GetGridExport(), w.GetGridExport()) || g.GetGridImport() > 100 {
		t.Errorf("import, export = %v, %v; want %v, %v as without the glitch", g.GetGridImport(), g.GetGridExport(), w.GetGridImport(), w.GetGridExport())
	}
	for _, s := range got.GetSamples() {
		at := s.GetAt().AsTime()
		inGlitch := !at.Before(glitch) && at.Before(glitch.Add(20*time.Minute))
		if inGlitch != (s.GetPower().Grid == nil) {
			t.Errorf("%v: grid %v; want it unset exactly while glitching", at, s.GetPower().Grid)
		}
	}
	// A month's windows are judged by their mean: the glitch's hour has none.
	month := period(t, c, pb.Span_SPAN_MONTH, "2026-11-12")
	if imp := month.GetTotals().GetGridImport(); imp > 2000 {
		t.Errorf("November's import = %v, want the glitch left out", imp)
	}
}

func TestBucketsFollowTheCalendar(t *testing.T) {
	_, _, c := setUp(t, energyfake.Options{})
	loc, _ := time.LoadLocation(vienna)

	for _, tc := range []struct {
		name    string
		span    pb.Span
		date    string
		from    time.Time
		buckets int
		samples bool
	}{
		{"a day of summer time's start has 23 hours", pb.Span_SPAN_DAY, "2026-03-29", time.Date(2026, 3, 29, 0, 0, 0, 0, loc), 23, true},
		{"today has the hours begun", pb.Span_SPAN_DAY, "", time.Date(2026, 11, 14, 0, 0, 0, 0, loc), 12, true},
		{"a week starts on Monday", pb.Span_SPAN_WEEK, "2026-11-04", time.Date(2026, 11, 2, 0, 0, 0, 0, loc), 7, true},
		{"a month has its days", pb.Span_SPAN_MONTH, "2026-02-14", time.Date(2026, 2, 1, 0, 0, 0, 0, loc), 28, false},
		{"a year has its months", pb.Span_SPAN_YEAR, "2025-06-01", time.Date(2025, 1, 1, 0, 0, 0, 0, loc), 12, false},
		{"this year has the months begun", pb.Span_SPAN_YEAR, "", time.Date(2026, 1, 1, 0, 0, 0, 0, loc), 11, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := period(t, c, tc.span, tc.date)

			if from := got.GetFrom().AsTime(); !from.Equal(tc.from) {
				t.Errorf("from = %v, want %v", from.In(loc), tc.from)
			}
			if n := len(got.GetBuckets()); n != tc.buckets {
				t.Errorf("%d buckets, want %d", n, tc.buckets)
			}
			if (len(got.GetSamples()) > 0) != tc.samples {
				t.Errorf("%d samples, want some: %v", len(got.GetSamples()), tc.samples)
			}
		})
	}
}

func TestAPeriodThatIsOverIsReadOnce(t *testing.T) {
	fake, _, c := setUp(t, energyfake.Options{})
	period(t, c, pb.Span_SPAN_DAY, "2026-11-10")
	n := len(fake.Queries())

	period(t, c, pb.Span_SPAN_DAY, "2026-11-10")

	if again := len(fake.Queries()) - n; again != 0 {
		t.Errorf("read again with %d queries, want none", again)
	}
}

// The Energy Page and its Widget ask for the same period at the same
// moment.
func TestCallersArrivingTogetherForAnUncachedPeriodShareOneRead(t *testing.T) {
	fake, _, c := setUp(t, energyfake.Options{})
	period(t, c, pb.Span_SPAN_DAY, "2026-11-09")
	one := len(fake.Queries()) // what reading one period takes
	slow, _, c := setUp(t, energyfake.Options{QueryDelay: 200 * time.Millisecond})
	before := len(slow.Queries())

	var wg sync.WaitGroup
	answers := make([]*pb.GetPeriodResponse, 5)
	for i := range answers {
		wg.Go(func() {
			resp, err := c.GetPeriod(t.Context(), &pb.GetPeriodRequest{Span: pb.Span_SPAN_DAY, Date: "2026-11-09", TimeZone: vienna})
			if err != nil {
				t.Error(err)
			}
			answers[i] = resp
		})
	}
	wg.Wait()

	if got := len(slow.Queries()) - before; got != one {
		t.Errorf("%d queries for five callers of one period, want the %d of one read", got, one)
	}
	for i, a := range answers {
		if !a.GetSetUp() || !proto.Equal(a, answers[0]) {
			t.Errorf("caller %d got %v, want the shared answer", i, a)
		}
	}
}

func TestWithoutPricesThereAreNoCosts(t *testing.T) {
	_, addr := serve(t, energyfake.Options{})
	_, c := start(t, &pb.EnergyApiConfig{Url: addr, Token: "fake-token"})

	got := period(t, c, pb.Span_SPAN_DAY, "2026-11-10")

	if got.GetPrices() != nil || got.GetTotals().GetCost() != 0 || got.GetTotals().GetSavings() != 0 {
		t.Errorf("prices %v, totals %v; want no prices and no money", got.GetPrices(), got.GetTotals())
	}
}

func TestABadRequestIsInvalidArgument(t *testing.T) {
	_, _, c := setUp(t, energyfake.Options{})

	for _, req := range []*pb.GetPeriodRequest{
		{Span: pb.Span_SPAN_UNSPECIFIED},
		{Span: pb.Span_SPAN_DAY, Date: "14.11.2026"},
		{Span: pb.Span_SPAN_DAY, TimeZone: "Europe/Atlantis"},
	} {
		if _, err := c.GetPeriod(t.Context(), req); status.Code(err) != codes.InvalidArgument {
			t.Errorf("GetPeriod(%v) = %v, want INVALID_ARGUMENT", req, err)
		}
	}
}
