package checks

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"github.com/itsKontra/ddash/components/checks/checkspb"
	workloadpb "github.com/itsKontra/ddash/proto/ddash/workload"
)

// store keeps Checks and their beats in the Component's DB. Times are
// Unix ms.
type store struct{ db *sql.DB }

var (
	types = map[checkspb.CheckType]string{
		checkspb.CheckType_CHECK_TYPE_TCP:  "tcp",
		checkspb.CheckType_CHECK_TYPE_ICMP: "icmp",
		checkspb.CheckType_CHECK_TYPE_DNS:  "dns",
		checkspb.CheckType_CHECK_TYPE_HTTP: "http",
	}
	states  = map[checkspb.State]string{checkspb.State_STATE_PENDING: "pending", checkspb.State_STATE_UP: "up", checkspb.State_STATE_DOWN: "down"}
	records = map[checkspb.RecordType]string{}
	kinds   = map[workloadpb.Kind]string{
		workloadpb.Kind_KIND_LXC:      "lxc",
		workloadpb.Kind_KIND_VM:       "vm",
		workloadpb.Kind_KIND_DOCKER:   "docker",
		workloadpb.Kind_KIND_EXTERNAL: "external",
	}
)

func init() {
	for v, name := range checkspb.RecordType_name {
		if v != 0 {
			records[checkspb.RecordType(v)] = strings.TrimPrefix(name, "RECORD_TYPE_")
		}
	}
}

// key returns the key of m whose value is v.
func key[K comparable](m map[K]string, v string) K {
	for k, s := range m {
		if s == v {
			return k
		}
	}
	var zero K
	return zero
}

func ms(t time.Time) int64 { return t.UnixMilli() }

// now is the time to the ms, as the DB keeps it.
func now() time.Time { return time.Now().Truncate(time.Millisecond) }

// load reads every Check with its last standing.
func (s *store) load(ctx context.Context) ([]*check, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, name, type, target, dns_name, dns_record_type, muted, disabled,
		       state, failures, since, last_beat, latency_ms, reason,
		       COALESCE(app_id, ''), app_enabled, gone_at, cert_expires,
		       workload_kind, workload_host, workload_id
		FROM checks`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*check
	for rows.Next() {
		var (
			ch                 check
			typ, record, state string
			wlKind             string
			since              int64
			lastBeat           sql.NullInt64
			latency            float64
			goneAt, certExp    sql.NullInt64
		)
		if err := rows.Scan(&ch.id, &ch.spec.name, &typ, &ch.spec.target, &ch.spec.dnsName, &record, &ch.muted, &ch.disabled,
			&state, &ch.standing.failures, &since, &lastBeat, &latency, &ch.standing.reason,
			&ch.appID, &ch.appEnabled, &goneAt, &certExp,
			&wlKind, &ch.workload.host, &ch.workload.id); err != nil {
			return nil, err
		}
		ch.goneAt, ch.standing.certExpires = fromMs(goneAt), fromMs(certExp)
		ch.workload.kind = key(kinds, wlKind)
		ch.spec.typ, ch.spec.recordType, ch.standing.state = key(types, typ), key(records, record), key(states, state)
		ch.standing.since = time.UnixMilli(since)
		if lastBeat.Valid {
			ch.standing.lastBeat = time.UnixMilli(lastBeat.Int64)
		}
		ch.standing.latency = time.Duration(latency * float64(time.Millisecond))
		out = append(out, &ch)
	}
	return out, rows.Err()
}

// create stores a new manual Check and returns its id.
func (s *store) create(ctx context.Context, ch *check) (int64, error) {
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO checks (kind, name, type, target, dns_name, dns_record_type, state, since)
		VALUES ('manual', ?, ?, ?, ?, ?, ?, ?)`,
		ch.spec.name, types[ch.spec.typ], ch.spec.target, ch.spec.dnsName, records[ch.spec.recordType],
		states[ch.standing.state], ms(ch.standing.since))
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// update stores a Check's new spec and standing.
func (s *store) update(ctx context.Context, id int64, sp spec, st standing) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE checks SET name = ?, type = ?, target = ?, dns_name = ?, dns_record_type = ?,
		       state = ?, failures = ?, since = ?, last_beat = ?, latency_ms = ?, reason = ?
		WHERE id = ?`,
		sp.name, types[sp.typ], sp.target, sp.dnsName, records[sp.recordType],
		states[st.state], st.failures, ms(st.since), lastBeat(st), latencyMs(st.latency), st.reason, id)
	return err
}

// setFlag sets muted or disabled.
func (s *store) setFlag(ctx context.Context, id int64, flag string, on bool) error {
	_, err := s.db.ExecContext(ctx, `UPDATE checks SET `+flag+` = ? WHERE id = ?`, on, id)
	return err
}

// delete removes a Check, its beats and its last-notified state.
func (s *store) delete(ctx context.Context, id int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM checks WHERE id = ?`, id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM notified WHERE item IN (?, ?)`, checkItem(id), certItem(id)); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM maintenance WHERE kind = 'check' AND check_id = ?`, id); err != nil {
		return err
	}
	return tx.Commit()
}

// loadNotified reads the last-notified state of every item.
func (s *store) loadNotified(ctx context.Context) (map[string]notified, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT item, state, since, reason FROM notified`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]notified{}
	for rows.Next() {
		var (
			item, state string
			since       int64
			n           notified
		)
		if err := rows.Scan(&item, &state, &since, &n.reason); err != nil {
			return nil, err
		}
		n.state, n.since = mark(state), time.UnixMilli(since)
		out[item] = n
	}
	return out, rows.Err()
}

// setNotified stores the state item was last notified in.
func (s *store) setNotified(ctx context.Context, item string, n notified) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO notified (item, state, since, reason) VALUES (?, ?, ?, ?)
		ON CONFLICT (item) DO UPDATE SET state = excluded.state, since = excluded.since, reason = excluded.reason`,
		item, string(n.state), ms(n.since), n.reason)
	return err
}

// recordBeat adds a beat to check_event and its minute, hour and day
// buckets in check_stat (as maint when in Maintenance, as pending when it
// left the Check pending), and stores the standing after it, together.
func (s *store) recordBeat(ctx context.Context, id int64, r result, st standing, changed, maint bool) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var latency any
	if r.up {
		latency = latencyMs(r.latency)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO check_event (check_id, at, up, state, changed, latency_ms, reason, maint)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		id, ms(st.lastBeat), r.up, states[st.state], changed, latency, r.reason, maint); err != nil {
		return err
	}
	if err := addToBuckets(ctx, tx, id, st.lastBeat, r, st.state, maint); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE checks SET state = ?, failures = ?, since = ?, last_beat = ?, latency_ms = ?, reason = ?, cert_expires = ?
		WHERE id = ?`,
		states[st.state], st.failures, ms(st.since), lastBeat(st), latencyMs(st.latency), st.reason, orNull(st.certExpires), id); err != nil {
		return err
	}
	return tx.Commit()
}

func lastBeat(st standing) any { return orNull(st.lastBeat) }

// orNull is t in Unix ms, or NULL for the zero time.
func orNull(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return ms(t)
}

// fromMs reads a nullable Unix ms column; NULL is the zero time.
func fromMs(v sql.NullInt64) time.Time {
	if !v.Valid {
		return time.Time{}
	}
	return time.UnixMilli(v.Int64)
}

// saveAppChecks stores App Checks as SetAppChecks left them, together:
// it inserts those without an id, setting it, and updates the rest.
func (s *store) saveAppChecks(ctx context.Context, chs []*check) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, ch := range chs {
		st := ch.standing
		if ch.id == 0 {
			res, err := tx.ExecContext(ctx, `
				INSERT INTO checks (kind, app_id, name, type, target, app_enabled, state, since,
				                    workload_kind, workload_host, workload_id)
				VALUES ('app', ?, ?, 'http', ?, ?, ?, ?, ?, ?, ?)`,
				ch.appID, ch.spec.name, ch.spec.target, ch.appEnabled, states[st.state], ms(st.since),
				kinds[ch.workload.kind], ch.workload.host, ch.workload.id)
			if err != nil {
				return err
			}
			if ch.id, err = res.LastInsertId(); err != nil {
				return err
			}
			continue
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE checks SET name = ?, target = ?, app_enabled = ?, gone_at = ?,
			       state = ?, failures = ?, since = ?, last_beat = ?, latency_ms = ?, reason = ?, cert_expires = ?,
			       workload_kind = ?, workload_host = ?, workload_id = ?
			WHERE id = ?`,
			ch.spec.name, ch.spec.target, ch.appEnabled, orNull(ch.goneAt),
			states[st.state], st.failures, ms(st.since), lastBeat(st), latencyMs(st.latency), st.reason, orNull(st.certExpires),
			kinds[ch.workload.kind], ch.workload.host, ch.workload.id,
			ch.id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func latencyMs(d time.Duration) float64 { return float64(d) / float64(time.Millisecond) }
