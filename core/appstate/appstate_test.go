package appstate_test

import (
	"context"
	"path/filepath"
	"testing"
	"testing/synctest"
	"time"

	"github.com/itsKontra/ddash/core/appstate"
	"github.com/itsKontra/ddash/core/auth"
	"github.com/itsKontra/ddash/core/coredb"
	corepb "github.com/itsKontra/ddash/proto/ddash/core"
)

var install = corepb.File_ddash_core_core_proto.Services().ByName("CoreService").Methods().ByName("Install")

func TestEntriesOlderThanAYearAreSweptDaily(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		db, err := coredb.Open(t.Context(), filepath.Join(t.TempDir(), "core.db"))
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		gate, err := appstate.Open(t.Context(), db)
		if err != nil {
			t.Fatal(err)
		}
		ctx, stop := context.WithCancel(t.Context())
		defer stop()
		go gate.SweepDaily(ctx)
		done, err := gate.Admit(ctx, auth.Actor{Kind: auth.Core}, appstate.Action{
			Chain: "chain", Target: "core", Method: install, Request: &corepb.InstallRequest{Plugin: "hello"},
		})
		if err != nil {
			t.Fatal(err)
		}
		done(nil)
		count := func() int {
			entries, err := gate.Entries(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			return len(entries)
		}

		time.Sleep(364 * 24 * time.Hour)
		synctest.Wait()
		if n := count(); n != 1 {
			t.Errorf("after 364 days: %d entries, want the Install kept", n)
		}

		time.Sleep(2 * 24 * time.Hour)
		synctest.Wait()
		if n := count(); n != 0 {
			t.Errorf("after 366 days: %d entries, want the Install swept", n)
		}
	})
}
