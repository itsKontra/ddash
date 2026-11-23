// Package energyapi is the energy-api Component: it reads the house's
// power readings and energy counters from an InfluxDB v2 bucket with a
// read-only token, leaves implausible readings out, and counts them into
// a period's energy, buckets and costs. It never writes anything.
package energyapi

import (
	"context"
	_ "embed"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	// The zones a browser names, whatever the host has installed.
	_ "time/tzdata"

	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/singleflight"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	pb "github.com/itsKontra/ddash/components/energy-api/energyapipb"
	"github.com/itsKontra/ddash/sdk"
)

//go:embed component.yaml
var manifest []byte

const (
	// requestTimeout bounds one InfluxDB request: a year's query takes a
	// few seconds.
	requestTimeout = 60 * time.Second
	// fresh is how old the latest reading may be for GetFlow.
	fresh = 5 * time.Minute
	// pastTTL is how long a period that is over is served from memory.
	pastTTL = time.Hour
	// maxPeriods bounds the periods kept in memory.
	maxPeriods = 64
	// lookBack is how far before a period its counters' last reading is
	// looked for, to count the period's first window.
	lookBack = 30 * 24 * time.Hour
)

// Definition is energy-api, ready for sdk.Serve.
func Definition() sdk.Definition {
	return define(time.Now)
}

// define is energy-api on the clock now.
func define(now func() time.Time) sdk.Definition {
	return sdk.Define(sdk.Spec[*pb.EnergyApiConfig]{
		Manifest: manifest,
		Service:  &pb.EnergyApiService_ServiceDesc,
		New: func() sdk.Component[*pb.EnergyApiConfig] {
			return &component{now: now}
		},
	})
}

type component struct {
	pb.UnimplementedEnergyApiServiceServer
	now func() time.Time

	// db is nil while not set up.
	db     *influx
	prices *pb.Prices
	// capacity is the battery's usable kWh, nil while not configured.
	capacity *float64
	// reserve is the battery's backup reserve in %, nil while not configured.
	reserve *float64
	flow    *sdk.Cache[*pb.GetFlowResponse]
	first   *sdk.Cache[time.Time]

	mu      sync.Mutex
	periods map[string]cachedPeriod
	// reading shares one read of a period among the callers that ask for
	// it while it isn't cached.
	reading singleflight.Group
}

type cachedPeriod struct {
	resp    *pb.GetPeriodResponse
	expires time.Time
}

func (c *component) Start(_ context.Context, _ *sdk.Env, cfg *pb.EnergyApiConfig) error {
	addr, token := strings.TrimSpace(cfg.GetUrl()), strings.TrimSpace(cfg.GetToken())
	var errs sdk.FieldErrors
	prices := parsePrices(cfg, &errs)
	capacity := parseCapacity(cfg.GetBatteryCapacity(), &errs)
	reserve := parseReserve(cfg.GetBatteryReserve(), &errs)
	if addr == "" && token == "" {
		return errs.Err()
	}
	u, err := url.Parse(addr)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		errs.Add("url", "must be an http or https URL")
	}
	if token == "" {
		errs.Add("token", "is required with an address")
	}
	bucket := "PV"
	if cfg.Bucket != nil {
		bucket = strings.TrimSpace(cfg.GetBucket())
		if bucket == "" {
			errs.Add("bucket", "is required")
		}
	}
	if err := errs.Err(); err != nil {
		return err
	}
	c.db = newInflux(addr, token, bucket, &http.Client{Timeout: requestTimeout})
	c.prices = prices
	c.capacity = capacity
	c.reserve = reserve
	c.flow = sdk.Cached(sdk.TTLFast, c.readFlow)
	c.first = sdk.Cached(24*time.Hour, c.readFirst)
	c.periods = map[string]cachedPeriod{}
	return nil
}

// parsePrices reads the prices, € per kWh with a decimal point or comma.
// Without a buy price there are none.
func parsePrices(cfg *pb.EnergyApiConfig, errs *sdk.FieldErrors) *pb.Prices {
	parse := func(name, s string) float64 {
		s = strings.ReplaceAll(strings.TrimSpace(s), ",", ".")
		if s == "" {
			return 0
		}
		v, err := strconv.ParseFloat(s, 64)
		if err != nil || v < 0 || v >= 10 || math.IsNaN(v) {
			errs.Add(name, "must be € per kWh, e.g. 0.2418")
		}
		return v
	}
	buy, sell := parse("buy_price", cfg.GetBuyPrice()), parse("sell_price", cfg.GetSellPrice())
	if strings.TrimSpace(cfg.GetBuyPrice()) == "" {
		if strings.TrimSpace(cfg.GetSellPrice()) != "" {
			errs.Add("buy_price", "is required with a feed-in tariff")
		}
		return nil
	}
	return &pb.Prices{Buy: buy, Sell: sell}
}

// parseCapacity reads the battery's capacity, kWh with a decimal point or
// comma. Empty is none.
func parseCapacity(s string, errs *sdk.FieldErrors) *float64 {
	s = strings.ReplaceAll(strings.TrimSpace(s), ",", ".")
	if s == "" {
		return nil
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil || !(v > 0 && v < 10000) {
		errs.Add("battery_capacity", "must be kWh, e.g. 10")
		return nil
	}
	return &v
}

// parseReserve reads the battery's backup reserve, % from 0 to 99 with a
// decimal point or comma. Empty is none.
func parseReserve(s string, errs *sdk.FieldErrors) *float64 {
	s = strings.ReplaceAll(strings.TrimSpace(s), ",", ".")
	if s == "" {
		return nil
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil || !(v >= 0 && v <= 99) {
		errs.Add("battery_reserve", "must be a percentage from 0 to 99, e.g. 15")
		return nil
	}
	return &v
}

// Health is down while InfluxDB can't be read, and degraded while it has
// no reading from the last five minutes. Not set up is OK.
func (c *component) Health(ctx context.Context) sdk.Health {
	if c.db == nil {
		return sdk.Health{State: sdk.OK, Message: "not set up"}
	}
	flow, err := c.flow.Get(ctx)
	if err != nil {
		return sdk.Health{State: sdk.Down, Message: status.Convert(err).Message()}
	}
	if flow.GetAt() == nil {
		return sdk.Health{State: sdk.Degraded, Message: "no reading in the last 5 minutes"}
	}
	return sdk.Health{State: sdk.OK}
}

func (c *component) GetFlow(ctx context.Context, _ *pb.GetFlowRequest) (*pb.GetFlowResponse, error) {
	if c.db == nil {
		return &pb.GetFlowResponse{}, nil
	}
	return c.flow.Get(ctx)
}

func (c *component) readFlow(ctx context.Context) (*pb.GetFlowResponse, error) {
	now := c.now()
	rows, err := c.db.query(ctx, flux(c.db.bucket, now.Add(-fresh), now, flowSeries, reduce{fn: "last", bounded: true}))
	if err != nil {
		return nil, err
	}
	resp := &pb.GetFlowResponse{SetUp: true}
	if len(rows) == 0 {
		return resp, nil
	}
	values := map[string]float64{}
	var at time.Time
	for _, r := range rows {
		values[r.field] = r.value
		if r.at.After(at) {
			at = r.at
		}
	}
	resp.At = timestamppb.New(at)
	resp.Power = powerOf(values)
	resp.Battery = &pb.Battery{SocPct: opt(values, socPct), Voltage: opt(values, batteryV), CapacityKwh: c.capacity, ReservePct: c.reserve}
	return resp, nil
}

// byField is every power series by its field.
var byField = func() map[string]series {
	m := map[string]series{}
	for _, s := range flowSeries {
		m[s.field] = s
	}
	return m
}()

func opt(values map[string]float64, s series) *float64 {
	if v, ok := values[s.field]; ok {
		return &v
	}
	return nil
}

// powerOf is the power of one moment from its readings by field. The
// grid turns positive while importing, and consumption is what solar and
// grid supply less what the battery takes, where both are read.
func powerOf(values map[string]float64) *pb.Power {
	p := &pb.Power{
		Solar:    opt(values, solarKW),
		Battery:  opt(values, batteryKW),
		House:    opt(values, houseKW),
		HeatPump: opt(values, heatPumpKW),
		Loads:    opt(values, loadsKW),
	}
	if g, ok := values[gridKW.field]; ok {
		p.Grid = proto.Float64(-g + 0)
	}
	if p.Solar != nil && p.Grid != nil {
		p.Consumption = proto.Float64(max(0, p.GetSolar()+p.GetGrid()-p.GetBattery()))
	}
	return p
}

func (c *component) GetPeriod(ctx context.Context, req *pb.GetPeriodRequest) (*pb.GetPeriodResponse, error) {
	if c.db == nil {
		return &pb.GetPeriodResponse{}, nil
	}
	loc := time.UTC
	if req.GetTimeZone() != "" {
		var err error
		if loc, err = time.LoadLocation(req.GetTimeZone()); err != nil {
			return nil, status.Errorf(codes.InvalidArgument, "time zone %q is unknown", req.GetTimeZone())
		}
	}
	now := c.now()
	p, err := periodOf(req.GetSpan(), req.GetDate(), loc, now)
	if err != nil {
		return nil, err
	}
	key := fmt.Sprintf("%d|%d|%s", p.span, p.from.Unix(), loc)
	c.mu.Lock()
	hit, ok := c.periods[key]
	c.mu.Unlock()
	if ok && now.Before(hit.expires) {
		return hit.resp, nil
	}
	// The read belongs to no one caller: it outlives the one that started
	// it, bounded by the queries' own timeout, and fills the cache.
	read := c.reading.DoChan(key, func() (any, error) {
		resp, err := c.readPeriod(context.WithoutCancel(ctx), p, now)
		if err != nil {
			return nil, err
		}
		ttl := sdk.TTLSlow
		// Over once its last window is: later readings rarely arrive.
		if !now.Before(p.to.Add(p.window)) {
			ttl = pastTTL
		}
		c.mu.Lock()
		if len(c.periods) >= maxPeriods {
			clear(c.periods)
		}
		c.periods[key] = cachedPeriod{resp: resp, expires: now.Add(ttl)}
		c.mu.Unlock()
		return resp, nil
	})
	select {
	case <-ctx.Done():
		return nil, status.FromContextError(ctx.Err()).Err()
	case r := <-read:
		if r.Err != nil {
			return nil, r.Err
		}
		return r.Val.(*pb.GetPeriodResponse), nil
	}
}

func (c *component) readFirst(ctx context.Context) (time.Time, error) {
	rows, err := c.db.query(ctx, flux(c.db.bucket, time.Unix(0, 0), c.now(), []series{solarKWh}, reduce{fn: "first"}))
	if err != nil || len(rows) == 0 {
		return time.Time{}, err
	}
	return rows[0].at, nil
}

func (c *component) readPeriod(ctx context.Context, p period, now time.Time) (*pb.GetPeriodResponse, error) {
	resp := &pb.GetPeriodResponse{
		SetUp:    true,
		Span:     p.span,
		From:     timestamppb.New(p.from),
		To:       timestamppb.New(p.to),
		Previous: p.previous(),
		Prices:   c.prices,
	}
	if p.to.Before(now) {
		resp.Next = p.next()
	}
	stop := p.to
	if now.Before(stop) {
		stop = now
	}
	var before, counters, powers []row
	var first time.Time
	g, gctx := errgroup.WithContext(ctx)
	if p.from.Before(stop) {
		db := c.db
		g.Go(func() (err error) {
			before, err = db.query(gctx, flux(db.bucket, p.from.Add(-lookBack), p.from, counterSeries, reduce{fn: "last"}))
			return err
		})
		g.Go(func() (err error) {
			counters, err = db.query(gctx, flux(db.bucket, p.from, stop, counterSeries, reduce{every: p.window, fn: "last"}))
			return err
		})
		fields := loadSeries
		if p.curve {
			fields = powerSeries
		}
		g.Go(func() (err error) {
			// A day's or week's readings are few enough to bound one by one;
			// a month's or year's windows are judged by their mean.
			powers, err = db.query(gctx, flux(db.bucket, p.from, stop, fields, reduce{every: p.window, fn: "mean", bounded: p.curve}))
			return err
		})
	}
	g.Go(func() (err error) {
		first, err = c.first.Get(gctx)
		return err
	})
	if err := g.Wait(); err != nil {
		return nil, err
	}
	if !first.IsZero() {
		resp.FirstDay = first.In(p.from.Location()).Format(day)
	}

	// Only the buckets that have begun.
	n := 0
	for n < len(p.edges)-1 && p.edges[n].Before(now) {
		n++
	}
	totals := make([]*pb.Totals, n)
	for i := range totals {
		totals[i] = &pb.Totals{}
	}
	at := func(t time.Time) *pb.Totals {
		if i := p.bucket(t); i >= 0 && i < n {
			return totals[i]
		}
		return nil
	}

	// Counters: each window's last reading, stamped with the window's end,
	// after the last reading before the period.
	readings := map[string][]point{}
	for _, r := range before {
		readings[r.field] = append(readings[r.field], point{at: r.at, v: r.value})
	}
	for _, r := range counters {
		readings[r.field] = append(readings[r.field], point{at: r.at.Add(p.window), v: r.value})
	}
	add := map[string]func(*pb.Totals, float64){
		solarKWh.field:    func(t *pb.Totals, v float64) { t.Solar += v },
		importKWh.field:   func(t *pb.Totals, v float64) { t.GridImport += v },
		exportKWh.field:   func(t *pb.Totals, v float64) { t.GridExport += v },
		chargedKWh.field:  func(t *pb.Totals, v float64) { t.BatteryCharged += v },
		dischargedK.field: func(t *pb.Totals, v float64) { t.BatteryDischarged += v },
	}
	for field, points := range readings {
		slices.SortFunc(points, func(a, b point) int { return a.at.Compare(b.at) })
		for _, s := range steps(points) {
			if t := at(s.at.Add(-time.Nanosecond)); t != nil {
				add[field](t, s.kwh)
			}
		}
	}

	// Powers: the loads integrated over each window (up to now), and for
	// a day or week the curve.
	byTime := map[time.Time]map[string]float64{}
	for _, r := range powers {
		if s, ok := byField[r.field]; !ok || !s.plausible(r.value) {
			continue
		}
		if t := at(r.at); t != nil {
			hours := min(p.window, now.Sub(r.at)).Hours()
			switch r.field {
			case houseKW.field:
				t.House += max(0, r.value) * hours
			case heatPumpKW.field:
				t.HeatPump += max(0, r.value) * hours
			}
		}
		if p.curve {
			if byTime[r.at] == nil {
				byTime[r.at] = map[string]float64{}
			}
			byTime[r.at][r.field] = r.value
		}
	}
	times := make([]time.Time, 0, len(byTime))
	for t := range byTime {
		times = append(times, t)
	}
	slices.SortFunc(times, time.Time.Compare)
	for _, t := range times {
		resp.Samples = append(resp.Samples, &pb.Sample{At: timestamppb.New(t), Power: powerOf(byTime[t]), SocPct: opt(byTime[t], socPct)})
	}

	resp.Totals = &pb.Totals{}
	for i, t := range totals {
		c.finish(t)
		resp.Buckets = append(resp.Buckets, &pb.Bucket{From: timestamppb.New(p.edges[i]), To: timestamppb.New(p.edges[i+1]), Totals: t})
		sum(resp.Totals, t)
	}
	return resp, nil
}

// finish derives a bucket's consumption and money from its energy.
func (c *component) finish(t *pb.Totals) {
	t.Consumption = max(0, t.Solar+t.GridImport-t.GridExport-t.BatteryCharged+t.BatteryDischarged)
	if c.prices != nil {
		t.Cost = t.GridImport * c.prices.Buy
		t.Revenue = t.GridExport * c.prices.Sell
		t.Savings = max(0, t.Consumption-t.GridImport)*c.prices.Buy + t.Revenue
	}
}

func sum(into, t *pb.Totals) {
	into.Solar += t.Solar
	into.GridImport += t.GridImport
	into.GridExport += t.GridExport
	into.BatteryCharged += t.BatteryCharged
	into.BatteryDischarged += t.BatteryDischarged
	into.Consumption += t.Consumption
	into.House += t.House
	into.HeatPump += t.HeatPump
	into.Cost += t.Cost
	into.Revenue += t.Revenue
	into.Savings += t.Savings
}
