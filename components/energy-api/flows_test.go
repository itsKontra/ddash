package energyapi_test

import (
	"database/sql"
	"math"
	"regexp"
	"strings"
	"testing"
	"time"

	energyapi "github.com/itsKontra/ddash/components/energy-api"
	pb "github.com/itsKontra/ddash/components/energy-api/energyapipb"
	"github.com/itsKontra/ddash/components/energy-api/energyfake"
	"github.com/itsKontra/ddash/sdk/sdktest"
)

// gridCharging is a day of the fake that charges its battery from the grid
// from 13:00 to 15:00, after the sun charged it in the morning, and
// discharges it in the evening.
var gridCharging = energyfake.Options{GridCharge: [2]float64{13, 15}}

// within tells whether a is b to within tol, relative.
func within(a, b, tol float64) bool {
	return math.Abs(a-b) <= tol*math.Max(math.Abs(a), math.Abs(b))+1e-6
}

// flowsAddUp fails unless each source's flows add up to its counter, the
// flows into each destination to its counter, and those into the use to
// the consumption.
func flowsAddUp(t *testing.T, name string, x *pb.Totals) {
	t.Helper()
	f := x.GetFlows()
	for _, c := range []struct {
		what      string
		got, want float64
	}{
		{"solar", f.GetSolarUsed() + f.GetSolarBattery() + f.GetSolarExport(), x.GetSolar()},
		{"battery", f.GetBatteryUsed() + f.GetBatteryExport(), x.GetBatteryDischarged()},
		{"grid", f.GetGridUsed() + f.GetGridBattery(), x.GetGridImport()},
		{"export", f.GetSolarExport() + f.GetBatteryExport(), x.GetGridExport()},
		{"into the battery", f.GetSolarBattery() + f.GetGridBattery(), x.GetBatteryCharged()},
		{"used", f.GetSolarUsed() + f.GetBatteryUsed() + f.GetGridUsed(), x.GetConsumption()},
	} {
		if math.Abs(c.got-c.want) > 1e-6 {
			t.Errorf("%s: %s's flows add up to %v, want its counter %v", name, c.what, c.got, c.want)
		}
	}
}

// bucketAt is the bucket starting at h o'clock in Vienna.
func bucketAt(t *testing.T, p *pb.GetPeriodResponse, h int) *pb.Totals {
	t.Helper()
	loc, _ := time.LoadLocation(vienna)
	for _, b := range p.GetBuckets() {
		if b.GetFrom().AsTime().In(loc).Hour() == h {
			return b.GetTotals()
		}
	}
	t.Fatalf("no bucket at %d:00", h)
	return nil
}

// The issue's day: the sun charges the battery in the morning, the grid in
// the afternoon, and the battery serves the evening. A split of the day's
// totals cancels these out; one every 5 minutes keeps each.
func TestFlowsAreAttributedEveryFiveMinutes(t *testing.T) {
	_, _, c := setUp(t, gridCharging)

	got := period(t, c, pb.Span_SPAN_DAY, "2026-11-10")

	x := got.GetTotals()
	f := x.GetFlows()
	if f.GetSolarUsed() <= 1 || f.GetSolarBattery() <= 0.5 || f.GetGridBattery() <= 3 || f.GetBatteryUsed() <= 3 {
		t.Errorf("flows %v; want solar used and charging, the grid charging and the battery serving the use", f)
	}
	if f.GetEstimated() {
		t.Errorf("estimated, want split by the VE.Bus counters")
	}
	flowsAddUp(t, "the day", x)
	for _, b := range got.GetBuckets() {
		flowsAddUp(t, b.GetFrom().AsTime().String(), b.GetTotals())
	}
	if m := bucketAt(t, got, 11).GetFlows(); m.GetSolarBattery() <= 0 || m.GetSolarUsed() <= 0 || m.GetGridBattery() > 0 {
		t.Errorf("11:00: %v; want the sun serving the use and charging, not the grid", m)
	}
	if a := bucketAt(t, got, 13).GetFlows(); a.GetGridBattery() <= 2 || a.GetSolarUsed() <= 0 {
		t.Errorf("13:00: %v; want the grid charging, the sun still serving the use", a)
	}
	if e := bucketAt(t, got, 19).GetFlows(); e.GetBatteryUsed() <= 0.5 || e.GetSolarUsed() > 0 {
		t.Errorf("19:00: %v; want the battery serving the use", e)
	}
}

// What the house used of its own is what saved buying it, less the grid's
// charge into the battery: that was bought, and counts once.
func TestSavingsAreTheOwnUseLessTheGridsCharge(t *testing.T) {
	_, _, c := setUp(t, gridCharging)

	x := period(t, c, pb.Span_SPAN_DAY, "2026-11-10").GetTotals()

	f := x.GetFlows()
	if want := (f.GetSolarUsed()+f.GetBatteryUsed()-f.GetGridBattery())*0.2418 + x.GetRevenue(); !near(x.GetSavings(), want) {
		t.Errorf("savings = %v, want (solar used + battery used − grid charge) × 0.2418 + revenue = %v", x.GetSavings(), want)
	}
	// Grid charging cost what the use didn't buy: no more saved than
	// buying everything would have cost beyond the bill.
	if want := (x.GetConsumption()-x.GetGridImport())*0.2418 + x.GetRevenue(); math.Abs(x.GetSavings()-want) > 0.01 {
		t.Errorf("savings = %v, want what buying all the use would have cost less the bill, %v", x.GetSavings(), want)
	}
}

// The VE.Bus counters split the battery's losses and the Victron's AC-out
// loads out of the use.
func TestLossesAndTheACOutComeFromTheVEBusCounters(t *testing.T) {
	_, _, c := setUp(t, gridCharging)

	x := period(t, c, pb.Span_SPAN_DAY, "2026-11-10").GetTotals()

	f := x.GetFlows()
	// The fake's charger keeps 92 % and its inverter delivers 95 %.
	// The battery counts in 0.01 kWh steps, the VE.Bus counters finer.
	if want := x.GetBatteryCharged() * (1/0.92 - 1); !within(f.GetChargeLoss(), want, 0.1) {
		t.Errorf("charge loss = %v, want ~%v", f.GetChargeLoss(), want)
	}
	if want := x.GetBatteryDischarged() * 0.05; !within(f.GetDischargeLoss(), want, 0.1) {
		t.Errorf("discharge loss = %v, want ~%v", f.GetDischargeLoss(), want)
	}
	// The house is the Victron's AC-out load.
	if !within(f.GetAcOut(), x.GetHouse(), 0.02) {
		t.Errorf("AC out = %v, want the house's %v", f.GetAcOut(), x.GetHouse())
	}
}

// Before the VE.Bus counters began (a history backfilled without them),
// the split comes from the power readings, close to the counters', and
// says it is estimated.
func TestWithoutTheVEBusCountersTheSplitIsEstimated(t *testing.T) {
	from := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	backfilled := gridCharging
	backfilled.VEBusFrom = from
	_, _, c := setUp(t, backfilled)
	_, _, exact := setUp(t, gridCharging)

	for _, tc := range []struct {
		date      string
		estimated bool
	}{{"2026-11-09", true}, {"2026-11-10", true}, {"2026-11-11", false}} {
		got := period(t, c, pb.Span_SPAN_DAY, tc.date).GetTotals()
		want := period(t, exact, pb.Span_SPAN_DAY, tc.date).GetTotals()

		f, w := got.GetFlows(), want.GetFlows()
		if f.GetEstimated() != tc.estimated {
			t.Errorf("%s: estimated %v, want %v", tc.date, f.GetEstimated(), tc.estimated)
		}
		flowsAddUp(t, tc.date, got)
		for _, c := range []struct {
			what      string
			got, want float64
		}{
			{"solar used", f.GetSolarUsed(), w.GetSolarUsed()},
			{"grid to battery", f.GetGridBattery(), w.GetGridBattery()},
			{"battery used", f.GetBatteryUsed(), w.GetBatteryUsed()},
		} {
			if !within(c.got, c.want, 0.1) {
				t.Errorf("%s: %s = %v, want close to the counters' %v", tc.date, c.what, c.got, c.want)
			}
		}
	}
	// The losses count only where the counters do.
	if f := period(t, c, pb.Span_SPAN_DAY, "2026-11-09").GetTotals().GetFlows(); f.GetChargeLoss() != 0 || f.GetAcOut() != 0 {
		t.Errorf("a day before the counters: losses %v, %v and AC out %v; want none", f.GetChargeLoss(), f.GetDischargeLoss(), f.GetAcOut())
	}
}

// vebusQueries are the queries that read the VE.Bus counters, which only
// attributing hours does.
func vebusQueries(fake *energyfake.Server) []string {
	var out []string
	for _, q := range fake.Queries() {
		if strings.Contains(q, "acin1_to_inverter_kWh") {
			out = append(out, q)
		}
	}
	return out
}

// restart is energy-api started again on the DB of h, with cfg.
func restart(t *testing.T, h *sdktest.Harness, cfg *pb.EnergyApiConfig) pb.EnergyApiServiceClient {
	t.Helper()
	h.Stop()
	again := sdktest.New(t, energyapi.DefineAt(now), sdktest.WithDB(h.DBPath))
	again.MustStart(cfg)
	return pb.NewEnergyApiServiceClient(again.Conn)
}

func TestAttributedHoursOutliveARestart(t *testing.T) {
	fake, addr := serve(t, gridCharging)
	cfg := &pb.EnergyApiConfig{Url: addr, Token: "fake-token", BuyPrice: "0.2418"}
	h, c := start(t, cfg)
	want := period(t, c, pb.Span_SPAN_MONTH, "2026-10-01")
	n := len(vebusQueries(fake))

	// A new token reads the same bucket.
	cfg.Token = "fake-token"
	c = restart(t, h, cfg)
	got := period(t, c, pb.Span_SPAN_MONTH, "2026-10-01")

	if again := len(vebusQueries(fake)) - n; again != 0 {
		t.Errorf("attributed again with %d queries, want the cached hours", again)
	}
	if !near(got.GetTotals().GetFlows().GetSolarUsed(), want.GetTotals().GetFlows().GetSolarUsed()) || !near(got.GetTotals().GetSolar(), want.GetTotals().GetSolar()) {
		t.Errorf("totals %v, want the first read's %v", got.GetTotals(), want.GetTotals())
	}
}

func TestAnotherInfluxDBClearsTheCache(t *testing.T) {
	_, addr := serve(t, energyfake.Options{})
	other, otherAddr := serve(t, gridCharging)
	h, c := start(t, &pb.EnergyApiConfig{Url: addr, Token: "fake-token"})
	period(t, c, pb.Span_SPAN_DAY, "2026-11-10")

	c = restart(t, h, &pb.EnergyApiConfig{Url: otherAddr, Token: "fake-token"})
	got := period(t, c, pb.Span_SPAN_DAY, "2026-11-10")

	if len(vebusQueries(other)) == 0 || got.GetTotals().GetFlows().GetGridBattery() < 3 {
		t.Errorf("flows %v; want the other InfluxDB's grid charging", got.GetTotals().GetFlows())
	}
}

func TestHoursOfAnotherAlgorithmAreAttributedAgain(t *testing.T) {
	fake, addr := serve(t, energyfake.Options{})
	cfg := &pb.EnergyApiConfig{Url: addr, Token: "fake-token"}
	h, c := start(t, cfg)
	period(t, c, pb.Span_SPAN_DAY, "2026-11-10")
	db, err := sql.Open("sqlite", h.DBPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec("UPDATE hour SET algo = ?", energyapi.AlgoVersion-1); err != nil {
		t.Fatal(err)
	}
	n := len(vebusQueries(fake))

	c = restart(t, h, cfg)
	period(t, c, pb.Span_SPAN_DAY, "2026-11-10")

	if len(vebusQueries(fake)) == n {
		t.Error("served the old algorithm's hours, want them attributed again")
	}
}

var reRange = regexp.MustCompile(`range\(start: (\S+), stop: (\S+)\)`)

// A first Year read attributes its hours a week at a time, each read as
// InfluxDB reduces it in its storage engine.
func TestAYearIsAttributedInBoundedChunks(t *testing.T) {
	fake, _, c := setUp(t, energyfake.Options{})

	got := period(t, c, pb.Span_SPAN_YEAR, "2026-06-01")

	qs := vebusQueries(fake)
	if len(qs) < 2*45 {
		t.Errorf("%d queries for the VE.Bus counters, want a pair for each week of the year", len(qs))
	}
	for _, q := range qs {
		if strings.Contains(q, "toFloat") || strings.Contains(q, "r._value") {
			t.Errorf("query filters values, which InfluxDB can't push down:\n%s", q)
		}
		m := reRange.FindStringSubmatch(q)
		a, _ := time.Parse(time.RFC3339, m[1])
		b, _ := time.Parse(time.RFC3339, m[2])
		if strings.Contains(q, "aggregateWindow") && b.Sub(a) > 7*24*time.Hour {
			t.Errorf("a query over %v, want at most a week", b.Sub(a))
		}
	}
	flowsAddUp(t, "the year", got.GetTotals())
}

// The current hour is attributed up to now on every read and never cached.
func TestTheCurrentHourIsLive(t *testing.T) {
	half := now.Add(30 * time.Minute)
	_, addr := serve(t, energyfake.Options{Now: func() time.Time { return half }})
	h := sdktest.New(t, energyapi.DefineAt(half))
	h.MustStart(&pb.EnergyApiConfig{Url: addr, Token: "fake-token"})
	c := pb.NewEnergyApiServiceClient(h.Conn)

	got := period(t, c, pb.Span_SPAN_DAY, "")

	if noon := bucketAt(t, got, 12); noon.GetSolar() <= 0 || noon.GetFlows().GetSolarUsed() <= 0 {
		t.Errorf("12:00 so far: %v, want its half hour of sun", noon)
	}
	db, err := sql.Open("sqlite", h.DBPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var n int
	if err := db.QueryRow("SELECT count(*) FROM hour WHERE at >= ?", now.Unix()).Scan(&n); err != nil || n != 0 {
		t.Errorf("%d cached hours from 12:00 (%v), want none", n, err)
	}
	if err := db.QueryRow("SELECT count(*) FROM hour").Scan(&n); err != nil || n != 12 {
		t.Errorf("%d cached hours (%v), want the 12 that ended", n, err)
	}
}

// After Start, the cache is filled in the background back to the first
// reading, so a period's first read finds its hours.
func TestTheCacheIsWarmedAfterStart(t *testing.T) {
	fake, addr := serve(t, energyfake.Options{})
	h := sdktest.New(t, energyapi.DefineWarmingAt(now))
	h.MustStart(&pb.EnergyApiConfig{Url: addr, Token: "fake-token"})
	db, err := sql.Open("sqlite", h.DBPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	// The fake's history: 400 days.
	deadline := time.Now().Add(30 * time.Second)
	var n int
	for n < 400*24 && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
		_ = db.QueryRow("SELECT count(*) FROM hour").Scan(&n)
	}
	if n < 400*24 {
		t.Fatalf("%d hours cached, want the fake's 400 days", n)
	}
	before := len(vebusQueries(fake))
	period(t, pb.NewEnergyApiServiceClient(h.Conn), pb.Span_SPAN_MONTH, "2026-03-01")
	if again := len(vebusQueries(fake)) - before; again != 0 {
		t.Errorf("a warmed month attributed with %d queries, want none", again)
	}
}
