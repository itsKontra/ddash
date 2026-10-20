package core_test

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/itsKontra/ddash/core/coretest"
	"github.com/itsKontra/ddash/core/internal/sample"
	"github.com/itsKontra/ddash/core/internal/sample/samplepb"
	"github.com/itsKontra/ddash/core/runtime"
	"github.com/itsKontra/ddash/sdk"
)

// A notebook is a greeter whose Act keeps req.note, when set, in its DB and
// answers every note it keeps, joined by "|", as the actor.
func notebook(extraMigrations ...string) sdk.Definition {
	return greeter(sample.Hooks{
		Migrations: sample.Migrations(extraMigrations...),
		Act: func(ctx context.Context, env *sdk.Env, req *samplepb.ActRequest) (*samplepb.ActResponse, error) {
			if req.GetNote() != "" {
				if _, err := env.DB.ExecContext(ctx, `INSERT INTO note (text) VALUES (?)`, req.GetNote()); err != nil {
					return nil, err
				}
			}
			rows, err := env.DB.QueryContext(ctx, `SELECT text FROM note ORDER BY id`)
			if err != nil {
				return nil, err
			}
			defer rows.Close()
			var notes []string
			for rows.Next() {
				var n string
				if err := rows.Scan(&n); err != nil {
					return nil, err
				}
				notes = append(notes, n)
			}
			return &samplepb.ActResponse{Actor: strings.Join(notes, "|")}, rows.Err()
		},
	})
}

// addTag is a migration a later Release of the notebook adds.
const addTag = `ALTER TABLE note ADD COLUMN tag TEXT NOT NULL DEFAULT ''`

// onRelease runs a core of Release rel on data with hello installed as
// greeter's Plugin, greeter being def.
func onRelease(t *testing.T, data, rel string, def sdk.Definition) *coretest.Harness {
	t.Helper()
	return coretest.New(t, coretest.Options{
		Release:    rel,
		Components: []sdk.Definition{def},
		Plugins:    []coretest.Plugin{hello},
		DataDir:    data,
	})
}

// note keeps text in the notebook (unless empty) and returns every note it
// keeps.
func note(t *testing.T, h *coretest.Harness, text string) []string {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"note": text})
	res := h.Call("hello", "greeter", "Act", string(body))
	if res.Code != http.StatusOK {
		t.Fatalf("Act: status %d: %s", res.Code, res.Body)
	}
	var got struct{ Actor string }
	if err := json.Unmarshal(res.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Actor == "" {
		return nil
	}
	return strings.Split(got.Actor, "|")
}

// snapshots lists the files in /data/db/snapshots.
func snapshots(t *testing.T, data string) []string {
	t.Helper()
	return files(t, data, "snapshots")
}

// aside lists the DBs a rollback moved aside, in /data/db/aside.
func aside(t *testing.T, data string) []string {
	t.Helper()
	return files(t, data, "aside")
}

func files(t *testing.T, data, dir string) []string {
	t.Helper()
	entries, _ := os.ReadDir(filepath.Join(data, "db", dir))
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

// installedOn runs Release rel on a fresh data dir with hello installed and
// one note kept, then shuts it down, as before an image tag bump.
func installedOn(t *testing.T, rel string, def sdk.Definition, text string) string {
	t.Helper()
	data := t.TempDir()
	h := onRelease(t, data, rel, def)
	h.SeedConfig("greeter", configured)
	h.Install("hello")
	h.WaitState("greeter", runtime.OK)
	note(t, h, text)
	h.Shutdown()
	return data
}

func TestBumpingTheReleaseRunsTheNewOneAfterSnapshottingEachComponentsDB(t *testing.T) {
	data := installedOn(t, "v1.0.0", notebook(), "before the update")

	h := onRelease(t, data, "v1.1.0", notebook(addTag))

	h.WaitState("greeter", runtime.OK) // so its Describe said v1.1.0
	if got, want := note(t, h, ""), []string{"before the update"}; !slices.Equal(got, want) {
		t.Errorf("notes after the update = %v, want %v migrated", got, want)
	}
	if got, want := snapshots(t, data), []string{"greeter.v1.0.0.db"}; !slices.Equal(got, want) {
		t.Errorf("snapshots = %v, want %v", got, want)
	}
}

// wantDownOnce runs Release rel of def on data, and wants greeter down with
// message, launched once and never again.
func wantDownOnce(t *testing.T, data, rel string, def sdk.Definition, message string) {
	t.Helper()
	l := &launcher{Launcher: runtime.InProcess(nil, def)}
	h := coretest.New(t, coretest.Options{
		Release:    rel,
		Components: []sdk.Definition{def},
		Plugins:    []coretest.Plugin{hello},
		DataDir:    data,
		Launcher:   l,
	})
	synctest.Wait()
	if got := h.Status("greeter"); got.State != runtime.Down || !strings.Contains(got.Message, message) {
		t.Errorf("greeter is %v, want down with %q", got, message)
	}

	time.Sleep(time.Hour)
	synctest.Wait()

	if got := h.Status("greeter"); got.State != runtime.Down {
		t.Errorf("an hour later greeter is %v, want still down", got)
	}
	if got := l.order(); len(got) != 1 {
		t.Errorf("launched %v, want once: no restart loop", got)
	}
}

func TestARollbackWithoutASnapshotRefusesTheNewerSchema(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		data := installedOn(t, "v1.1.0", notebook(addTag), "on v1.1.0")

		wantDownOnce(t, data, "v1.0.0", notebook(), "migrate: schema is ahead of this Release")
	})
}

func TestAFailedMigrationLeavesTheComponentDownWithoutARestartLoop(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		data := installedOn(t, "v1.0.0", notebook(), "before the update")

		wantDownOnce(t, data, "v1.1.0", notebook("ALTER TABLE nope ADD COLUMN x"), "no such table: nope")
	})
}

// updatedThenRolledBack installs on v1.0.0 with a note, updates to v1.1.0
// and keeps another note, then runs v1.0.0 again.
func updatedThenRolledBack(t *testing.T) (data string, h *coretest.Harness) {
	t.Helper()
	data = installedOn(t, "v1.0.0", notebook(), "before the update")
	v2 := onRelease(t, data, "v1.1.0", notebook(addTag))
	v2.WaitState("greeter", runtime.OK)
	note(t, v2, "after the update")
	v2.Shutdown()

	h = onRelease(t, data, "v1.0.0", notebook())
	h.WaitState("greeter", runtime.OK)
	return data, h
}

func TestRollingBackRestoresTheSnapshotAfterMovingTheNewerDBAside(t *testing.T) {
	data, h := updatedThenRolledBack(t)

	if got, want := note(t, h, ""), []string{"before the update"}; !slices.Equal(got, want) {
		t.Errorf("notes after the rollback = %v, want the snapshot's %v", got, want)
	}
	if got := snapshots(t, data); len(got) != 0 {
		t.Errorf("snapshots = %v, want none: the one there was restored", got)
	}
	if got, want := aside(t, data), []string{"greeter.v1.1.0.db"}; !slices.Equal(got, want) {
		t.Errorf("moved aside = %v, want the newer DB: %v", got, want)
	}
}

// The snapshot is one generation named <component>.<release>.db, whatever
// a rollback moved aside before.
func TestASnapshotIsAlwaysNamedAfterItsComponentAndRelease(t *testing.T) {
	data, h := updatedThenRolledBack(t) // moves greeter.v1.1.0.db aside
	h.Shutdown()
	again := onRelease(t, data, "v1.1.0", notebook(addTag)) // snapshots v1.0.0
	again.WaitState("greeter", runtime.OK)
	again.Shutdown()

	next := onRelease(t, data, "v1.2.0", notebook(addTag)) // snapshots v1.1.0
	next.WaitState("greeter", runtime.OK)

	if got, want := snapshots(t, data), []string{"greeter.v1.1.0.db"}; !slices.Equal(got, want) {
		t.Errorf("snapshots = %v, want one generation: %v", got, want)
	}
}

// Uninstalling with "also delete data" deletes the snapshot too: nothing of
// the Component is left that no Orphans list shows.
func TestUninstallingWithItsDataDeletesTheSnapshotToo(t *testing.T) {
	data := installedOn(t, "v1.0.0", notebook(), "before the update")
	h := onRelease(t, data, "v1.1.0", notebook(addTag))
	h.WaitState("greeter", runtime.OK)

	orphans, err := h.Uninstall("hello", true)
	if err != nil {
		t.Fatal(err)
	}

	if got := snapshots(t, data); len(orphans) != 0 || len(got) != 0 {
		t.Errorf("orphans %v and snapshots %v, want none of greeter's data left", orphans, got)
	}
}

func TestADBMovedAsideIsNeverDeleted(t *testing.T) {
	data, h := updatedThenRolledBack(t)
	h.Shutdown()

	again := onRelease(t, data, "v1.1.0", notebook(addTag)) // snapshots v1.0.0
	again.WaitState("greeter", runtime.OK)
	again.Shutdown()
	back := onRelease(t, data, "v1.0.0", notebook()) // moves v1.1.0 aside again
	back.WaitState("greeter", runtime.OK)

	if got, want := aside(t, data), []string{"greeter.v1.1.0.2.db", "greeter.v1.1.0.db"}; !slices.Equal(got, want) {
		t.Errorf("moved aside = %v, want both DBs: %v", got, want)
	}
}
