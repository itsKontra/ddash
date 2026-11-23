package energyapi

import (
	"context"
	"slices"
	"time"

	"golang.org/x/sync/errgroup"

	pb "github.com/itsKontra/ddash/components/energy-api/energyapipb"
)

// algoVersion is the version of how an hour is attributed. A cached hour
// of another version is attributed again when it is read.
const algoVersion = 1

const (
	// slot is what an hour is attributed in: a sum of minimums isn't the
	// minimum of sums, so solar serving the use is settled every 5 minutes.
	slot = 5 * time.Minute
	// chunk bounds the hours one set of queries attributes, so a first
	// Year read is many small queries instead of one that times out.
	chunk = 7 * 24 * time.Hour
	// settle is how long after its end an hour may still get late
	// readings. An hour attributed later than that is final.
	settle = 48 * time.Hour
	// refresh is how long an hour that isn't final is served from the cache.
	refresh = 45 * time.Second
)

// The Victron's VE.Bus energy counters, on its AC side.
var (
	acInToOut  = counter("victron", "acin1_to_acout_kWh")
	acInToInv  = counter("victron", "acin1_to_inverter_kWh")
	invToOut   = counter("victron", "inverter_to_acout_kWh")
	invToAcIn  = counter("victron", "inverter_to_acin1_kWh")
	acOutToInv = counter("victron", "acout_to_inverter_kWh")
)

// hourCounters are what an hour is counted from, in the order of the c…
// indexes below.
var hourCounters = []series{solarKWh, importKWh, exportKWh, chargedKWh, dischargedK, acInToOut, acInToInv, invToOut, invToAcIn, acOutToInv}

const (
	cSolar = iota
	cImport
	cExport
	cCharged
	cDischarged
	cInToOut
	cInToInv
	cInvToOut
	cInvToIn
	cOutToInv
	nCounters
)

// splitPowers split a slot that lacks the VE.Bus counters, in the order
// of the p… indexes below.
var splitPowers = []series{solarKW, gridKW, batteryKW}

const (
	pSolar = iota
	pGrid
	pBattery
	nPowers
)

// hour is one UTC hour's energy: its counters and its flows. Hours, not
// days, since periods are asked for in the browser's zone and hours add
// up to any of its days.
type hour struct {
	at time.Time
	t  *pb.Totals
}

// slotReadings is what the counters counted in one slot, and its powers'
// means.
type slotReadings struct {
	kwh     [nCounters]float64
	read    [nCounters]bool
	power   [nPowers]float64
	powered [nPowers]bool
}

// vebus tells whether a slot has the VE.Bus counters it is split by, and
// the Huawei's and the grid meter's.
func (s *slotReadings) vebus() bool {
	for _, i := range []int{cSolar, cImport, cExport, cInToOut, cInToInv, cInvToOut, cInvToIn} {
		if !s.read[i] {
			return false
		}
	}
	return true
}

// counted tells whether any of the Huawei's or the grid meter's counters
// was read in the slot: a slot without is no energy to split.
func (s *slotReadings) counted() bool { return s.read[cSolar] || s.read[cImport] || s.read[cExport] }

// share is what slots' splits add up to: the solar into the charger and
// the charger's whole input, and the export from the solar and from the
// battery.
type share struct{ solarCharge, charge, solarExport, batteryExport float64 }

func (a *share) add(b share) {
	a.solarCharge += b.solarCharge
	a.charge += b.charge
	a.solarExport += b.solarExport
	a.batteryExport += b.batteryExport
}

// split attributes one slot's energy on the Victron's AC input, which the
// inverter and the grid meter share: solar s, import i, export e, into
// the charger q and from the inverter back to the AC input ri. The AC
// input's loads (the heat pump, and the house passed through) are what is
// left. The solar serves them first, then the charger, then the export;
// the inverter serves the loads left, then the export; the grid covers
// the rest. What the inverter puts out to the AC output serves the house
// there and needs no split.
func split(s, i, e, q, ri float64) share {
	loads := max(0, s+i+ri-e-q)
	sl := min(s, loads)
	rl := min(ri, loads-sl)
	sq := min(s-sl, q)
	se := min(s-sl-sq, e)
	re := min(ri-rl, e-se)
	// Export the solar and the inverter had nothing left for: the grid
	// served loads in the same slot while the solar was exported.
	if short := e - se - re; short > 0 {
		x := min(short, sl)
		se, short = se+x, short-x
		x = min(short, sq)
		sq, se, short = sq-x, se+x, short-x
		re += min(short, rl)
	}
	return share{solarCharge: sq, charge: q, solarExport: se, batteryExport: re}
}

// powerSplit splits a slot by its powers' means, where it lacks the
// VE.Bus counters: the battery's power stands in for the charger's input
// and the inverter's output.
func powerSplit(r *slotReadings) (share, bool) {
	if !r.powered[pSolar] || !r.powered[pGrid] {
		return share{}, false
	}
	h := slot.Hours()
	g := r.power[pGrid] // positive while exporting
	b := r.power[pBattery]
	return split(max(0, r.power[pSolar])*h, max(0, -g)*h, max(0, g)*h, max(0, b)*h, max(0, -b)*h), true
}

// attribute splits an hour's counters as its slots' shares say: the
// export between solar and battery, and the charge between solar and
// grid. Each source's flows add up to its counter, and what is left of
// each goes to the use.
func attribute(t *pb.Totals, sh share) *pb.Flows {
	s, i, e, c, d := t.Solar, t.GridImport, t.GridExport, t.BatteryCharged, t.BatteryDischarged
	f := &pb.Flows{}
	f.SolarExport = e
	if x := sh.solarExport + sh.batteryExport; x > 0 {
		f.SolarExport = e * sh.solarExport / x
	}
	f.SolarExport = min(f.SolarExport, s)
	f.BatteryExport = min(e-f.SolarExport, d)
	f.SolarExport = min(e-f.BatteryExport, s)
	f.SolarBattery = c
	if sh.charge > 0 {
		f.SolarBattery = c * min(1, sh.solarCharge/sh.charge)
	}
	f.SolarBattery = min(f.SolarBattery, s-f.SolarExport)
	f.GridBattery = min(c-f.SolarBattery, i)
	f.SolarBattery = min(c-f.GridBattery, s-f.SolarExport)
	f.SolarUsed = s - f.SolarExport - f.SolarBattery
	f.BatteryUsed = d - f.BatteryExport
	f.GridUsed = i - f.GridBattery
	return f
}

// attributeHours reads the hours [from, to) every slot and attributes
// them; from is an hour's start, and the last hour may end early at to.
// Counters are read as each slot's last value, which InfluxDB reduces in
// its storage engine; so are the powers' means, judged after.
func (c *component) attributeHours(ctx context.Context, from, to time.Time) ([]hour, error) {
	db := c.db
	var before, counters, powers []row
	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() (err error) {
		before, err = db.query(gctx, flux(db.bucket, from.Add(-lookBack), from, hourCounters, reduce{fn: "last"}))
		return err
	})
	g.Go(func() (err error) {
		counters, err = db.query(gctx, flux(db.bucket, from, to, hourCounters, reduce{every: slot, fn: "last"}))
		return err
	})
	g.Go(func() (err error) {
		powers, err = db.query(gctx, flux(db.bucket, from, to, splitPowers, reduce{every: slot, fn: "mean"}))
		return err
	})
	if err := g.Wait(); err != nil {
		return nil, err
	}

	slots := make([]slotReadings, (to.Sub(from)+slot-1)/slot)
	slotOf := func(t time.Time) int {
		if t.Before(from) || !t.Before(to) {
			return -1
		}
		return int(t.Sub(from) / slot)
	}
	counterIndex := map[string]int{}
	for i, s := range hourCounters {
		counterIndex[s.field] = i
	}
	// Each slot's last reading, stamped with the slot's end, after the
	// last reading before the hours.
	readings := make([][]point, nCounters)
	for _, r := range before {
		if i, ok := counterIndex[r.field]; ok {
			readings[i] = append(readings[i], point{at: r.at, v: r.value})
		}
	}
	for _, r := range counters {
		if i, ok := counterIndex[r.field]; ok {
			readings[i] = append(readings[i], point{at: r.at.Add(slot), v: r.value})
		}
	}
	for i, points := range readings {
		slices.SortFunc(points, func(a, b point) int { return a.at.Compare(b.at) })
		for _, s := range steps(points) {
			if n := slotOf(s.at.Add(-time.Nanosecond)); n >= 0 {
				slots[n].kwh[i] += s.kwh
				slots[n].read[i] = true
			}
		}
	}
	for _, r := range powers {
		i := slices.IndexFunc(splitPowers, func(s series) bool { return s.field == r.field })
		if n := slotOf(r.at); i >= 0 && n >= 0 && splitPowers[i].plausible(r.value) {
			slots[n].power[i] = r.value
			slots[n].powered[i] = true
		}
	}

	perHour := int(time.Hour / slot)
	var out []hour
	for start := 0; start < len(slots); start += perHour {
		t := &pb.Totals{}
		var sh share
		var vebus [nCounters]float64
		counted, split := 0, 0
		for n := start; n < min(start+perHour, len(slots)); n++ {
			r := &slots[n]
			t.Solar += r.kwh[cSolar]
			t.GridImport += r.kwh[cImport]
			t.GridExport += r.kwh[cExport]
			t.BatteryCharged += r.kwh[cCharged]
			t.BatteryDischarged += r.kwh[cDischarged]
			for i := cInToOut; i < nCounters; i++ {
				vebus[i] += r.kwh[i]
			}
			if !r.counted() {
				continue
			}
			counted++
			if r.vebus() {
				split++
				sh.add(splitSlot(r))
			} else if s, ok := powerSplit(r); ok {
				sh.add(s)
			}
		}
		t.Flows = attribute(t, sh)
		t.Flows.Estimated = split < counted
		if split > 0 && !t.Flows.Estimated {
			t.Flows.ChargeLoss = vebus[cInToInv] + vebus[cOutToInv] - t.BatteryCharged
			t.Flows.DischargeLoss = t.BatteryDischarged - vebus[cInvToOut] - vebus[cInvToIn]
			t.Flows.AcOut = vebus[cInToOut] + vebus[cInvToOut] - vebus[cOutToInv]
		}
		out = append(out, hour{at: from.Add(time.Duration(start) * slot), t: t})
	}
	return out, nil
}

// splitSlot splits a slot by its counters: the VE.Bus counters say what
// went into the charger from the AC input and what the inverter fed back.
func splitSlot(r *slotReadings) share {
	return split(r.kwh[cSolar], r.kwh[cImport], r.kwh[cExport], r.kwh[cInToInv], r.kwh[cInvToIn])
}

// hours returns the UTC hours from the one holding from up to stop,
// attributing those the cache lacks chunk by chunk. An hour that has
// begun but not ended (the current one) is attributed up to stop and
// never cached.
func (c *component) hours(ctx context.Context, from, stop, now time.Time) ([]hour, error) {
	var out []hour
	for a := from.UTC().Truncate(time.Hour); a.Before(stop); a = a.Add(chunk) {
		hs, err := c.fill(ctx, a, earliest(a.Add(chunk), stop), now)
		if err != nil {
			return nil, err
		}
		out = append(out, hs...)
	}
	return out, nil
}

// fill returns the hours [from, to), at most a chunk, from the cache, and
// attributes and caches the ones it lacks, has of another version, or
// has from more than refresh ago while not yet final. Fills take turns,
// so a read and the warm-up never attribute the same hours twice.
func (c *component) fill(ctx context.Context, from, to, now time.Time) ([]hour, error) {
	// The cache is keyed by times, which are equal only in the same zone.
	from, to = from.UTC(), to.UTC()
	c.filling.Lock()
	defer c.filling.Unlock()
	cached, err := c.store.load(ctx, from, to)
	if err != nil {
		return nil, err
	}
	var first, last time.Time
	for h := from; h.Before(to); h = h.Add(time.Hour) {
		if r, ok := cached[h]; ok && r.valid(now) {
			continue
		}
		if first.IsZero() {
			first = h
		}
		last = h
	}
	out := make([]hour, 0, int(to.Sub(from)/time.Hour)+1)
	if !first.IsZero() {
		hs, err := c.attributeHours(ctx, first, earliest(last.Add(time.Hour), to))
		if err != nil {
			return nil, err
		}
		var whole []hour
		for _, h := range hs {
			cached[h.at] = cachedHour{hour: h, algo: algoVersion, at: now}
			if !h.at.Add(time.Hour).After(earliest(to, now)) {
				whole = append(whole, h)
			}
		}
		if err := c.store.save(ctx, whole, now); err != nil {
			return nil, err
		}
	}
	for h := from; h.Before(to); h = h.Add(time.Hour) {
		if r, ok := cached[h]; ok {
			out = append(out, r.hour)
		}
	}
	return out, nil
}

// warm attributes the hours the cache lacks, newest first and chunk by
// chunk, back to the first reading, so a first Year read finds them.
func (c *component) warm(ctx context.Context) {
	first, err := c.first.Get(ctx)
	if err != nil || first.IsZero() {
		return
	}
	now := c.now()
	for to := now.Truncate(time.Hour); to.After(first) && ctx.Err() == nil; to = to.Add(-chunk) {
		from := latest(to.Add(-chunk), first.Truncate(time.Hour))
		if _, err := c.fill(ctx, from, to, now); err != nil {
			if ctx.Err() == nil {
				c.log.Warn("warming the hours' cache", "err", err)
			}
			return
		}
	}
}

// sumFlows adds f's flows to into's.
func sumFlows(into, f *pb.Flows) {
	into.SolarUsed += f.SolarUsed
	into.SolarBattery += f.SolarBattery
	into.SolarExport += f.SolarExport
	into.BatteryUsed += f.BatteryUsed
	into.BatteryExport += f.BatteryExport
	into.GridUsed += f.GridUsed
	into.GridBattery += f.GridBattery
	into.ChargeLoss += f.ChargeLoss
	into.DischargeLoss += f.DischargeLoss
	into.AcOut += f.AcOut
	into.Estimated = into.Estimated || f.Estimated
}

func earliest(a, b time.Time) time.Time {
	if b.Before(a) {
		return b
	}
	return a
}

func latest(a, b time.Time) time.Time {
	if b.After(a) {
		return b
	}
	return a
}
