package core_test

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/itsKontra/ddash/core/appstate"
	"github.com/itsKontra/ddash/core/coretest"
	"github.com/itsKontra/ddash/core/internal/sample"
	"github.com/itsKontra/ddash/core/internal/sample/samplepb"
	"github.com/itsKontra/ddash/core/runtime"
	"github.com/itsKontra/ddash/sdk"
	"github.com/itsKontra/ddash/sdk/migrate"
)

// requestJSON decodes an entry's recorded request.
func requestJSON(t *testing.T, e appstate.Entry) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(e.Request), &m); err != nil {
		t.Fatalf("request %q: %v", e.Request, err)
	}
	return m
}

// componentEntries is the Audit Log without the core's own Actions, oldest
// first.
func componentEntries(h *coretest.Harness) []appstate.Entry {
	var out []appstate.Entry
	for _, e := range h.AuditLog() {
		if e.Target != "core" {
			out = append([]appstate.Entry{e}, out...)
		}
	}
	return out
}

func TestAnActionFromTheUIIsRecordedAndAGetterIsNot(t *testing.T) {
	h := newHello(t, configured)
	h.WaitState("greeter", runtime.OK)
	before := time.Now()

	if res := h.Call("hello", "greeter", "Greet", `{"name":"Ada"}`); res.Code != http.StatusOK {
		t.Fatalf("Greet: status %d: %s", res.Code, res.Body)
	}
	res := h.Call("hello", "greeter", "Act", `{"note":"hi"}`)

	if res.Code != http.StatusOK {
		t.Fatalf("Act: status %d: %s", res.Code, res.Body)
	}
	var acted struct{ Actor string }
	json.Unmarshal(res.Body.Bytes(), &acted)
	if acted.Actor != "operator:test" {
		t.Errorf("the Component ran Act for %q, want operator:test", acted.Actor)
	}
	entries := componentEntries(h)
	if len(entries) != 1 {
		t.Fatalf("%d entries, want 1 for Act and none for Greet: %+v", len(entries), entries)
	}
	e := entries[0]
	if e.ActorKind != "operator" || e.Username != "test" || e.Target != "greeter" || e.Method != "Act" || !e.Disruptive {
		t.Errorf("entry = %+v, want operator test's disruptive greeter Act", e)
	}
	if e.Outcome != appstate.OK || e.Chain == "" || e.Time.Before(before.Add(-time.Second)) || e.Duration < 0 {
		t.Errorf("entry = %+v, want ok with a chain id, a time and a duration", e)
	}
	if got := requestJSON(t, e); got["note"] != "hi" {
		t.Errorf("request = %v, want the note", got)
	}
}

func TestAFailedActionIsRecordedWithItsCodeAndMessage(t *testing.T) {
	refuse := sample.Hooks{Act: func(context.Context, *sdk.Env, *samplepb.ActRequest) (*samplepb.ActResponse, error) {
		return nil, status.Error(codes.PermissionDenied, "protected in Proxmox")
	}}
	h := coretest.New(t, coretest.Options{Components: []sdk.Definition{greeter(refuse)}, Plugins: []coretest.Plugin{hello}})
	h.SeedConfig("greeter", configured)
	h.Install("hello")
	h.WaitState("greeter", runtime.OK)

	res := h.Call("hello", "greeter", "Act", `{}`)

	wantError(t, res, http.StatusForbidden, "permission_denied", "protected in Proxmox")
	entries := componentEntries(h)
	if len(entries) != 1 || entries[0].Outcome != "PermissionDenied" || entries[0].Message != "protected in Proxmox" {
		t.Errorf("entries = %+v, want one PermissionDenied with its message", entries)
	}
}

// relayTo is a sample hook whose Act calls Act on the Component it requires.
func relayTo(note string) sample.Hooks {
	return sample.Hooks{Act: func(ctx context.Context, env *sdk.Env, _ *samplepb.ActRequest) (*samplepb.ActResponse, error) {
		return sdk.Require(env, samplepb.NewSampleServiceClient).Act(ctx, &samplepb.ActRequest{Note: note})
	}}
}

// relayer is a Plugin requiring relay, which requires greeter.
var relayer = coretest.Plugin{Name: "relayer", Requires: []string{"relay"}}

// newRelay runs a core with relayer installed, relay requiring greeter, and
// both up.
func newRelay(t *testing.T, relayHooks, greeterHooks sample.Hooks) *coretest.Harness {
	h := coretest.New(t, coretest.Options{
		Components: []sdk.Definition{greeter(greeterHooks), sample.Define("relay", []string{"greeter"}, relayHooks)},
		Plugins:    []coretest.Plugin{relayer},
	})
	h.SeedConfig("greeter", configured)
	h.SeedConfig("relay", configured)
	h.Install("relayer")
	h.WaitState("greeter", runtime.OK)
	h.WaitState("relay", runtime.OK)
	return h
}

func TestEachHopOfAnActionIsRecordedUnderOneChain(t *testing.T) {
	h := newRelay(t, relayTo("onward"), sample.Hooks{})

	res := h.Call("relayer", "relay", "Act", `{"note":"first"}`)

	if res.Code != http.StatusOK {
		t.Fatalf("status %d: %s", res.Code, res.Body)
	}
	var acted struct{ Actor string }
	json.Unmarshal(res.Body.Bytes(), &acted)
	if acted.Actor != "operator:test" {
		t.Errorf("greeter ran Act for %q, want operator:test carried through relay", acted.Actor)
	}
	entries := componentEntries(h)
	if len(entries) != 2 {
		t.Fatalf("%d entries, want one per hop: %+v", len(entries), entries)
	}
	for i, want := range []struct{ target, note string }{{"relay", "first"}, {"greeter", "onward"}} {
		e := entries[i]
		if e.Target != want.target || requestJSON(t, e)["note"] != want.note || e.Outcome != appstate.OK {
			t.Errorf("hop %d = %+v, want %s ok with note %s", i, e, want.target, want.note)
		}
		if e.ActorKind != "operator" || e.Username != "test" {
			t.Errorf("hop %d ran for %s:%s, want operator:test", i, e.ActorKind, e.Username)
		}
	}
	if entries[0].Chain == "" || entries[0].Chain != entries[1].Chain {
		t.Errorf("chains %q and %q, want one shared chain id", entries[0].Chain, entries[1].Chain)
	}
}

// breakAuditLog makes every insert into the Audit Log fail, as a full disk
// would.
func breakAuditLog(t *testing.T, h *coretest.Harness) {
	t.Helper()
	db, err := migrate.Open(filepath.Join(h.DataDir, "core.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	_, err = db.Exec(`CREATE TRIGGER disk_full BEFORE INSERT ON audit_entry BEGIN SELECT RAISE(FAIL, 'database or disk is full'); END`)
	if err != nil {
		t.Fatal(err)
	}
}

func TestAnActionIsRefusedWhenTheAuditLogCantRecordIt(t *testing.T) {
	ran := false
	h := coretest.New(t, coretest.Options{
		Components: []sdk.Definition{greeter(sample.Hooks{Act: func(context.Context, *sdk.Env, *samplepb.ActRequest) (*samplepb.ActResponse, error) {
			ran = true
			return &samplepb.ActResponse{}, nil
		}})},
		Plugins: []coretest.Plugin{hello},
	})
	h.SeedConfig("greeter", configured)
	h.Install("hello")
	h.WaitState("greeter", runtime.OK)
	breakAuditLog(t, h)

	res := h.Call("hello", "greeter", "Act", `{}`)

	wantError(t, res, http.StatusServiceUnavailable, "unavailable", "audit log unavailable")
	if ran {
		t.Error("the Component ran the Action")
	}
	if res := h.Call("hello", "greeter", "Greet", `{"name":"Ada"}`); res.Code != http.StatusOK {
		t.Errorf("a getter still works: status %d: %s", res.Code, res.Body)
	}
}
