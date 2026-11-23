package energyapi

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/itsKontra/ddash/sdk"
)

// influx reads one bucket of an InfluxDB v2 server with a token, through
// Flux queries answered as CSV. It only ever reads.
type influx struct {
	base   string
	token  string
	bucket string
	http   *http.Client
	// org is the bucket's organisation ID, which a query needs. A
	// read-only token may list buckets but not organisations, so it comes
	// from the bucket.
	org *sdk.Cache[string]
}

func newInflux(base, token, bucket string, client *http.Client) *influx {
	x := &influx{base: strings.TrimSuffix(base, "/"), token: token, bucket: bucket, http: client}
	x.org = sdk.Cached(24*time.Hour, x.fetchOrg)
	return x
}

// row is one value of a query's answer.
type row struct {
	field string
	at    time.Time
	value float64
}

func (x *influx) fetchOrg(ctx context.Context) (string, error) {
	var resp struct {
		Buckets []struct {
			Name  string `json:"name"`
			OrgID string `json:"orgID"`
		} `json:"buckets"`
	}
	body, err := x.do(ctx, http.MethodGet, "/api/v2/buckets?name="+url.QueryEscape(x.bucket), nil, "application/json")
	if err != nil {
		return "", err
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return "", status.Errorf(codes.Unavailable, "InfluxDB answered buckets with something other than JSON: %v", err)
	}
	for _, b := range resp.Buckets {
		if b.Name == x.bucket {
			return b.OrgID, nil
		}
	}
	return "", status.Errorf(codes.FailedPrecondition, "bucket %s doesn't exist or the token can't read it", x.bucket)
}

// query runs a Flux query and returns its rows. Each must have _field,
// _time and _value columns.
func (x *influx) query(ctx context.Context, flux string) ([]row, error) {
	org, err := x.org.Get(ctx)
	if err != nil {
		return nil, err
	}
	req, _ := json.Marshal(map[string]any{
		"query": flux,
		"type":  "flux",
		// A plain CSV: a header per table, no annotations.
		"dialect": map[string]any{"header": true, "annotations": []string{}},
	})
	body, err := x.do(ctx, http.MethodPost, "/api/v2/query?orgID="+url.QueryEscape(org), req, "application/csv")
	if err != nil {
		return nil, err
	}
	return parseCSV(body)
}

// maxAnswer bounds an answer: a year of hourly power readings is ~5 MiB.
const maxAnswer = 64 << 20

// do sends one request. Its errors name InfluxDB's host, never the token.
func (x *influx) do(ctx context.Context, method, path string, body []byte, accept string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, method, x.base+path, bytes.NewReader(body))
	if err != nil {
		return nil, status.Errorf(codes.Internal, "building the request: %v", err)
	}
	host := req.URL.Host
	req.Header.Set("Authorization", "Token "+x.token)
	req.Header.Set("Accept", accept)
	req.Header.Set("User-Agent", "ddash")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := x.http.Do(req)
	if err != nil {
		var uerr *url.Error
		if errors.As(err, &uerr) {
			err = uerr.Err
		}
		return nil, status.Errorf(codes.Unavailable, "%s unreachable: %v", host, err)
	}
	defer resp.Body.Close()
	out, err := io.ReadAll(io.LimitReader(resp.Body, maxAnswer+1))
	if err != nil {
		return nil, status.Errorf(codes.Unavailable, "%s: reading the answer: %v", host, err)
	}
	if len(out) > maxAnswer {
		return nil, status.Errorf(codes.ResourceExhausted, "%s answered more than %d MiB", host, maxAnswer>>20)
	}
	if resp.StatusCode/100 != 2 {
		var e struct {
			Message string `json:"message"`
		}
		why := resp.Status
		if json.Unmarshal(out, &e) == nil && e.Message != "" {
			why += ": " + e.Message
		}
		code := codes.Unavailable
		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
			code = codes.PermissionDenied
		}
		return nil, status.Errorf(code, "InfluxDB answered %s", why)
	}
	return out, nil
}

// parseCSV reads InfluxDB's CSV without annotations: tables one after the
// other, each with its own header row. An error row fails the whole answer.
func parseCSV(body []byte) ([]row, error) {
	r := csv.NewReader(bytes.NewReader(body))
	r.FieldsPerRecord = -1
	r.ReuseRecord = true
	var out []row
	field, at, value, errCol := -1, -1, -1, -1
	for {
		rec, err := r.Read()
		if err == io.EOF {
			return out, nil
		}
		if err != nil {
			return nil, status.Errorf(codes.Unavailable, "InfluxDB answered unreadable CSV: %v", err)
		}
		if len(rec) > 1 && (rec[1] == "result" || rec[0] == "error") {
			field, at, value, errCol = -1, -1, -1, -1
			for i, name := range rec {
				switch name {
				case "_field":
					field = i
				case "_time":
					at = i
				case "_value":
					value = i
				case "error":
					errCol = i
				}
			}
			continue
		}
		if errCol >= 0 && errCol < len(rec) {
			return nil, status.Errorf(codes.Unavailable, "InfluxDB failed the query: %s", rec[errCol])
		}
		if field < 0 || at < 0 || value < 0 || max(field, at, value) >= len(rec) {
			continue
		}
		t, err := time.Parse(time.RFC3339Nano, rec[at])
		if err != nil {
			continue
		}
		v, err := strconv.ParseFloat(rec[value], 64)
		if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
			continue
		}
		out = append(out, row{field: rec[field], at: t, value: v})
	}
}

// series is one field Telegraf writes, with the range a plausible value
// (or window's mean) of it lies in; a counter has none.
type series struct {
	measurement string
	field       string
	min, max    float64
	counter     bool
}

const (
	// maxKW bounds every power reading and counter step: the inverter is
	// a 10 kW one and the house's connection allows ~17 kW. The meter's
	// "invalid" sentinel, 2147483.647, lies far beyond.
	maxKW = 25.0
)

func power(m, f string) series { return series{measurement: m, field: f, min: -maxKW, max: maxKW} }
func counter(m, f string) series {
	return series{measurement: m, field: f, counter: true}
}

// The fields energy-api reads: the Huawei SUN2000 and its grid meter
// over Modbus, and the Victron GX over Modbus TCP.
var (
	solarKW     = power("kW", "INVERTER_Pout")
	gridKW      = power("kW", "Grid_Pout") // positive while exporting
	batteryKW   = power("victron", "battery_power_kW")
	houseKW     = power("victron", "essential_load_kW")
	heatPumpKW  = power("victron", "ac_load_kW")
	loadsKW     = power("victron", "consumption_kW")
	socPct      = series{measurement: "victron", field: "battery_soc_pct", min: 0, max: 100}
	batteryV    = series{measurement: "victron", field: "battery_voltage_V", min: 0, max: 100}
	solarKWh    = counter("kWh", "Inverter_Energy_Yield")
	importKWh   = counter("kWh", "Grid_Energy_Use")
	exportKWh   = counter("kWh", "Grid_Energy_Yield")
	chargedKWh  = counter("victron", "charged_kWh")
	dischargedK = counter("victron", "discharged_kWh")
)

var (
	powerSeries = []series{solarKW, gridKW, batteryKW, houseKW, heatPumpKW, loadsKW, socPct}
	flowSeries  = append(append([]series{}, powerSeries...), batteryV)
)

// reduce is how a query reduces each field: to windows of every with fn
// ("mean" or "last"), or with every zero to one value by fn ("last" or
// "first"). A bounded query drops implausible readings before reducing;
// that keeps InfluxDB from reducing in its storage engine, which is ~100×
// faster, so a long range is read unbounded and judged after.
type reduce struct {
	every   time.Duration
	fn      string
	bounded bool
}

// flux builds the query for fields over [from, to): picked by measurement
// and field, implausible values dropped when bounded, then reduced. A
// window is stamped with its start.
func flux(bucket string, from, to time.Time, fields []series, r reduce) string {
	var b strings.Builder
	fmt.Fprintf(&b, "from(bucket: %s)\n", fluxString(bucket))
	fmt.Fprintf(&b, "  |> range(start: %s, stop: %s)\n", from.UTC().Format(time.RFC3339), to.UTC().Format(time.RFC3339))
	var pick, bound []string
	for _, s := range fields {
		pick = append(pick, fmt.Sprintf("(r._measurement == %s and r._field == %s)", fluxString(s.measurement), fluxString(s.field)))
		if s.counter {
			bound = append(bound, fmt.Sprintf("r._field == %s", fluxString(s.field)))
		} else {
			bound = append(bound, fmt.Sprintf("(r._field == %s and r._value >= %s and r._value <= %s)", fluxString(s.field), fluxFloat(s.min), fluxFloat(s.max)))
		}
	}
	fmt.Fprintf(&b, "  |> filter(fn: (r) => %s)\n", strings.Join(pick, " or "))
	if r.bounded {
		b.WriteString("  |> toFloat()\n")
		fmt.Fprintf(&b, "  |> filter(fn: (r) => %s)\n", strings.Join(bound, " or "))
	}
	if r.every > 0 {
		fmt.Fprintf(&b, "  |> aggregateWindow(every: %s, fn: %s, createEmpty: false, timeSrc: \"_start\")\n", fluxDuration(r.every), r.fn)
	} else {
		fmt.Fprintf(&b, "  |> %s()\n", r.fn)
	}
	b.WriteString(`  |> keep(columns: ["_time", "_field", "_value"])` + "\n")
	return b.String()
}

// plausible tells whether v lies in the series' range.
func (s series) plausible(v float64) bool { return s.counter || (v >= s.min && v <= s.max) }

func fluxString(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "${", `\${`)
	return `"` + r.Replace(s) + `"`
}

func fluxFloat(f float64) string { return strconv.FormatFloat(f, 'f', 1, 64) }

func fluxDuration(d time.Duration) string {
	if d%time.Hour == 0 {
		return fmt.Sprintf("%dh", d/time.Hour)
	}
	return fmt.Sprintf("%dm", d/time.Minute)
}
