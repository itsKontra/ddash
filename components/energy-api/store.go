package energyapi

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pb "github.com/itsKontra/ddash/components/energy-api/energyapipb"
)

// store is the cache of attributed hours in the Component's DB. InfluxDB
// is the source of truth: deleting the DB only costs attributing the
// hours again. Times are Unix seconds.
type store struct{ db *sql.DB }

// cachedHour is an hour as the cache has it: by which version and when
// it was attributed.
type cachedHour struct {
	hour
	algo int
	at   time.Time
}

// valid tells whether the hour may be served at now: of this version, and
// final (attributed once late readings stopped arriving) or fresh.
func (h cachedHour) valid(now time.Time) bool {
	if h.algo != algoVersion {
		return false
	}
	return !h.at.Before(h.hour.at.Add(time.Hour+settle)) || now.Sub(h.at) < refresh
}

// columns are the hour table's energy columns, in the order of fields.
const columns = `solar, grid_import, grid_export, battery_charged, battery_discharged,
	solar_used, solar_battery, solar_export, battery_used, battery_export, grid_used, grid_battery,
	charge_loss, discharge_loss, ac_out, house, heat_pump, unsplit`

func fields(t *pb.Totals) []any {
	f := t.Flows
	return []any{t.Solar, t.GridImport, t.GridExport, t.BatteryCharged, t.BatteryDischarged,
		f.SolarUsed, f.SolarBattery, f.SolarExport, f.BatteryUsed, f.BatteryExport, f.GridUsed, f.GridBattery,
		f.ChargeLoss, f.DischargeLoss, f.AcOut, t.House, t.HeatPump, t.Unsplit}
}

func scanFields(t *pb.Totals) []any {
	f := t.Flows
	return []any{&t.Solar, &t.GridImport, &t.GridExport, &t.BatteryCharged, &t.BatteryDischarged,
		&f.SolarUsed, &f.SolarBattery, &f.SolarExport, &f.BatteryUsed, &f.BatteryExport, &f.GridUsed, &f.GridBattery,
		&f.ChargeLoss, &f.DischargeLoss, &f.AcOut, &t.House, &t.HeatPump, &t.Unsplit}
}

func dbErr(err error) error {
	if err == nil {
		return nil
	}
	return status.Errorf(codes.Internal, "energy-api's cache: %v", err)
}

// source makes the cache hold the hours of this InfluxDB and bucket: it
// is cleared when either changed.
func (s *store) source(ctx context.Context, url, bucket string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return dbErr(err)
	}
	defer tx.Rollback()
	var was, wasBucket string
	switch err := tx.QueryRowContext(ctx, "SELECT url, bucket FROM source").Scan(&was, &wasBucket); err {
	case nil:
		if was == url && wasBucket == bucket {
			return nil
		}
	case sql.ErrNoRows:
	default:
		return dbErr(err)
	}
	for _, q := range []string{"DELETE FROM hour", "DELETE FROM source"} {
		if _, err := tx.ExecContext(ctx, q); err != nil {
			return dbErr(err)
		}
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO source (url, bucket) VALUES (?, ?)", url, bucket); err != nil {
		return dbErr(err)
	}
	return dbErr(tx.Commit())
}

// load returns the cached hours starting in [from, to) by their start.
func (s *store) load(ctx context.Context, from, to time.Time) (map[time.Time]cachedHour, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT at, algo, attributed_at, estimated, "+columns+" FROM hour WHERE at >= ? AND at < ?", from.Unix(), to.Unix())
	if err != nil {
		return nil, dbErr(err)
	}
	defer rows.Close()
	out := map[time.Time]cachedHour{}
	for rows.Next() {
		var at, attributed int64
		h := cachedHour{hour: hour{t: &pb.Totals{Flows: &pb.Flows{}}}}
		if err := rows.Scan(append([]any{&at, &h.algo, &attributed, &h.t.Flows.Estimated}, scanFields(h.t)...)...); err != nil {
			return nil, dbErr(err)
		}
		h.hour.at, h.at = time.Unix(at, 0).UTC(), time.Unix(attributed, 0)
		out[h.hour.at] = h
	}
	return out, dbErr(rows.Err())
}

// save caches hours as attributed at now.
func (s *store) save(ctx context.Context, hours []hour, now time.Time) error {
	if len(hours) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return dbErr(err)
	}
	defer tx.Rollback()
	stmt, err := tx.PrepareContext(ctx, fmt.Sprintf("INSERT OR REPLACE INTO hour (at, algo, attributed_at, estimated, %s) VALUES (?, ?, ?, ?%s)", columns, strings.Repeat(", ?", len(fields(&pb.Totals{Flows: &pb.Flows{}})))))
	if err != nil {
		return dbErr(err)
	}
	defer stmt.Close()
	for _, h := range hours {
		if _, err := stmt.ExecContext(ctx, append([]any{h.at.Unix(), algoVersion, now.Unix(), h.t.Flows.Estimated}, fields(h.t)...)...); err != nil {
			return dbErr(err)
		}
	}
	return dbErr(tx.Commit())
}
