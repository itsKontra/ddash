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
