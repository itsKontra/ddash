package appstate_test

import (
	"bytes"
	"context"
	"database/sql"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

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
		gate, err := appstate.Open(t.Context(), db, slog.New(slog.DiscardHandler))
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

// openGate opens a Gate over a new core DB, logging to the returned buffer.
func openGate(t *testing.T) (*appstate.Gate, *sql.DB, *bytes.Buffer) {
	t.Helper()
	db, err := coredb.Open(t.Context(), filepath.Join(t.TempDir(), "core.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	var logs bytes.Buffer
	gate, err := appstate.Open(t.Context(), db, slog.New(slog.NewTextHandler(&logs, nil)))
	if err != nil {
		t.Fatal(err)
	}
	return gate, db, &logs
}

// The caller reads only "audit log unavailable"; the log must say why.
func TestAWriteTheAuditLogCantMakeIsLoggedWithItsReason(t *testing.T) {
	action := appstate.Action{Chain: "chain", Target: "core", Method: install, Request: &corepb.InstallRequest{Plugin: "hello"}}

	t.Run("completing an entry", func(t *testing.T) {
		gate, db, logs := openGate(t)
		done, err := gate.Admit(t.Context(), auth.Actor{Kind: auth.Core}, action)
		if err != nil {
			t.Fatal(err)
		}
		db.Close()

		done(nil)

		for _, want := range []string{"level=WARN", "wasn't completed", "method=Install", "outcome=ok", "database is closed"} {
			if !strings.Contains(logs.String(), want) {
				t.Errorf("log = %q, want it to hold %q", logs, want)
			}
		}
	})
	t.Run("admitting an Action", func(t *testing.T) {
		gate, db, logs := openGate(t)
		db.Close()

		_, err := gate.Admit(t.Context(), auth.Actor{Kind: auth.Core}, action)

		if status.Code(err) != codes.Unavailable || status.Convert(err).Message() != "audit log unavailable" {
			t.Errorf("Admit = %v, want Unavailable: audit log unavailable", err)
		}
		for _, want := range []string{"level=WARN", "target=core", "method=Install", "database is closed"} {
			if !strings.Contains(logs.String(), want) {
				t.Errorf("log = %q, want it to hold %q", logs, want)
			}
		}
	})
	t.Run("recording an event", func(t *testing.T) {
		gate, db, logs := openGate(t)
		db.Close()

		err := gate.Record(t.Context(), auth.Actor{Kind: auth.Operator, Name: "ada"}, appstate.Event{Chain: "chain", Target: "core", Method: "SignIn"})

		if status.Code(err) != codes.Unavailable {
			t.Errorf("Record = %v, want Unavailable", err)
		}
		for _, want := range []string{"level=WARN", "method=SignIn", "database is closed"} {
			if !strings.Contains(logs.String(), want) {
				t.Errorf("log = %q, want it to hold %q", logs, want)
			}
		}
	})
	t.Run("the daily sweep", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			gate, db, logs := openGate(t)
			db.Close()
			ctx, stop := context.WithCancel(t.Context())
			defer stop()

			go gate.SweepDaily(ctx)
			synctest.Wait()

			for _, want := range []string{"level=WARN", "daily sweep failed", "database is closed"} {
				if !strings.Contains(logs.String(), want) {
					t.Errorf("log = %q, want it to hold %q", logs, want)
				}
			}
		})
	})
}

func TestListingTheTargetsReadsAnIndexNotTheTable(t *testing.T) {
	_, db, _ := openGate(t)
	rows, err := db.QueryContext(t.Context(), `EXPLAIN QUERY PLAN `+appstate.TargetsQuery)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var plan []string
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		plan = append(plan, detail)
	}
	got := strings.Join(plan, "; ")
	if !strings.Contains(got, "USING COVERING INDEX audit_entry_target") || strings.Contains(got, "TEMP B-TREE") {
		t.Errorf("plan = %q, want the audit_entry_target index and no temporary B-tree", got)
	}
}
