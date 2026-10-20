package core_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"maps"
	"testing"

	"google.golang.org/protobuf/proto"

	"github.com/itsKontra/ddash/core/coretest"
	"github.com/itsKontra/ddash/core/internal/sample"
	"github.com/itsKontra/ddash/core/internal/sample/samplepb"
	"github.com/itsKontra/ddash/core/runtime"
	"github.com/itsKontra/ddash/sdk"
)

// hello is a Plugin requiring one sample Component, "greeter".
var hello = coretest.Plugin{Name: "hello", Requires: []string{"greeter"}}

func greeter(hooks sample.Hooks) sdk.Definition { return sample.Define("greeter", nil, hooks) }

// configured is a greeter config Start accepts.
var configured = &samplepb.SampleConfig{Target: "world"}

// newHello runs a core with hello installed and greeter seeded with cfg (nil
// seeds nothing).
func newHello(t *testing.T, cfg *samplepb.SampleConfig) *coretest.Harness {
	h := coretest.New(t, coretest.Options{
		Components: []sdk.Definition{greeter(sample.Hooks{})},
		Plugins:    []coretest.Plugin{hello},
	})
	if cfg != nil {
		h.SeedConfig("greeter", cfg)
	}
	h.Install("hello")
	return h
}

// connectError decodes a Connect error response, failing the test on any
// other kind of response.
func connectError(t *testing.T, res *httptest.ResponseRecorder) (status int, code, message string) {
	t.Helper()
	var e struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	if ct := res.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("Content-Type %q, want application/json (status %d: %s)", ct, res.Code, res.Body)
	}
	if err := json.Unmarshal(res.Body.Bytes(), &e); err != nil || e.Code == "" {
		t.Fatalf("status %d, body %s: not a Connect error", res.Code, res.Body)
	}
	return res.Code, e.Code, e.Message
}

func wantError(t *testing.T, res *httptest.ResponseRecorder, status int, code, message string) {
	t.Helper()
	gotStatus, gotCode, gotMessage := connectError(t, res)
	if gotStatus != status || gotCode != code || (message != "" && gotMessage != message) {
		t.Errorf("got %d %s %q, want %d %s %q", gotStatus, gotCode, gotMessage, status, code, message)
	}
}

func TestAComponentWithoutValidConfigIsUnconfiguredAndNotConfigured(t *testing.T) {
	h := newHello(t, nil)

	h.WaitState("greeter", runtime.Unconfigured)
	res := h.Call("hello", "greeter", "Greet", `{"name":"Ada"}`)

	wantError(t, res, http.StatusBadRequest, "failed_precondition", "not configured")
}

func TestAComponentStartsWithDefaultsForUnsetConfigFields(t *testing.T) {
	h := newHello(t, &samplepb.SampleConfig{Target: "world", Times: proto.Int64(3)})

	h.WaitState("greeter", runtime.OK)
	res := h.Call("hello", "greeter", "Greet", `{"name":"Ada"}`)

	if res.Code != http.StatusOK {
		t.Fatalf("status %d: %s", res.Code, res.Body)
	}
	var got struct {
		Greeting string         `json:"greeting"`
		Config   map[string]any `json:"config"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"target": "world", "greeting": "hello", "times": "3", "loud": true, "tone": "TONE_WARM"}
	if got.Greeting != "hello Ada" || !maps.Equal(got.Config, want) {
		t.Errorf("got %q with config %v, want %q with %v", got.Greeting, got.Config, "hello Ada", want)
	}
}

func TestACallWithoutTheConnectProtocolHeaderIsRefusedAsCrossSite(t *testing.T) {
	h := newHello(t, configured)
	h.WaitState("greeter", runtime.OK)

	for _, version := range []string{"", "2"} {
		r := coretest.ConnectRequest("greeter", "Greet", `{"name":"Ada"}`)
		r.Header.Set(coretest.PluginHeader, "hello")
		r.Header.Set("Connect-Protocol-Version", version)

		res := h.Do(r)

		wantError(t, res, http.StatusBadRequest, "invalid_argument", `missing required header: set Connect-Protocol-Version to "1"`)
	}
}

func TestOnlyConnectUnaryJSONIsServed(t *testing.T) {
	h := newHello(t, configured)
	h.WaitState("greeter", runtime.OK)

	get := coretest.ConnectRequest("greeter", "Greet", "")
	get.Method = http.MethodGet
	get.Header.Set(coretest.PluginHeader, "hello")
	if res := h.Do(get); res.Code != http.StatusMethodNotAllowed || res.Header().Get("Allow") != http.MethodPost {
		t.Errorf("GET: status %d, Allow %q; want 405, POST", res.Code, res.Header().Get("Allow"))
	}

	form := coretest.ConnectRequest("greeter", "Greet", "name=Ada")
	form.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	form.Header.Set(coretest.PluginHeader, "hello")
	if res := h.Do(form); res.Code != http.StatusUnsupportedMediaType {
		t.Errorf("form POST: status %d, want 415", res.Code)
	}

	charset := coretest.ConnectRequest("greeter", "Greet", `{"name":"Ada"}`)
	charset.Header.Set("Content-Type", "application/json; charset=utf-8")
	charset.Header.Set(coretest.PluginHeader, "hello")
	if res := h.Do(charset); res.Code != http.StatusOK {
		t.Errorf("JSON with charset: status %d: %s", res.Code, res.Body)
	}
}

func TestCallsAreTranscodedFromTheComponentsDescriptors(t *testing.T) {
	h := newHello(t, configured)
	h.WaitState("greeter", runtime.OK)

	for _, c := range []struct {
		why, method, body string
		status            int
		code, message     string
	}{
		{"unknown fields are ignored", "Greet", `{"name":"Ada","mood":"sunny"}`, http.StatusOK, "", ""},
		{"stock connect-web clients name the service", "ddash.sample.SampleService/Greet", `{"name":"Ada"}`, http.StatusOK, "", ""},
		{"malformed JSON", "Greet", `{"name":`, http.StatusBadRequest, "invalid_argument", ""},
		{"a field of the wrong type", "Greet", `{"name":"Ada","count":"many"}`, http.StatusBadRequest, "invalid_argument", ""},
		{"the Component's own error", "Greet", `{}`, http.StatusBadRequest, "invalid_argument", "name is required"},
		{"an empty body is an empty request", "Greet", ``, http.StatusBadRequest, "invalid_argument", "name is required"},
		{"no such method", "Shout", `{}`, http.StatusNotImplemented, "unimplemented", ""},
		{"another service", "ddash.sample.OtherService/Greet", `{}`, http.StatusNotImplemented, "unimplemented", ""},
	} {
		res := h.Call("hello", "greeter", c.method, c.body)
		if c.status == http.StatusOK {
			if res.Code != http.StatusOK {
				t.Errorf("%s: status %d: %s", c.why, res.Code, res.Body)
			}
			continue
		}
		status, code, msg := connectError(t, res)
		if status != c.status || code != c.code || (c.message != "" && msg != c.message) {
			t.Errorf("%s: %d %s %q, want %d %s %q", c.why, status, code, msg, c.status, c.code, c.message)
		}
	}
}

func TestAPluginReachesOnlyTheComponentsItRequiresDirectly(t *testing.T) {
	h := coretest.New(t, coretest.Options{
		Components: []sdk.Definition{
			greeter(sample.Hooks{}),
			sample.Define("relay", []string{"greeter"}, sample.Hooks{}),
		},
		Plugins: []coretest.Plugin{
			hello,
			{Name: "relayer", Requires: []string{"relay"}},
			{Name: "absent", Requires: []string{"greeter"}},
		},
	})
	h.SeedConfig("greeter", configured)
	h.SeedConfig("relay", configured)
	h.Install("hello")
	h.Install("relayer")
	h.WaitState("greeter", runtime.OK)
	h.WaitState("relay", runtime.OK)

	if res := h.Call("relayer", "relay", "Greet", `{"name":"Ada"}`); res.Code != http.StatusOK {
		t.Errorf("relayer → relay: status %d: %s", res.Code, res.Body)
	}
	for _, c := range []struct{ plugin, component, why string }{
		{"relayer", "greeter", "required only through relay"},
		{"hello", "relay", "not required"},
		{"absent", "greeter", "the Plugin isn't installed"},
		{"", "greeter", "no Plugin named"},
		{"hello", "nonexistent", "no such Component"},
	} {
		res := h.Call(c.plugin, c.component, "Greet", `{"name":"Ada"}`)
		if status, code, msg := connectError(t, res); status != http.StatusForbidden || code != "permission_denied" {
			t.Errorf("%q → %s (%s): %d %s %q, want 403 permission_denied", c.plugin, c.component, c.why, status, code, msg)
		}
	}
}
