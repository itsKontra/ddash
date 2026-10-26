package checks

import (
	"io/fs"
	"path/filepath"
	"testing"
	"testing/synctest"
	"time"

	"github.com/itsKontra/ddash/components/checks/checkspb"
	"github.com/itsKontra/ddash/sdk/migrate"
)

// Maintenance (#49) has no Action yet, so its part of the uptime formula is
// checked on the store: beats in Maintenance count as neither up nor down.
func TestUptimeLeavesOutBeatsInMaintenance(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := testStore(t)
		ch := &check{spec: spec{typ: checkspb.CheckType_CHECK_TYPE_TCP, target: "nas:22"}, standing: standing{state: checkspb.State_STATE_PENDING, since: now()}}
		id, err := s.create(t.Context(), ch)
		if err != nil {
			t.Fatal(err)
		}
		st := ch.standing
		for _, b := range []struct {
			r     result
			maint bool
		}{
			{result{up: true, latency: 10 * time.Millisecond}, false},
			{result{reason: "timeout"}, true},
			{result{reason: "timeout"}, true},
			{result{up: true, latency: 90 * time.Millisecond}, true},
			{result{reason: "timeout"}, false}, // pending
		} {
			time.Sleep(Interval)
			next := st.after(b.r, now())
			if err := s.recordBeat(t.Context(), id, b.r, next, next.state != st.state, b.maint); err != nil {
				t.Fatal(err)
			}
			st = next
		}

		u, err := s.uptime(t.Context(), id, periods[0], periods[0].from(now()))
		if err != nil {
			t.Fatal(err)
		}
		if u.GetUp() != 1 || u.GetPending() != 1 || u.GetMaintenance() != 3 || u.GetRatio() != 1 {
			t.Errorf("up %d, pending %d, maintenance %d, ratio %v; want 1, 1, 3 and 1", u.GetUp(), u.GetPending(), u.GetMaintenance(), u.GetRatio())
		}
		if l := u.GetLatency(); l.GetAvgMs() != 10 || l.GetMaxMs() != 10 {
			t.Errorf("latency %v, want only the counted up beat's 10 ms", l)
		}
	})
}

func testStore(t *testing.T) *store {
	t.Helper()
	db, err := migrate.Open(filepath.Join(t.TempDir(), "checks.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	sub, err := fs.Sub(migrations, "migrations")
	if err != nil {
		t.Fatal(err)
	}
	if err := migrate.Apply(t.Context(), db, sub); err != nil {
		t.Fatal(err)
	}
	return &store{db}
}
