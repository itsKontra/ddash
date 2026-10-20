package core_test

import (
	"errors"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/itsKontra/ddash/core/appstate"
	"github.com/itsKontra/ddash/core/coretest"
	"github.com/itsKontra/ddash/core/internal/sample"
	"github.com/itsKontra/ddash/core/runtime"
	"github.com/itsKontra/ddash/sdk"
)

// coreEntries is the Audit Log's core Actions, oldest first.
func coreEntries(h *coretest.Harness) []appstate.Entry {
	var out []appstate.Entry
	for _, e := range h.AuditLog() {
		if e.Target == "core" {
			out = append([]appstate.Entry{e}, out...)
		}
	}
	return out
}

func TestInstallIsACoreActionInTheAuditLog(t *testing.T) {
	h := newHello(t, configured)

	err := h.TryInstall("nonexistent")

	if status.Code(err) != codes.NotFound {
		t.Errorf("installing a Plugin the Release lacks: %v, want NotFound", err)
	}
	entries := coreEntries(h)
	if len(entries) != 2 {
		t.Fatalf("entries = %+v, want both installs", entries)
	}
	ok, missing := entries[0], entries[1]
	if ok.Method != "Install" || ok.Disruptive || ok.Outcome != appstate.OK || ok.ActorKind != "operator" || ok.Username != "test" {
		t.Errorf("install = %+v, want operator test's non-disruptive ok Install", ok)
	}
	if got := requestJSON(t, ok); got["plugin"] != "hello" {
		t.Errorf("install request = %v, want plugin hello", got)
	}
	if missing.Outcome != "NotFound" {
		t.Errorf("install of a missing Plugin = %+v, want NotFound", missing)
	}
}

// newShared runs a core where hello requires greeter, and relayer requires
// relay, which requires greeter too. Both Plugins are installed and every
// Component is up.
func newShared(t *testing.T) *coretest.Harness {
	h := coretest.New(t, coretest.Options{
		Components: []sdk.Definition{greeter(sample.Hooks{}), sample.Define("relay", []string{"greeter"}, sample.Hooks{})},
		Plugins:    []coretest.Plugin{hello, relayer},
	})
	h.SeedConfig("greeter", configured)
	h.SeedConfig("relay", configured)
	h.Install("hello")
	h.Install("relayer")
	h.WaitState("greeter", runtime.OK)
	h.WaitState("relay", runtime.OK)
	return h
}

func dbFiles(t *testing.T, h *coretest.Harness) []string {
	t.Helper()
	var names []string
	entries, err := os.ReadDir(filepath.Join(h.DataDir, "db"))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		t.Fatal(err)
	}
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

func TestUninstallStopsWhatNothingStillRequiresAndKeepsItsData(t *testing.T) {
	h := newShared(t)

	orphans, err := h.Uninstall("relayer", false)

	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(orphans, []string{"relay"}) {
		t.Errorf("orphans = %v, want [relay]", orphans)
	}
	if _, running := h.Host.Runtime().Status()["relay"]; running {
		t.Errorf("relay is still there: %v", h.Status("relay"))
	}
	if got := h.Status("greeter").State; got != runtime.OK {
		t.Errorf("greeter is %s, want ok: hello still requires it", got)
	}
	if res := h.Call("relayer", "relay", "Greet", `{"name":"Ada"}`); res.Code != http.StatusForbidden {
		t.Errorf("calling the uninstalled Plugin's Component: status %d, want 403", res.Code)
	}
	if files := dbFiles(t, h); !slices.Contains(files, "relay.db") {
		t.Errorf("DB files %v, want relay.db kept", files)
	}
	entries := coreEntries(h)
	if last := entries[len(entries)-1]; last.Method != "Uninstall" || !last.Disruptive || last.Outcome != appstate.OK {
		t.Errorf("last core entry = %+v, want a disruptive ok Uninstall", last)
	}

	h.Install("relayer")

	h.WaitState("relay", runtime.OK) // its config was kept
	if orphans := h.Host.Runtime().Orphans(); len(orphans) != 0 {
		t.Errorf("orphans after reinstalling = %v, want none", orphans)
	}
}

func TestUninstallWithDeleteDataDeletesDBFilesAndConfig(t *testing.T) {
	h := newShared(t)

	orphans, err := h.Uninstall("relayer", true)

	if err != nil || len(orphans) != 0 {
		t.Fatalf("Uninstall = %v, %v; want no orphans", orphans, err)
	}
	for _, f := range dbFiles(t, h) {
		if f == "relay.db" || f == "relay.db-wal" || f == "relay.db-shm" {
			t.Errorf("%s is still there", f)
		}
	}
	if files := dbFiles(t, h); !slices.Contains(files, "greeter.db") {
		t.Errorf("DB files %v, want greeter.db kept: greeter still runs", files)
	}

	h.Install("relayer")

	h.WaitState("relay", runtime.Unconfigured) // its config is gone
}

func TestUninstallingAPluginThatIsntInstalledIsNotFound(t *testing.T) {
	h := coretest.New(t, coretest.Options{Components: []sdk.Definition{greeter(sample.Hooks{})}, Plugins: []coretest.Plugin{hello}})

	_, err := h.Uninstall("hello", false)

	if status.Code(err) != codes.NotFound {
		t.Errorf("err = %v, want NotFound", err)
	}
}
