package checks_test

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"
	"time"

	"github.com/itsKontra/ddash/components/checks/checkspb"
	"github.com/itsKontra/ddash/sdk/migrate"
	"github.com/itsKontra/ddash/sdk/sdktest"
)

// olderDB lays out a checks DB as the Release before App Checks left it: a
// manual Check (id 2; id 1 was deleted) with two beats and their buckets.
func olderDB(t *testing.T) string {
	t.Helper()
	old := fstest.MapFS{}
	for _, name := range []string{"0001_checks.sql", "0002_check_stat.sql"} {
		body, err := os.ReadFile(filepath.Join("migrations", name))
		if err != nil {
			t.Fatal(err)
		}
		old[name] = &fstest.MapFile{Data: body}
	}
	path := filepath.Join(t.TempDir(), "checks.db")
	db, err := migrate.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := migrate.Apply(t.Context(), db, old); err != nil {
		t.Fatal(err)
	}
	// Two beats a minute apart, two minutes ago.
	at := time.Now().Add(-2 * time.Minute).Truncate(time.Minute).UnixMilli()
	next := at + time.Minute.Milliseconds()
	for _, q := range []string{
		`INSERT INTO checks (id, kind, name, type, target, state, since, last_beat, latency_ms)
		 VALUES (1, 'manual', 'gone', 'tcp', 'old:22', 'pending', 0, NULL, 0)`,
		`DELETE FROM checks WHERE id = 1`,
		fmt.Sprintf(`INSERT INTO checks (id, kind, name, type, target, state, since, last_beat, latency_ms)
		 VALUES (2, 'manual', 'nas', 'tcp', '127.0.0.1:1', 'up', %d, %d, 12)`, at, next),
		fmt.Sprintf(`INSERT INTO check_event (check_id, at, up, state, changed, latency_ms)
		 VALUES (2, %d, 1, 'up', 1, 10), (2, %d, 1, 'up', 0, 12)`, at, next),
		fmt.Sprintf(`INSERT INTO check_stat (check_id, period, bucket, up, latency_avg, latency_min, latency_max)
		 VALUES (2, 'minute', %d, 1, 10, 10, 10), (2, 'minute', %d, 1, 12, 12, 12)`, at, next),
	} {
		if _, err := db.ExecContext(t.Context(), q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	return path
}

func TestAnUpgradeKeepsManualChecksAndTheirHistory(t *testing.T) {
	_, c := start(t, sdktest.WithDB(olderDB(t)))

	got := find(t, c, 2)
	if got.GetSpec().GetName() != "nas" || got.GetState() != checkspb.State_STATE_UP || got.GetAppId() != "" {
		t.Errorf("Check 2 = %v, want the manual nas Check, up", got)
	}
	// It is probed again at once (a failure, nothing listens there), so
	// only the first two beats are the migrated ones.
	if got := states(beats(t, c, 2)); len(got) < 2 || !equal(got[:2], []string{"up", "up"}) {
		t.Errorf("beats %v, want both migrated ones first", got)
	}
	h, err := c.GetHistory(t.Context(), &checkspb.GetHistoryRequest{Id: 2})
	if err != nil {
		t.Fatal(err)
	}
	if u := h.GetUptime()[0]; u.GetUp() != 2 || u.GetLatency().GetMaxMs() != 12 {
		t.Errorf("24 h uptime %v, want both buckets' 2 up beats", u)
	}

	// A new Check takes a fresh id: neither the live nor the deleted one.
	if id := create(t, c, tcp("198.18.1.10:22")).GetId(); id != 3 {
		t.Errorf("new Check id %d, want 3", id)
	}
}
