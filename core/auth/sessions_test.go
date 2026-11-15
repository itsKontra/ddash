package auth

import (
	"context"
	"log/slog"
	"path/filepath"
	"testing"
	"testing/synctest"
	"time"

	"github.com/itsKontra/ddash/core/coredb"
)

// lastSeen reads the one session's last_seen.
func lastSeen(t *testing.T, s sessions) time.Time {
	t.Helper()
	var ms int64
	if err := s.db.QueryRow(`SELECT last_seen FROM session`).Scan(&ms); err != nil {
		t.Fatal(err)
	}
	return time.UnixMilli(ms)
}

// Every getter refresh and every asset is a request behind sign-in: they
// read the session, and only the first one a minute writes it.
func TestARequestWritesTheSessionAtMostOnceAMinute(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx := context.Background()
		db, err := coredb.Open(ctx, filepath.Join(t.TempDir(), "core.db"))
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		s := sessions{db: db, log: slog.New(slog.DiscardHandler)}
		token, err := s.create(ctx, Actor{Kind: Operator, Name: "ada", Sub: "sub-1"})
		if err != nil {
			t.Fatal(err)
		}
		signedIn := lastSeen(t, s)

		time.Sleep(TouchEvery - time.Second)
		op, ok, err := s.touch(ctx, token)
		if err != nil || !ok || op.Name != "ada" || op.Sub != "sub-1" {
			t.Fatalf("touch = %+v, %v, %v", op, ok, err)
		}
		if got := lastSeen(t, s); !got.Equal(signedIn) {
			t.Errorf("a request %v after the last write moved last_seen by %v", TouchEvery-time.Second, got.Sub(signedIn))
		}

		time.Sleep(time.Second)
		if _, ok, err := s.touch(ctx, token); err != nil || !ok {
			t.Fatalf("touch = %v, %v", ok, err)
		}
		if got := lastSeen(t, s); got.Sub(signedIn) != TouchEvery {
			t.Errorf("a request %v after the last write left last_seen at +%v", TouchEvery, got.Sub(signedIn))
		}
	})
}
