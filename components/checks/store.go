package checks

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"github.com/itsKontra/ddash/components/checks/checkspb"
)

// store keeps Checks and their beats in the Component's DB. Times are
// Unix ms.
type store struct{ db *sql.DB }

var (
	types   = map[checkspb.CheckType]string{checkspb.CheckType_CHECK_TYPE_TCP: "tcp", checkspb.CheckType_CHECK_TYPE_ICMP: "icmp", checkspb.CheckType_CHECK_TYPE_DNS: "dns"}
	states  = map[checkspb.State]string{checkspb.State_STATE_PENDING: "pending", checkspb.State_STATE_UP: "up", checkspb.State_STATE_DOWN: "down"}
	records = map[checkspb.RecordType]string{}
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
		       state, failures, since, last_beat, latency_ms, reason
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
			since              int64
			lastBeat           sql.NullInt64
			latency            float64
		)
		if err := rows.Scan(&ch.id, &ch.spec.name, &typ, &ch.spec.target, &ch.spec.dnsName, &record, &ch.muted, &ch.disabled,
			&state, &ch.standing.failures, &since, &lastBeat, &latency, &ch.standing.reason); err != nil {
			return nil, err
		}
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

// delete removes a Check and its beats.
func (s *store) delete(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM checks WHERE id = ?`, id)
	return err
}

// recordBeat adds a beat to check_event and stores the standing after it,
// together.
func (s *store) recordBeat(ctx context.Context, id int64, r result, st standing, changed bool) error {
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
		INSERT INTO check_event (check_id, at, up, state, changed, latency_ms, reason)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		id, ms(st.lastBeat), r.up, states[st.state], changed, latency, r.reason); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE checks SET state = ?, failures = ?, since = ?, last_beat = ?, latency_ms = ?, reason = ?
		WHERE id = ?`,
		states[st.state], st.failures, ms(st.since), lastBeat(st), latencyMs(st.latency), st.reason, id); err != nil {
		return err
	}
	return tx.Commit()
}

func lastBeat(st standing) any {
	if st.lastBeat.IsZero() {
		return nil
	}
	return ms(st.lastBeat)
}

func latencyMs(d time.Duration) float64 { return float64(d) / float64(time.Millisecond) }
