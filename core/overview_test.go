package core_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/itsKontra/ddash/core/appstate"
	"github.com/itsKontra/ddash/core/coretest"
)

// aLayout is a layout as the shell saves it: two placed Widgets and a third
// the Operator removed, so it is seen but not placed.
const aLayout = `{"layout":{"entries":[{"widget":"docker.containers","size":"M"},{"widget":"checks.summary","size":"S"}],"seen":["docker.containers","checks.summary","network.vpn"]}}`

func TestANewCoreHasAnEmptyLayout(t *testing.T) {
	h := coretest.New(t, coretest.Options{})

	got := coreJSON(t, h, "GetLayout", `{}`)

	if diff := cmp.Diff(map[string]any{"layout": map[string]any{}}, got); diff != "" {
		t.Errorf("GetLayout (-want +got):\n%s", diff)
	}
}

func TestASavedLayoutIsReadBackAfterARestart(t *testing.T) {
	dir := t.TempDir()
	h := coretest.New(t, coretest.Options{DataDir: dir})
	coreJSON(t, h, "SaveLayout", aLayout)
	h.Shutdown()

	got := coreJSON(t, coretest.New(t, coretest.Options{DataDir: dir}), "GetLayout", `{}`)

	var want map[string]any
	if err := json.Unmarshal([]byte(aLayout), &want); err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("GetLayout (-want +got):\n%s", diff)
	}
}

func TestSavingTheLayoutIsANonDisruptiveCoreActionInTheAuditLog(t *testing.T) {
	h := coretest.New(t, coretest.Options{})

	coreJSON(t, h, "SaveLayout", aLayout)

	entries := coreEntries(h)
	if len(entries) != 1 {
		t.Fatalf("core entries = %+v, want the one save", entries)
	}
	e := entries[0]
	if e.Method != "SaveLayout" || e.Disruptive || e.Outcome != appstate.OK || e.ActorKind != "operator" || e.Username != "test" {
		t.Errorf("entry = %+v, want operator test's non-disruptive ok SaveLayout", e)
	}
	saved := requestJSON(t, e)["layout"].(map[string]any)["entries"].([]any)
	if len(saved) != 2 {
		t.Errorf("recorded request = %v, want the saved layout", requestJSON(t, e))
	}
}

func TestALayoutTheShellCouldNotHaveMadeIsRefused(t *testing.T) {
	for name, body := range map[string]string{
		"unknown size":     `{"layout":{"entries":[{"widget":"docker.containers","size":"XL"}],"seen":["docker.containers"]}}`,
		"placed twice":     `{"layout":{"entries":[{"widget":"docker.containers","size":"S"},{"widget":"docker.containers","size":"M"}],"seen":["docker.containers"]}}`,
		"no plugin prefix": `{"layout":{"entries":[{"widget":"containers","size":"S"}],"seen":["containers"]}}`,
		"placed not seen":  `{"layout":{"entries":[{"widget":"docker.containers","size":"S"}]}}`,
	} {
		t.Run(name, func(t *testing.T) {
			h := coretest.New(t, coretest.Options{})
			coreJSON(t, h, "SaveLayout", aLayout)

			res := h.CoreCall("SaveLayout", body)

			if res.Code != http.StatusBadRequest || !json.Valid(res.Body.Bytes()) {
				t.Errorf("SaveLayout: status %d: %s, want 400 invalid_argument", res.Code, res.Body)
			}
			var answer struct{ Code string }
			_ = json.Unmarshal(res.Body.Bytes(), &answer)
			if answer.Code != "invalid_argument" {
				t.Errorf("code = %q, want invalid_argument", answer.Code)
			}
			if got := coreJSON(t, h, "GetLayout", `{}`); len(got["layout"].(map[string]any)["entries"].([]any)) != 2 {
				t.Errorf("layout after a refused save = %v, want the earlier one kept", got)
			}
			if last := coreEntries(h)[1]; last.Outcome != "InvalidArgument" {
				t.Errorf("refused save's entry = %+v, want InvalidArgument", last)
			}
		})
	}
}
