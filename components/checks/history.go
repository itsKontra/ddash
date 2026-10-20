package checks

import (
	"context"
	"database/sql"
	"time"

	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/itsKontra/ddash/components/checks/checkspb"
)

// How long history is kept. A daily sweep deletes what is older.
const (
	// BeatsKept is how long every beat stays in check_event.
	BeatsKept = 24 * time.Hour
	// ChangesKept is how long the beats that changed a Check's state stay.
	ChangesKept = 365 * 24 * time.Hour
	// SweepInterval is how often the sweep runs (plus sdk.Every's jitter);
	// it also runs on Start.
	SweepInterval = 24 * time.Hour
)

// period is one bucket size of check_stat, and the window read at it.
type period struct {
	name   string // check_stat.period
	window checkspb.Window
	size   time.Duration
	// count is how many buckets the window spans, the last holding now.
	count int
	// kept is how long the sweep keeps the period's buckets.
	kept time.Duration
}

// periods are check_stat's bucket sizes, in the order of the windows.
var periods = []period{
	{"minute", checkspb.Window_WINDOW_24H, time.Minute, 24 * 60, 24 * time.Hour},
	{"hour", checkspb.Window_WINDOW_30D, time.Hour, 30 * 24, 30 * 24 * time.Hour},
	{"day", checkspb.Window_WINDOW_365D, 24 * time.Hour, 365, 365 * 24 * time.Hour},
}

// start is the start of the bucket holding at. Days are UTC days.
func (p period) start(at time.Time) time.Time { return at.UTC().Truncate(p.size) }

// from is the start of the window's first bucket at now.
func (p period) from(now time.Time) time.Time {
	return p.start(now).Add(-time.Duration(p.count-1) * p.size)
}

// periodOf is the period read for window w; 24 h when unspecified.
func periodOf(w checkspb.Window) period {
	for _, p := range periods {
		if p.window == w {
			return p
		}
	}
	return periods[0]
}

// addToBuckets counts a beat at at in its minute, hour and day buckets: up
// or down (a failed beat while pending is down too), or maint in
// Maintenance. Latency is folded in from counted up beats only.
func addToBuckets(ctx context.Context, tx *sql.Tx, id int64, at time.Time, r result, maint bool) error {
	var up, down, inMaint int
	var latency any
	switch {
	case maint:
		inMaint = 1
	case r.up:
		up, latency = 1, latencyMs(r.latency)
	default:
		down = 1
	}
	for _, p := range periods {
		// Column names on the right of SET read the row before the update.
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO check_stat (check_id, period, bucket, up, down, maint, latency_avg, latency_min, latency_max)
			VALUES (?1, ?2, ?3, ?4, ?5, ?6, ?7, ?7, ?7)
			ON CONFLICT (check_id, period, bucket) DO UPDATE SET
			  up = up + excluded.up,
			  down = down + excluded.down,
			  maint = maint + excluded.maint,
			  latency_avg = CASE WHEN excluded.latency_avg IS NULL THEN latency_avg
			                ELSE (COALESCE(latency_avg, 0) * up + excluded.latency_avg) / (up + 1) END,
			  latency_min = CASE WHEN latency_min IS NULL OR excluded.latency_min < latency_min
			                THEN COALESCE(excluded.latency_min, latency_min) ELSE latency_min END,
			  latency_max = CASE WHEN latency_max IS NULL OR excluded.latency_max > latency_max
			                THEN COALESCE(excluded.latency_max, latency_max) ELSE latency_max END`,
			id, p.name, ms(p.start(at)), up, down, inMaint, latency); err != nil {
			return err
		}
	}
	return nil
}

func (c *component) GetHistory(ctx context.Context, req *checkspb.GetHistoryRequest) (*checkspb.GetHistoryResponse, error) {
	if err := c.known(req.GetId()); err != nil {
		return nil, err
	}
	id, at := req.GetId(), now()
	resp := &checkspb.GetHistoryResponse{}
	for _, p := range periods {
		u, err := c.db.uptime(ctx, id, p, p.from(at))
		if err != nil {
			return nil, err
		}
		resp.Uptime = append(resp.Uptime, u)
	}
	p := periodOf(req.GetWindow())
	from := p.from(at)
	resp.Window, resp.Bucket, resp.From = p.window, durationpb.New(p.size), timestamppb.New(from)
	var err error
	if resp.Buckets, err = c.db.buckets(ctx, id, p, from); err != nil {
		return nil, err
	}
	if resp.Changes, err = c.db.changes(ctx, id, from); err != nil {
		return nil, err
	}
	return resp, nil
}

func (c *component) ListBeats(ctx context.Context, req *checkspb.ListBeatsRequest) (*checkspb.ListBeatsResponse, error) {
	if err := c.known(req.GetId()); err != nil {
		return nil, err
	}
	at := now()
	beats, err := c.db.beats(ctx, req.GetId(), at.Add(-BeatsKept), at.Add(-ChangesKept))
	if err != nil {
		return nil, err
	}
	return &checkspb.ListBeatsResponse{Beats: beats}, nil
}

// known is NotFound unless Check id exists.
func (c *component) known(id int64) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, err := c.get(id)
	return err
}

// sweep deletes what history keeps no longer: beats older than BeatsKept
// unless they changed the state, those older than ChangesKept, and each
// period's buckets older than it keeps them.
func (c *component) sweep(ctx context.Context) {
	if err := c.db.sweep(ctx, now()); err != nil && ctx.Err() == nil {
		c.log.Error("sweep history", "err", err)
	}
}

func (s *store) sweep(ctx context.Context, at time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM check_event WHERE (NOT changed AND at < ?) OR at < ?`,
		ms(at.Add(-BeatsKept)), ms(at.Add(-ChangesKept))); err != nil {
		return err
	}
	for _, p := range periods {
		if _, err := tx.ExecContext(ctx, `DELETE FROM check_stat WHERE period = ? AND bucket < ?`,
			p.name, ms(at.Add(-p.kept))); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// uptime sums Check id's p buckets from from on.
func (s *store) uptime(ctx context.Context, id int64, p period, from time.Time) (*checkspb.Uptime, error) {
	u := &checkspb.Uptime{Window: p.window}
	var weighted, lo, hi sql.NullFloat64
	if err := s.db.QueryRowContext(ctx, `
		SELECT COALESCE(sum(up), 0), COALESCE(sum(down), 0), COALESCE(sum(maint), 0),
		       sum(latency_avg * up), min(latency_min), max(latency_max)
		FROM check_stat WHERE check_id = ? AND period = ? AND bucket >= ?`,
		id, p.name, ms(from)).Scan(&u.Up, &u.Down, &u.Maintenance, &weighted, &lo, &hi); err != nil {
		return nil, err
	}
	if counted := u.Up + u.Down; counted > 0 {
		ratio := float64(u.Up) / float64(counted)
		u.Ratio = &ratio
	}
	if weighted.Valid && u.Up > 0 {
		u.Latency = &checkspb.Latency{AvgMs: weighted.Float64 / float64(u.Up), MinMs: lo.Float64, MaxMs: hi.Float64}
	}
	return u, nil
}

// buckets reads Check id's p buckets from from on, oldest first.
func (s *store) buckets(ctx context.Context, id int64, p period, from time.Time) ([]*checkspb.Bucket, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT bucket, up, down, maint, latency_avg, latency_min, latency_max
		FROM check_stat WHERE check_id = ? AND period = ? AND bucket >= ?
		ORDER BY bucket`, id, p.name, ms(from))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*checkspb.Bucket
	for rows.Next() {
		var (
			b           checkspb.Bucket
			start       int64
			avg, lo, hi sql.NullFloat64
		)
		if err := rows.Scan(&start, &b.Up, &b.Down, &b.Maintenance, &avg, &lo, &hi); err != nil {
			return nil, err
		}
		b.Start = timestamppb.New(time.UnixMilli(start))
		if avg.Valid {
			b.Latency = &checkspb.Latency{AvgMs: avg.Float64, MinMs: lo.Float64, MaxMs: hi.Float64}
		}
		out = append(out, &b)
	}
	return out, rows.Err()
}

// changes reads Check id's state changes from from on, newest first.
func (s *store) changes(ctx context.Context, id int64, from time.Time) ([]*checkspb.Beat, error) {
	return s.readBeats(ctx, `
		SELECT at, up, state, changed, COALESCE(latency_ms, 0), reason FROM check_event
		WHERE check_id = ? AND changed AND at >= ?
		ORDER BY at DESC, id DESC`, id, ms(from))
}

// beats reads Check id's beats since all, and its state changes since
// changes, oldest first.
func (s *store) beats(ctx context.Context, id int64, all, changes time.Time) ([]*checkspb.Beat, error) {
	return s.readBeats(ctx, `
		SELECT at, up, state, changed, COALESCE(latency_ms, 0), reason FROM check_event
		WHERE check_id = ? AND (at >= ? OR (changed AND at >= ?))
		ORDER BY at, id`, id, ms(all), ms(changes))
}

func (s *store) readBeats(ctx context.Context, query string, args ...any) ([]*checkspb.Beat, error) {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*checkspb.Beat
	for rows.Next() {
		var (
			b     checkspb.Beat
			at    int64
			state string
		)
		if err := rows.Scan(&at, &b.Up, &state, &b.Changed, &b.LatencyMs, &b.Reason); err != nil {
			return nil, err
		}
		b.At, b.State = timestamppb.New(time.UnixMilli(at)), key(states, state)
		out = append(out, &b)
	}
	return out, rows.Err()
}
