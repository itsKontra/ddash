package core_test

import (
	"context"
	"encoding/json"
	"net/http"
	"sync/atomic"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/itsKontra/ddash/core/appstate"
	"github.com/itsKontra/ddash/core/coretest"
	"github.com/itsKontra/ddash/core/internal/sample"
	"github.com/itsKontra/ddash/core/internal/sample/samplepb"
	"github.com/itsKontra/ddash/core/runtime"
	"github.com/itsKontra/ddash/sdk"
)

func TestTheConfigFormDescribesEveryFieldAndNeverReturnsASecret(t *testing.T) {
	h := newHello(t, &samplepb.SampleConfig{Target: "world", Times: ptr[int64](3), Token: "s3cret", Tags: []string{"a"}})
	h.WaitState("greeter", runtime.OK)

	got := coreJSON(t, h, "GetConfigForm", `{"owner":"greeter"}`)

	want := map[string]any{
		"fields": []any{
			map[string]any{"name": "target", "kind": "KIND_STRING", "label": "Target", "help": "Whom to greet.", "placeholder": "world", "required": true},
			map[string]any{"name": "greeting", "kind": "KIND_STRING", "label": "Greeting", "placeholder": "hello", "default": "hello"},
			map[string]any{"name": "times", "kind": "KIND_INT", "label": "Times", "placeholder": "1", "default": "1", "min": "1", "max": "10"},
			map[string]any{"name": "loud", "kind": "KIND_BOOL", "label": "Loud", "placeholder": "true", "default": "true"},
			map[string]any{"name": "tone", "kind": "KIND_ENUM", "label": "Tone", "placeholder": "TONE_WARM", "default": "TONE_WARM", "options": []any{
				map[string]any{"value": "TONE_WARM", "label": "Warm and kind"},
				map[string]any{"value": "TONE_COLD", "label": "Cold"},
			}},
			map[string]any{"name": "token", "kind": "KIND_STRING", "label": "Token", "secret": true},
			map[string]any{"name": "server", "kind": "KIND_STRING", "label": "Server", "format": "url"},
			map[string]any{"name": "tags", "kind": "KIND_STRING", "label": "Tags", "repeated": true},
			map[string]any{"name": "peers", "kind": "KIND_TABLE", "label": "Peers", "repeated": true, "columns": []any{
				map[string]any{"name": "name", "kind": "KIND_STRING", "label": "Name", "required": true},
				map[string]any{"name": "address", "kind": "KIND_STRING", "label": "Address", "format": "ip"},
				map[string]any{"name": "port", "kind": "KIND_INT", "label": "Port", "placeholder": "22", "default": "22", "min": "1", "max": "65535"},
			}},
		},
		// Only what is stored: unset fields mean their default, and the
		// secret is only "set".
		"values":     map[string]any{"target": "world", "times": "3", "tags": []any{"a"}},
		"secretsSet": []any{"token"},
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("GetConfigForm (-want +got):\n%s", diff)
	}
}

// newKeyed runs hello whose greeter is down with "401 Unauthorized" unless
// it started with token "right", seeded with cfg.
func newKeyed(t *testing.T, cfg *samplepb.SampleConfig) *coretest.Harness {
	var token atomic.Value
	h := coretest.New(t, coretest.Options{
		Components: []sdk.Definition{greeter(sample.Hooks{
			Start: func(_ context.Context, _ *sdk.Env, cfg *samplepb.SampleConfig) error {
				token.Store(cfg.GetToken())
				return nil
			},
			Health: func(context.Context) sdk.Health {
				if token.Load() != "right" {
					return sdk.Health{State: sdk.Down, Message: "401 Unauthorized"}
				}
				return sdk.Health{State: sdk.OK}
			},
		})},
		Plugins: []coretest.Plugin{hello},
	})
	h.SeedConfig("greeter", cfg)
	h.Install("hello")
	return h
}

// greeting is the config greeter runs with, as its Greet answers it.
func greeting(t *testing.T, h *coretest.Harness) map[string]any {
	t.Helper()
	res := h.Call("hello", "greeter", "Greet", `{"name":"Ada"}`)
	if res.Code != http.StatusOK {
		t.Fatalf("Greet: status %d: %s", res.Code, res.Body)
	}
	var got struct {
		Config map[string]any `json:"config"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	return got.Config
}

// lastSave is the newest SaveConfig entry in the Audit Log.
func lastSave(t *testing.T, h *coretest.Harness) appstate.Entry {
	t.Helper()
	for _, e := range h.AuditLog() {
		if e.Method == "SaveConfig" {
			return e
		}
	}
	t.Fatal("no SaveConfig in the Audit Log")
	return appstate.Entry{}
}

func TestASavedConfigRestartsTheComponentWithItAndAnswersItsHealth(t *testing.T) {
	h := newKeyed(t, &samplepb.SampleConfig{Target: "world", Token: "right"})
	h.WaitState("greeter", runtime.OK)

	got := coreJSON(t, h, "SaveConfig", `{"owner":"greeter","values":{"target":"Ada","times":"2","peers":[{"name":"pi","address":"198.18.0.5"}]}}`)

	if diff := cmp.Diff(map[string]any{"status": map[string]any{"name": "greeter", "state": "ok", "configurable": true}}, got); diff != "" {
		t.Errorf("SaveConfig (-want +got):\n%s", diff)
	}
	want := map[string]any{
		"target": "Ada", "greeting": "hello", "times": "2", "loud": true, "tone": "TONE_WARM", "token": "right",
		"peers": []any{map[string]any{"name": "pi", "address": "198.18.0.5", "port": "22"}},
	}
	if diff := cmp.Diff(want, greeting(t, h)); diff != "" {
		t.Errorf("greeter runs with (-want +got):\n%s", diff)
	}
	form := coreJSON(t, h, "GetConfigForm", `{"owner":"greeter"}`)
	wantValues := map[string]any{"target": "Ada", "times": "2", "peers": []any{map[string]any{"name": "pi", "address": "198.18.0.5"}}}
	if diff := cmp.Diff(wantValues, form["values"]); diff != "" {
		t.Errorf("stored values (-want +got):\n%s", diff)
	}
	e := lastSave(t, h)
	wantRequest := map[string]any{"owner": "greeter", "changes": []any{
		map[string]any{"field": "target", "old": `"world"`, "new": `"Ada"`},
		map[string]any{"field": "times", "new": "2"},
		map[string]any{"field": "peers", "new": `[{"name":"pi","address":"198.18.0.5"}]`},
	}}
	if diff := cmp.Diff(wantRequest, requestJSON(t, e)); diff != "" || e.Outcome != appstate.OK || e.Disruptive || e.Target != "core" {
		t.Errorf("entry %+v; request (-want +got):\n%s", e, diff)
	}
}

func TestAConfigWithWrongCredentialsIsSavedAndItsHealthAnswered(t *testing.T) {
	h := newKeyed(t, &samplepb.SampleConfig{Target: "world", Token: "right"})
	h.WaitState("greeter", runtime.OK)

	got := coreJSON(t, h, "SaveConfig", `{"owner":"greeter","values":{"target":"world","token":"wrong"}}`)

	want := map[string]any{"status": map[string]any{"name": "greeter", "state": "down", "message": "401 Unauthorized", "configurable": true}}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("SaveConfig (-want +got):\n%s", diff)
	}
	if token := greeting(t, h)["token"]; token != "wrong" {
		t.Errorf("greeter runs with token %v, want the saved one", token)
	}
}

func ptr[T any](v T) *T { return &v }
