// Package energyfake is a fake InfluxDB v2 holding a simulated house's
// readings, as Telegraf writes them from a Huawei SUN2000 with its grid
// meter and a Victron GX: a 10 kWp roof under a seasonal sun with cloudy
// days, the house's and the heat pump's loads, and a 10 kWh battery with
// a 10 % backup reserve, every 5 minutes from 400 days ago up to the
// clock. The heat pump, the inverter and the grid meter share the
// Victron's AC input; the house is on its AC output, and the Victron's
// VE.Bus counters count the energy along each of its paths. One glitch,
// like the meter's: for 20 minutes the grid reads its "invalid" sentinel
// and its counters jump by millions.
//
// It answers the Flux queries energy-api sends, and only those: from a
// bucket, a range, a filter picking measurement and field, optionally
// toFloat and a filter bounding each field's _value, then aggregateWindow
// (mean or last, stamped with the window's start) or last() or first(),
// then keep.
package energyfake

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Step is how often the simulated house is read.
const Step = 5 * time.Minute

// Sentinel is what the grid meter reads while it is glitching.
const Sentinel = 2147483.647

// Options configure the fake.
type Options struct {
	// Token is the token every request must carry; "fake-token" when empty.
	Token string
	// Bucket is the only bucket; "PV" when empty.
	Bucket string
	// Now is the clock; time.Now when nil.
	Now func() time.Time
	// Glitch is when the grid meter's 20 minutes of glitching start; two
	// days before the clock started at 17:50 UTC when zero.
	Glitch time.Time
	// QueryDelay holds every query this long before it is answered, as a
	// year's query against a busy InfluxDB takes.
	QueryDelay time.Duration
	// GridCharge is the hours of every day, CET and fractional, [from, to),
	// in which the battery charges at full power, taking from the grid what
	// the sun doesn't give, as on a cheap tariff. None when both are zero.
	GridCharge [2]float64
	// VEBusFrom is when the Victron's VE.Bus counters begin, as a live
	// Telegraf feed's do after a history backfilled without them; the
	// whole history has them when zero.
	VEBusFrom time.Time
	// VictronFrom is when the Victron GX is read at all (its battery,
	// loads and counters), as before a history's first Victron readings;
	// the whole history has them when zero.
	VictronFrom time.Time
	// VictronGap is when the Victron GX isn't read, [from, to), as while
	// it hangs; a zero to leaves it quiet from from on. None when from is
	// zero.
	VictronGap [2]time.Time
	// InverterGap is the same for the Huawei inverter and its grid meter.
	InverterGap [2]time.Time
}

// Server is the fake. It is safe for concurrent use.
type Server struct {
	opts Options
	mu   sync.Mutex
	sim  *sim
	// Queries is every query answered, in order.
	queries []string
}

// New is a fake whose history begins 400 days before the clock.
func New(opts Options) *Server {
	if opts.Token == "" {
		opts.Token = "fake-token"
	}
	if opts.Bucket == "" {
		opts.Bucket = "PV"
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	now := opts.Now().UTC()
	if opts.Glitch.IsZero() {
		d := now.AddDate(0, 0, -2)
		opts.Glitch = time.Date(d.Year(), d.Month(), d.Day(), 17, 50, 0, 0, time.UTC)
	}
	start := now.AddDate(0, 0, -400).Truncate(24 * time.Hour)
	return &Server{opts: opts, sim: &sim{start: start, glitch: opts.Glitch, gridCharge: opts.GridCharge, vebusFrom: opts.VEBusFrom, victronFrom: opts.VictronFrom, gaps: map[string][2]time.Time{
		"victron": opts.VictronGap, "kW": opts.InverterGap, "kWh": opts.InverterGap,
	}}}
}

// Queries returns every Flux query answered so far.
func (s *Server) Queries() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.queries)
}

// ServeHTTP answers the bucket listing, queries and the health check.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/health" {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"name":"influxdb","status":"pass","version":"v2.7.12"}`)
		return
	}
	time.Sleep(s.opts.QueryDelay)
	if r.Header.Get("Authorization") != "Token "+s.opts.Token {
		fail(w, http.StatusUnauthorized, "unauthorized", "unauthorized access")
		return
	}
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/api/v2/buckets":
		var buckets []map[string]string
		if name := r.URL.Query().Get("name"); name == "" || name == s.opts.Bucket {
			buckets = append(buckets, map[string]string{"id": "b0000000000000b0", "name": s.opts.Bucket, "orgID": OrgID})
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"buckets": buckets})
	case r.Method == http.MethodPost && r.URL.Path == "/api/v2/query":
		if r.URL.Query().Get("orgID") != OrgID {
			fail(w, http.StatusNotFound, "not found", "organization not found")
			return
		}
		var body struct {
			Query string `json:"query"`
		}
		raw, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(raw, &body); err != nil {
			fail(w, http.StatusBadRequest, "invalid", "failed to decode request body")
			return
		}
		q, err := parse(body.Query)
		if err != nil {
			fail(w, http.StatusBadRequest, "invalid", err.Error())
			return
		}
		if q.bucket != s.opts.Bucket {
			fail(w, http.StatusNotFound, "not found", fmt.Sprintf("failed to initialize execute state: could not find bucket %q", q.bucket))
			return
		}
		s.mu.Lock()
		s.queries = append(s.queries, body.Query)
		s.sim.extend(s.opts.Now())
		rows := s.sim.answer(q)
		s.mu.Unlock()
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		fmt.Fprint(w, ",result,table,_time,_value,_field\r\n")
		for _, r := range rows {
			fmt.Fprintf(w, ",_result,%d,%s,%s,%s\r\n", r.table, r.at.UTC().Format(time.RFC3339), strconv.FormatFloat(r.v, 'f', -1, 64), r.field)
		}
	default:
		fail(w, http.StatusNotFound, "not found", "path not found")
	}
}

// OrgID is the fake's one organisation.
const OrgID = "0000000000000001"

func fail(w http.ResponseWriter, code int, kind, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"code": kind, "message": msg})
}

// query is what a Flux query asks for.
type query struct {
	bucket   string
	from, to time.Time
	fields   []string // "measurement/field"
	bounds   map[string][2]float64
	every    time.Duration
	fn       string
}

var (
	reBucket = regexp.MustCompile(`from\(bucket: "([^"]*)"\)`)
	reRange  = regexp.MustCompile(`range\(start: (\S+), stop: (\S+)\)`)
	rePick   = regexp.MustCompile(`r\._measurement == "([^"]+)" and r\._field == "([^"]+)"`)
	reBound  = regexp.MustCompile(`r\._field == "([^"]+)" and r\._value >= (-?[\d.]+) and r\._value <= (-?[\d.]+)`)
	reWindow = regexp.MustCompile(`aggregateWindow\(every: (\d+)([mh]), fn: (mean|last), createEmpty: false, timeSrc: "_start"\)`)
	reReduce = regexp.MustCompile(`\|> (last|first)\(\)`)
)

func parse(flux string) (query, error) {
	var q query
	m := reBucket.FindStringSubmatch(flux)
	if m == nil {
		return q, fmt.Errorf("energyfake: no from(bucket:) in %q", flux)
	}
	q.bucket = m[1]
	m = reRange.FindStringSubmatch(flux)
	if m == nil {
		return q, fmt.Errorf("energyfake: no range in %q", flux)
	}
	var err1, err2 error
	q.from, err1 = time.Parse(time.RFC3339, m[1])
	q.to, err2 = time.Parse(time.RFC3339, m[2])
	if err1 != nil || err2 != nil {
		return q, fmt.Errorf("energyfake: range takes RFC 3339 times: %q", m[0])
	}
	for _, p := range rePick.FindAllStringSubmatch(flux, -1) {
		q.fields = append(q.fields, p[1]+"/"+p[2])
	}
	q.bounds = map[string][2]float64{}
	if strings.Contains(flux, "toFloat()") {
		for _, b := range reBound.FindAllStringSubmatch(flux, -1) {
			lo, _ := strconv.ParseFloat(b[2], 64)
			hi, _ := strconv.ParseFloat(b[3], 64)
			q.bounds[b[1]] = [2]float64{lo, hi}
		}
	}
	if m := reWindow.FindStringSubmatch(flux); m != nil {
		n, _ := strconv.Atoi(m[1])
		q.every = time.Duration(n) * time.Minute
		if m[2] == "h" {
			q.every = time.Duration(n) * time.Hour
		}
		q.fn = m[3]
	} else if m := reReduce.FindStringSubmatch(flux); m != nil {
		q.fn = m[1]
	} else {
		return q, fmt.Errorf("energyfake: no aggregateWindow, last() or first() in %q", flux)
	}
	return q, nil
}

type out struct {
	table int
	field string
	at    time.Time
	v     float64
}

// answer reduces the readings of each field the query picks.
func (s *sim) answer(q query) []out {
	var rows []out
	for table, key := range q.fields {
		measurement, field, _ := strings.Cut(key, "/")
		read, since, ok := s.series(measurement, field)
		if !ok {
			continue
		}
		bound, bounded := q.bounds[field]
		var windowAt time.Time
		var sum float64
		var n int
		var last float64
		flush := func() {
			if n == 0 {
				return
			}
			v := last
			if q.fn == "mean" {
				v = sum / float64(n)
			}
			rows = append(rows, out{table: table, field: field, at: windowAt, v: v})
			n, sum = 0, 0
		}
		for i := max(s.index(q.from), since); i < s.n && s.at(i).Before(q.to); i++ {
			if s.quiet(measurement, i) {
				continue
			}
			v := read(i)
			if bounded && (v < bound[0] || v > bound[1]) {
				continue
			}
			t := s.at(i)
			switch q.fn {
			case "first":
				if n == 0 {
					windowAt, last, n = t, v, 1
				}
				continue
			case "last":
				if q.every == 0 {
					windowAt, last, n = t, v, 1
					continue
				}
			}
			w := t.Truncate(q.every)
			if n > 0 && !w.Equal(windowAt) {
				flush()
			}
			windowAt = w
			sum += v
			last = v
			n++
		}
		flush()
	}
	return rows
}

// sim is the simulated house, read every Step from start.
type sim struct {
	start       time.Time
	glitch      time.Time
	gridCharge  [2]float64
	vebusFrom   time.Time
	victronFrom time.Time
	// gaps is when each measurement isn't read, by measurement.
	gaps map[string][2]time.Time
	n    int
	// Powers in kW (battery positive while charging, grid positive while
	// importing), the state of charge in %, and the counters in kWh.
	solar, house, heatPump, battery, grid, soc                []float64
	solarKWh, importKWh, exportKWh, chargedKWh, dischargedKWh []float64
	// The VE.Bus counters in kWh, on the AC side: the AC input to the AC
	// output (passed through) and to the charger, and the inverter to the
	// AC output and back to the AC input.
	acInToOut, acInToInverter, inverterToOut, inverterToAcIn []float64
}

func (s *sim) at(i int) time.Time { return s.start.Add(time.Duration(i) * Step) }

// index is the first reading at or after t.
func (s *sim) index(t time.Time) int {
	if !t.After(s.start) {
		return 0
	}
	return int((t.Sub(s.start) + Step - 1) / Step)
}

// quiet tells whether measurement isn't read at reading i: it is in its
// gap.
func (s *sim) quiet(measurement string, i int) bool {
	g := s.gaps[measurement]
	t := s.at(i)
	return !g[0].IsZero() && !t.Before(g[0]) && (g[1].IsZero() || t.Before(g[1]))
}

func (s *sim) glitching(i int) bool {
	t := s.at(i)
	return !t.Before(s.glitch) && t.Before(s.glitch.Add(20*time.Minute))
}

// series reads one field as Telegraf writes it, from the index of its
// first reading.
func (s *sim) series(measurement, field string) (func(int) float64, int, bool) {
	jump := func(c []float64) func(int) float64 {
		return func(i int) float64 {
			if s.glitching(i) {
				return c[i] + 21460526
			}
			return c[i]
		}
	}
	plain := func(c []float64) func(int) float64 { return func(i int) float64 { return c[i] } }
	m := map[string]func(int) float64{
		"kW/INVERTER_Pout": plain(s.solar),
		"kW/Grid_Pout": func(i int) float64 {
			if s.glitching(i) {
				return Sentinel
			}
			return round(-s.grid[i], 3)
		},
		"kWh/Inverter_Energy_Yield": plain(s.solarKWh),
		"kWh/Grid_Energy_Use":       jump(s.importKWh),
		"kWh/Grid_Energy_Yield":     jump(s.exportKWh),
		"victron/battery_power_kW":  plain(s.battery),
		"victron/essential_load_kW": plain(s.house),
		"victron/ac_load_kW":        plain(s.heatPump),
		"victron/consumption_kW":    func(i int) float64 { return round(s.house[i]+s.heatPump[i], 3) },
		"victron/battery_soc_pct":   func(i int) float64 { return math.Round(s.soc[i]) },
		"victron/battery_voltage_V": func(i int) float64 { return round(48+s.soc[i]*0.07, 1) },
		"victron/charged_kWh":       plain(s.chargedKWh),
		"victron/discharged_kWh":    plain(s.dischargedKWh),
	}
	hundredths := func(c []float64) func(int) float64 { return func(i int) float64 { return round(c[i], 2) } }
	vebus := map[string]func(int) float64{
		"victron/acin1_to_acout_kWh":    hundredths(s.acInToOut),
		"victron/acin1_to_inverter_kWh": hundredths(s.acInToInverter),
		"victron/inverter_to_acout_kWh": hundredths(s.inverterToOut),
		"victron/inverter_to_acin1_kWh": hundredths(s.inverterToAcIn),
		// Nothing on the AC output feeds back: it never counts.
		"victron/acout_to_inverter_kWh": func(int) float64 { return 0.17 },
	}
	key := measurement + "/" + field
	since := 0
	if measurement == "victron" {
		since = s.index(s.victronFrom)
	}
	if f, ok := vebus[key]; ok {
		return f, max(since, s.index(s.vebusFrom)), true
	}
	f, ok := m[key]
	return f, since, ok
}

func round(v float64, places int) float64 {
	p := math.Pow(10, float64(places))
	return math.Round(v*p) / p
}

const (
	capacity = 10.0 // kWh
	maxBatt  = 5.0  // kW
	// Reserve is the backup reserve in %, which the battery isn't
	// discharged below.
	Reserve = 10.0
	// Charging takes more than reaches the battery; discharging delivers
	// less than leaves it.
	chargeLoss, dischargeLoss = 0.92, 0.95
)

// extend simulates up to now.
func (s *sim) extend(now time.Time) {
	h := Step.Hours()
	for ; !s.at(s.n).After(now); s.n++ {
		t := s.at(s.n)
		solar, house, heat := sun(t), houseLoad(t), heatPumpLoad(t)
		soc, solarC, imp, exp, chg, dis := 50.0, 31000.0, 14600.0, 22000.0, 1300.0, 1270.0
		inOut, inInv, invOut, invIn := 8100.0, 2400.0, 1900.0, 310.0
		if s.n > 0 {
			i := s.n - 1
			soc, solarC, imp, exp, chg, dis = s.soc[i], s.solarKWh[i], s.importKWh[i], s.exportKWh[i], s.chargedKWh[i], s.dischargedKWh[i]
			inOut, inInv, invOut, invIn = s.acInToOut[i], s.acInToInverter[i], s.inverterToOut[i], s.inverterToAcIn[i]
		}
		// The battery takes the surplus and covers the deficit, within its
		// power and between its reserve and 100 %; while grid charging it
		// takes its full power.
		surplus := solar - house - heat
		full := (100 - soc) / 100 * capacity / h
		batt := 0.0
		if hr := hour(t); hr >= s.gridCharge[0] && hr < s.gridCharge[1] {
			batt = min(maxBatt, full)
		} else if surplus > 0 {
			batt = min(surplus*chargeLoss, maxBatt, full)
		} else {
			batt = -min(-surplus/dischargeLoss, maxBatt, max(0, soc-Reserve)/100*capacity/h)
		}
		ac := batt / chargeLoss
		if batt < 0 {
			ac = batt * dischargeLoss
		}
		grid := house + heat + ac - solar
		// The inverter serves the house on the AC output first and feeds
		// the rest back to the AC input; the AC input passes the house's
		// remaining load through.
		inverter := max(0, -ac)
		inOut += max(0, house-inverter) * h
		inInv += max(0, ac) * h
		invOut += min(inverter, house) * h
		invIn += max(0, inverter-house) * h
		soc += batt * h / capacity * 100
		solar, house, heat, batt, grid = round(solar, 3), round(house, 3), round(heat, 3), round(batt, 3), round(grid, 3)
		s.solar = append(s.solar, solar)
		s.house = append(s.house, house)
		s.heatPump = append(s.heatPump, heat)
		s.battery = append(s.battery, batt)
		s.grid = append(s.grid, grid)
		s.soc = append(s.soc, soc)
		s.solarKWh = append(s.solarKWh, round(solarC+solar*h, 2))
		s.importKWh = append(s.importKWh, round(imp+max(grid, 0)*h, 2))
		s.exportKWh = append(s.exportKWh, round(exp+max(-grid, 0)*h, 2))
		s.chargedKWh = append(s.chargedKWh, round(chg+max(batt, 0)*h, 2))
		s.dischargedKWh = append(s.dischargedKWh, round(dis+max(-batt, 0)*h, 2))
		// Summed unrounded, read to 0.01 kWh.
		s.acInToOut = append(s.acInToOut, inOut)
		s.acInToInverter = append(s.acInToInverter, inInv)
		s.inverterToOut = append(s.inverterToOut, invOut)
		s.inverterToAcIn = append(s.inverterToAcIn, invIn)
	}
}

// hour is t's hour of the day in CET, fractional.
func hour(t time.Time) float64 {
	t = t.UTC().Add(time.Hour)
	return float64(t.Hour()) + float64(t.Minute())/60
}

// season is 1 at midsummer and -1 at midwinter.
func season(t time.Time) float64 {
	return math.Cos(2 * math.Pi * float64(t.YearDay()-172) / 365.25)
}

// cloud is a day's share of clear sky, 0.2 to 1, the same all day.
func cloud(t time.Time) float64 {
	d := float64(t.UTC().Unix() / 86400)
	x := math.Sin(d*12.9898) * 43758.5453
	return 0.2 + 0.8*(x-math.Floor(x))
}

// sun is the 10 kWp roof's output: a sine from sunrise to sunset, higher
// and longer in summer, dimmed on cloudy days and flickering on the
// cloudiest.
func sun(t time.Time) float64 {
	length := 12 + 4*season(t)
	rise := 12.5 - length/2
	x := (hour(t) - rise) / length
	if x <= 0 || x >= 1 {
		return 0
	}
	peak := 8.6 * (0.62 + 0.38*season(t)) * cloud(t)
	p := peak * math.Pow(math.Sin(math.Pi*x), 1.3)
	if cloud(t) < 0.55 {
		p *= 0.75 + 0.25*math.Sin(float64(t.Unix()/300)*1.7)
	}
	return max(0, p)
}

func bump(h, at, width float64) float64 {
	x := (h - at) / width
	return math.Exp(-x * x)
}

// houseLoad is a base load with breakfast, lunch and the evening.
func houseLoad(t time.Time) float64 {
	h := hour(t)
	return 0.28 + 1.3*bump(h, 7.3, 0.6) + 0.7*bump(h, 12.3, 0.7) + 1.2*bump(h, 19, 1.4)
}

// heatPumpLoad modulates with the cold: idle in summer, up to ~1.4 kW in
// midwinter, more at night and in the early morning.
func heatPumpLoad(t time.Time) float64 {
	cold := min(1, max(0, -season(t)+0.2))
	if cold == 0 {
		return 0.03
	}
	h := hour(t)
	return 0.03 + cold*(0.55+0.45*bump(h, 5, 3)+0.25*math.Sin(float64(t.Unix()/300)*0.9))
}
