package core_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/google/go-cmp/cmp"
	"google.golang.org/protobuf/proto"

	dockerapi "github.com/itsKontra/ddash/components/docker-api"
	"github.com/itsKontra/ddash/core/appstate"
	"github.com/itsKontra/ddash/core/coretest"
	"github.com/itsKontra/ddash/core/internal/sample"
	"github.com/itsKontra/ddash/core/internal/sample/samplepb"
	"github.com/itsKontra/ddash/core/runtime"
	corepb "github.com/itsKontra/ddash/proto/ddash/core"
	"github.com/itsKontra/ddash/sdk"
	"github.com/itsKontra/ddash/sdk/fixture"
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

// rejected decodes an "invalid config" answer to its errors by path.
func rejected(t *testing.T, res *httptest.ResponseRecorder) map[string]string {
	t.Helper()
	wantError(t, res, http.StatusBadRequest, "invalid_argument", "invalid config")
	var body struct {
		Details []struct {
			Type  string `json:"type"`
			Value string `json:"value"`
		} `json:"details"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, d := range body.Details {
		if d.Type != "ddash.core.ConfigErrors" {
			continue
		}
		b, err := base64.RawStdEncoding.DecodeString(strings.TrimRight(d.Value, "="))
		if err != nil {
			t.Fatal(err)
		}
		var errs corepb.ConfigErrors
		if err := proto.Unmarshal(b, &errs); err != nil {
			t.Fatal(err)
		}
		for _, e := range errs.GetErrors() {
			got[e.GetPath()] = e.GetMessage()
		}
	}
	return got
}

func TestAConfigBreakingItsFieldsRulesIsRejectedBeforeTheComponentRestarts(t *testing.T) {
	var starts atomic.Int32
	h := coretest.New(t, coretest.Options{
		Components: []sdk.Definition{greeter(sample.Hooks{Start: func(context.Context, *sdk.Env, *samplepb.SampleConfig) error {
			starts.Add(1)
			return nil
		}})},
		Plugins: []coretest.Plugin{hello},
	})
	h.SeedConfig("greeter", configured)
	h.Install("hello")
	h.WaitState("greeter", runtime.OK)

	res := h.CoreCall("SaveConfig", `{"owner":"greeter","values":{
		"times":"11","server":"198.18.2.10:2375","tags":["x"],
		"peers":[{"address":"198.18.0.5"},{"name":"b","address":"198.18.0.256","port":"0"}]}}`)

	want := map[string]string{
		"target":           "is required",
		"times":            "must be at most 10",
		"server":           "must be a URL like https://host.example",
		"peers[0].name":    "is required",
		"peers[1].address": "must be an IP address",
		"peers[1].port":    "must be at least 1",
	}
	if diff := cmp.Diff(want, rejected(t, res)); diff != "" {
		t.Errorf("errors (-want +got):\n%s", diff)
	}
	if n := starts.Load(); n != 1 {
		t.Errorf("greeter started %d times, want once: a rejected config never reaches it", n)
	}
	if target := greeting(t, h)["target"]; target != "world" {
		t.Errorf("greeter runs with target %v, want the stored world", target)
	}
	if e := lastSave(t, h); e.Outcome != "InvalidArgument" || e.Message != "invalid config" {
		t.Errorf("entry %+v, want a failed InvalidArgument", e)
	}
}

func TestAConfigStartRejectsIsNotSavedAndTheComponentRunsOnTheOldOne(t *testing.T) {
	h := newHello(t, configured)
	h.WaitState("greeter", runtime.OK)

	res := h.CoreCall("SaveConfig", `{"owner":"greeter","values":{"target":"nowhere","greeting":"hi"}}`)

	if diff := cmp.Diff(map[string]string{"target": "can't be greeted"}, rejected(t, res)); diff != "" {
		t.Errorf("errors (-want +got):\n%s", diff)
	}
	h.WaitState("greeter", runtime.OK)
	if got := greeting(t, h); got["target"] != "world" || got["greeting"] != "hello" {
		t.Errorf("greeter runs with %v, want the stored config", got)
	}
	form := coreJSON(t, h, "GetConfigForm", `{"owner":"greeter"}`)
	if diff := cmp.Diff(map[string]any{"target": "world"}, form["values"]); diff != "" || form["errors"] != nil {
		t.Errorf("stored values (-want +got):\n%s\nerrors %v, want none", diff, form["errors"])
	}
	if e := lastSave(t, h); e.Outcome != "InvalidArgument" {
		t.Errorf("entry %+v, want a failed InvalidArgument", e)
	}
}

func TestASaveKeepsTheSecretsItLeavesUnchangedAndReplacesOrClearsTheOthers(t *testing.T) {
	h := newKeyed(t, &samplepb.SampleConfig{Target: "world", Token: "right"})
	h.WaitState("greeter", runtime.OK)
	saves := []struct {
		body       string
		token      string
		changes    []any
		secretsSet any
	}{
		{
			// The form never has the secret, so it sends none.
			body:       `{"owner":"greeter","values":{"target":"Ada"}}`,
			token:      "right",
			changes:    []any{map[string]any{"field": "target", "old": `"world"`, "new": `"Ada"`}},
			secretsSet: []any{"token"},
		},
		{
			body:       `{"owner":"greeter","values":{"target":"Ada","token":"rotated"}}`,
			token:      "rotated",
			changes:    []any{map[string]any{"field": "token", "change": "changed"}},
			secretsSet: []any{"token"},
		},
		{
			body:    `{"owner":"greeter","values":{"target":"Ada"},"clearSecrets":["token"]}`,
			token:   "",
			changes: []any{map[string]any{"field": "token", "change": "cleared"}},
		},
	}
	for _, s := range saves {
		coreJSON(t, h, "SaveConfig", s.body)

		if got := greeting(t, h)["token"]; got != nil && got != s.token || got == nil && s.token != "" {
			t.Errorf("after %s: greeter runs with token %v, want %q", s.body, got, s.token)
		}
		e := lastSave(t, h)
		if diff := cmp.Diff(s.changes, requestJSON(t, e)["changes"]); diff != "" || strings.Contains(e.Request, "rotated") {
			t.Errorf("after %s: recorded %s; changes (-want +got):\n%s", s.body, e.Request, diff)
		}
		if got := coreJSON(t, h, "GetConfigForm", `{"owner":"greeter"}`)["secretsSet"]; !cmp.Equal(s.secretsSet, got) {
			t.Errorf("after %s: secrets set %v, want %v", s.body, got, s.secretsSet)
		}
	}
}

// greeterStatus is greeter's entry in the Catalogue, under hello.
func greeterStatus(t *testing.T, h *coretest.Harness) map[string]any {
	t.Helper()
	cat := coreJSON(t, h, "ListCatalogue", `{}`)
	return cat["plugins"].([]any)[0].(map[string]any)["components"].([]any)[0].(map[string]any)
}

func TestAComponentLeftWithoutARequiredFieldNeedsConfigAndItsFormSaysWhy(t *testing.T) {
	// Saved before a Release made target required.
	h := newHello(t, &samplepb.SampleConfig{Greeting: ptr("hi")})

	h.WaitState("greeter", runtime.Unconfigured)

	want := map[string]any{"name": "greeter", "state": "unconfigured", "message": "invalid config", "configurable": true, "needsConfig": true}
	if diff := cmp.Diff(want, greeterStatus(t, h)); diff != "" {
		t.Errorf("Catalogue (-want +got):\n%s", diff)
	}
	form := coreJSON(t, h, "GetConfigForm", `{"owner":"greeter"}`)
	if diff := cmp.Diff([]any{map[string]any{"path": "target", "message": "is required"}}, form["errors"]); diff != "" {
		t.Errorf("form errors (-want +got):\n%s", diff)
	}
}

func TestAComponentNeverConfiguredIsUnconfiguredButNeedsNoConfig(t *testing.T) {
	h := newHello(t, nil)

	h.WaitState("greeter", runtime.Unconfigured)

	if got := greeterStatus(t, h); got["needsConfig"] != nil || got["configurable"] != true {
		t.Errorf("Catalogue: %v, want configurable, not needing config", got)
	}
}

func TestInstallAnswersTheComponentsItAdds(t *testing.T) {
	h := coretest.New(t, coretest.Options{
		Components: []sdk.Definition{greeter(sample.Hooks{}), sample.Define("relay", []string{"greeter"}, sample.Hooks{})},
		Plugins:    []coretest.Plugin{hello, relayer},
	})
	h.Install("hello")

	got := coreJSON(t, h, "Install", `{"plugin":"relayer"}`)

	// greeter already runs for hello; the shell opens relay's form.
	if diff := cmp.Diff(map[string]any{"components": []any{"relay"}}, got); diff != "" {
		t.Errorf("Install (-want +got):\n%s", diff)
	}
}

func TestAComponentValidOnDefaultsStartsRightAfterInstall(t *testing.T) {
	h := coretest.New(t, coretest.Options{
		Components: []sdk.Definition{dockerapi.Definition()},
		Plugins:    []coretest.Plugin{{Name: "docker", Requires: []string{"docker-api"}}},
	})

	h.Install("docker")

	h.WaitState("docker-api", runtime.OK)
}

func TestUninstallWithDeleteDataForgetsTheConfig(t *testing.T) {
	h := newHello(t, &samplepb.SampleConfig{Target: "world", Token: "s3cret"})
	h.WaitState("greeter", runtime.OK)

	if _, err := h.Uninstall("hello", true); err != nil {
		t.Fatal(err)
	}
	h.Install("hello")

	h.WaitState("greeter", runtime.Unconfigured)
	form := coreJSON(t, h, "GetConfigForm", `{"owner":"greeter"}`)
	if values, _ := form["values"].(map[string]any); len(values) != 0 || form["secretsSet"] != nil {
		t.Errorf("after reinstalling: values %v, secrets %v, want none", form["values"], form["secretsSet"])
	}
}

func TestTheThemeIsACoreSettingSavedLikeAnyConfig(t *testing.T) {
	h := coretest.New(t, coretest.Options{})

	if diff := cmp.Diff(map[string]any{"settings": map[string]any{"theme": "THEME_DDASH"}}, coreJSON(t, h, "GetCoreSettings", `{}`)); diff != "" {
		t.Errorf("default settings (-want +got):\n%s", diff)
	}
	form := coreJSON(t, h, "GetConfigForm", `{"owner":"core"}`)
	wantFields := []any{map[string]any{
		"name": "theme", "kind": "KIND_ENUM", "label": "Theme", "placeholder": "THEME_DDASH", "default": "THEME_DDASH",
		"options": []any{map[string]any{"value": "THEME_DDASH", "label": "ddash"}},
	}}
	if diff := cmp.Diff(wantFields, form["fields"]); diff != "" {
		t.Errorf("core form (-want +got):\n%s", diff)
	}

	if diff := cmp.Diff(map[string]any{}, coreJSON(t, h, "SaveConfig", `{"owner":"core","values":{"theme":"THEME_DDASH"}}`)); diff != "" {
		t.Errorf("SaveConfig (-want +got):\n%s", diff)
	}

	form = coreJSON(t, h, "GetConfigForm", `{"owner":"core"}`)
	if diff := cmp.Diff(map[string]any{"theme": "THEME_DDASH"}, form["values"]); diff != "" {
		t.Errorf("stored (-want +got):\n%s", diff)
	}
	e := lastSave(t, h)
	want := map[string]any{"owner": "core", "changes": []any{map[string]any{"field": "theme", "new": `"THEME_DDASH"`}}}
	if diff := cmp.Diff(want, requestJSON(t, e)); diff != "" || e.Outcome != appstate.OK {
		t.Errorf("entry %+v; request (-want +got):\n%s", e, diff)
	}
	res := h.CoreCall("SaveConfig", `{"owner":"core","values":{"theme":"THEME_NEON"}}`)
	wantError(t, res, http.StatusBadRequest, "invalid_argument", "")
}

func TestDockerApisHostsAreATableFormThatSavesIntoAListOfContainers(t *testing.T) {
	media := fixture.Server(t, "../components/docker-api/testdata/baseline")
	h := coretest.New(t, coretest.Options{Components: []sdk.Definition{dockerapi.Definition()}, Plugins: []coretest.Plugin{docker}})
	h.Install("docker")
	h.WaitState("docker-api", runtime.OK)

	form := coreJSON(t, h, "GetConfigForm", `{"owner":"docker-api"}`)
	wantFields := []any{map[string]any{
		"name": "hosts", "kind": "KIND_TABLE", "label": "Docker hosts", "repeated": true,
		"help": "Each host's read-only socket-proxy. None is fine.",
		"columns": []any{
			map[string]any{"name": "name", "kind": "KIND_STRING", "label": "Name", "required": true, "placeholder": "media"},
			map[string]any{"name": "url", "kind": "KIND_STRING", "label": "Socket-proxy URL", "required": true, "format": "url", "placeholder": "tcp://198.18.2.10:2375"},
		},
	}}
	if diff := cmp.Diff(wantFields, form["fields"]); diff != "" {
		t.Errorf("docker-api form (-want +got):\n%s", diff)
	}

	res := h.CoreCall("SaveConfig", `{"owner":"docker-api","values":{"hosts":[{"name":"media","url":"tcp://`+media.Listener.Addr().String()+`"},{"name":"nas","url":"nas:2375"}]}}`)
	if diff := cmp.Diff(map[string]string{"hosts[1].url": "must be a URL like https://host.example"}, rejected(t, res)); diff != "" {
		t.Errorf("errors (-want +got):\n%s", diff)
	}

	got := coreJSON(t, h, "SaveConfig", `{"owner":"docker-api","values":{"hosts":[{"name":"media","url":"tcp://`+media.Listener.Addr().String()+`"}]}}`)
	if state := got["status"].(map[string]any)["state"]; state != "ok" {
		t.Errorf("SaveConfig = %v, want docker-api ok", got)
	}
	if res := h.Call("docker", "docker-api", "ListContainers", `{}`); !strings.Contains(res.Body.String(), "jellyfin") {
		t.Errorf("ListContainers: %d %s, want media's containers", res.Code, res.Body)
	}
}

func ptr[T any](v T) *T { return &v }
