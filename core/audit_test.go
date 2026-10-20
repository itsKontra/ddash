package core_test

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/itsKontra/ddash/core/appstate"
	"github.com/itsKontra/ddash/core/coretest"
	"github.com/itsKontra/ddash/core/internal/sample"
	"github.com/itsKontra/ddash/core/internal/sample/samplepb"
	"github.com/itsKontra/ddash/core/router"
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

func TestAComponentCantCallOutsideItsRequires(t *testing.T) {
	h := coretest.New(t, coretest.Options{
		Components: []sdk.Definition{greeter(sample.Hooks{}), sample.Define("loner", nil, relayTo("sneaky"))},
		Plugins:    []coretest.Plugin{hello, {Name: "lonely", Requires: []string{"loner"}}},
	})
	h.SeedConfig("greeter", configured)
	h.SeedConfig("loner", configured)
	h.Install("hello")
	h.Install("lonely")
	h.WaitState("greeter", runtime.OK)
	h.WaitState("loner", runtime.OK)

	res := h.Call("lonely", "loner", "Act", `{}`)

	wantError(t, res, http.StatusForbidden, "permission_denied", "loner doesn't require a Component serving ddash.sample.SampleService")
	entries := componentEntries(h)
	if len(entries) != 1 || entries[0].Target != "loner" || entries[0].Outcome != "PermissionDenied" {
		t.Errorf("entries = %+v, want only loner's refused Act", entries)
	}
}

func TestWorkAComponentStartsItselfRunsForTheComponent(t *testing.T) {
	answered := make(chan string, 1)
	onItsOwn := sample.Hooks{Start: func(ctx context.Context, env *sdk.Env, _ *samplepb.SampleConfig) error {
		sdk.Every(ctx, time.Hour, func(ctx context.Context) {
			resp, err := sdk.Require(env, samplepb.NewSampleServiceClient).Act(ctx, &samplepb.ActRequest{Note: "on its own"})
			if err != nil {
				answered <- err.Error()
				return
			}
			answered <- resp.GetActor()
		})
		return nil
	}}
	h := newRelay(t, onItsOwn, sample.Hooks{})

	select {
	case actor := <-answered:
		if actor != "component:relay" {
			t.Errorf("greeter ran Act for %q, want component:relay", actor)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("relay's own work never called greeter")
	}
	entries := componentEntries(h)
	if len(entries) != 1 {
		t.Fatalf("entries = %+v, want greeter's Act", entries)
	}
	if e := entries[0]; e.ActorKind != "component" || e.Username != "relay" || e.Target != "greeter" || e.Chain == "" {
		t.Errorf("entry = %+v, want component relay's greeter Act on a chain", e)
	}
}

func TestACallBetweenComponentsGetsTheHostsDeadline(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		waitOnGreeter := sample.Hooks{Act: func(ctx context.Context, env *sdk.Env, _ *samplepb.ActRequest) (*samplepb.ActResponse, error) {
			_, err := sdk.Require(env, samplepb.NewSampleServiceClient).Wait(ctx, &samplepb.WaitRequest{})
			return &samplepb.ActResponse{Actor: status.Code(err).String()}, nil
		}}
		h := newRelay(t, waitOnGreeter, sample.Hooks{})
		start := time.Now()

		res := h.Call("relayer", "relay", "Act", `{}`)

		var got struct{ Actor string }
		json.Unmarshal(res.Body.Bytes(), &got)
		if took := time.Since(start); got.Actor != "DeadlineExceeded" || took != router.GetterDeadline {
			t.Errorf("greeter's Wait ended with %q after %v, want DeadlineExceeded after %v", got.Actor, took, router.GetterDeadline)
		}
	})
}

func TestSecretsAreRedactedOnEveryHopButReachTheComponent(t *testing.T) {
	forward := sample.Hooks{Act: func(ctx context.Context, env *sdk.Env, req *samplepb.ActRequest) (*samplepb.ActResponse, error) {
		return sdk.Require(env, samplepb.NewSampleServiceClient).Act(ctx, req)
	}}
	received := make(chan *samplepb.ActRequest, 1)
	capture := sample.Hooks{Act: func(_ context.Context, _ *sdk.Env, req *samplepb.ActRequest) (*samplepb.ActResponse, error) {
		received <- req
		return &samplepb.ActResponse{}, nil
	}}
	h := newRelay(t, forward, capture)

	res := h.Call("relayer", "relay", "Act",
		`{"note":"hi","password":"hunter2","credentials":{"user":"ada","token":"t0k"},"more":[{"user":"bob","token":"x"}]}`)

	if res.Code != http.StatusOK {
		t.Fatalf("status %d: %s", res.Code, res.Body)
	}
	if got := <-received; got.GetPassword() != "hunter2" || got.GetCredentials().GetToken() != "t0k" {
		t.Errorf("greeter received %v, want the secrets unchanged", got)
	}
	entries := componentEntries(h)
	if len(entries) != 2 {
		t.Fatalf("entries = %+v, want 2 hops", entries)
	}
	want := `{"note":"hi","password":"REDACTED","credentials":{"user":"ada","token":"REDACTED"},"more":[{"user":"bob","token":"REDACTED"}]}`
	for _, e := range entries {
		var got, wantJSON any
		json.Unmarshal([]byte(e.Request), &got)
		json.Unmarshal([]byte(want), &wantJSON)
		if !reflect.DeepEqual(got, wantJSON) {
			t.Errorf("%s recorded %s, want %s", e.Target, e.Request, want)
		}
	}
}

func TestALargeRequestIsTruncatedAtFourKiB(t *testing.T) {
	h := newHello(t, configured)
	h.WaitState("greeter", runtime.OK)
	note := strings.Repeat("a", 5000)

	if res := h.Call("hello", "greeter", "Act", `{"note":"`+note+`"}`); res.Code != http.StatusOK {
		t.Fatalf("status %d: %s", res.Code, res.Body)
	}

	entries := componentEntries(h)
	if len(entries) != 1 {
		t.Fatalf("entries = %+v, want 1", entries)
	}
	full := `{"note":"` + note + `"}`
	if got := entries[0].Request; got != full[:4096]+appstate.TruncatedMarker {
		t.Errorf("recorded %d bytes ending %q, want the first 4096 bytes and the marker", len(got), got[max(0, len(got)-40):])
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
