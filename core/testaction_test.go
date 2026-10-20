package core_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	ntfyapi "github.com/itsKontra/ddash/components/ntfy-api"
	"github.com/itsKontra/ddash/core/appstate"
	"github.com/itsKontra/ddash/core/coretest"
	"github.com/itsKontra/ddash/core/internal/sample"
	"github.com/itsKontra/ddash/core/runtime"
	"github.com/itsKontra/ddash/sdk"
)

var notify = coretest.Plugin{Name: "notify", Requires: []string{"ntfy-api"}}

// ntfyServer fakes an ntfy server answering every publish with code and
// counts them.
type ntfyServer struct {
	*httptest.Server
	mu     sync.Mutex
	code   int
	titles []string
}

func newNtfy(t *testing.T) *ntfyServer {
	s := &ntfyServer{code: http.StatusOK}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var m struct{ Title string }
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &m)
		s.mu.Lock()
		defer s.mu.Unlock()
		s.titles = append(s.titles, m.Title)
		w.WriteHeader(s.code)
		if s.code == http.StatusOK {
			_, _ = io.WriteString(w, `{"id":"sPs71M8A2T"}`)
		} else {
			_, _ = io.WriteString(w, `{"code":40101,"http":401,"error":"unauthorized"}`)
		}
	}))
	t.Cleanup(s.Close)
	return s
}

func (s *ntfyServer) answer(code int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.code = code
}

func (s *ntfyServer) published() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.titles...)
}

func TestTheConfigFormsTestButtonRunsTheTestActionOnTheSavedConfig(t *testing.T) {
	srv := newNtfy(t)
	h := coretest.New(t, coretest.Options{Components: []sdk.Definition{ntfyapi.Definition()}, Plugins: []coretest.Plugin{notify}})
	h.Install("notify")
	h.WaitState("ntfy-api", runtime.Unconfigured)

	form := coreJSON(t, h, "GetConfigForm", `{"owner":"ntfy-api"}`)
	test, _ := form["testAction"].(map[string]any)
	if test["method"] != "SendTest" || test["label"] != "Send test" {
		t.Errorf("form test action = %v, want SendTest labelled Send test", form["testAction"])
	}
	if server := fieldNamed(form, "server"); server["default"] != "https://ntfy.sh" {
		t.Errorf("server field = %v, want default https://ntfy.sh", server)
	}

	if res := h.CoreCall("RunTestAction", `{"owner":"ntfy-api"}`); res.Code == http.StatusOK || !strings.Contains(res.Body.String(), "not configured") {
		t.Errorf("RunTestAction before a config = %d %s, want not configured", res.Code, res.Body)
	}

	values, _ := json.Marshal(map[string]any{"server": srv.URL, "topic": "ddash-alerts"})
	coreJSON(t, h, "SaveConfig", `{"owner":"ntfy-api","values":`+string(values)+`}`)
	coreJSON(t, h, "RunTestAction", `{"owner":"ntfy-api"}`)

	if got := srv.published(); len(got) != 1 || got[0] != "ddash test" {
		t.Errorf("published %v, want the test Notification", got)
	}
	if e := lastEntry(h); e.Target != "ntfy-api" || e.Method != "SendTest" || e.Username != "test" || e.Outcome != appstate.OK {
		t.Errorf("Audit Log entry %+v, want operator test's ok ntfy-api SendTest", e)
	}

	srv.answer(http.StatusUnauthorized)
	res := h.CoreCall("RunTestAction", `{"owner":"ntfy-api"}`)
	var failed struct{ Code, Message string }
	_ = json.Unmarshal(res.Body.Bytes(), &failed)
	if failed.Code != "unavailable" || failed.Message != "ntfy answered 401: unauthorized" {
		t.Errorf("RunTestAction while ntfy refuses = %d %s, want unavailable: ntfy answered 401: unauthorized", res.Code, res.Body)
	}
}

func TestAFormWithoutATestActionHasNoButtonAndRefusesToRunOne(t *testing.T) {
	h := coretest.New(t, coretest.Options{Components: []sdk.Definition{greeter(sample.Hooks{})}, Plugins: []coretest.Plugin{hello}})
	h.SeedConfig("greeter", configured)
	h.Install("hello")
	h.WaitState("greeter", runtime.OK)

	if form := coreJSON(t, h, "GetConfigForm", `{"owner":"greeter"}`); form["testAction"] != nil {
		t.Errorf("test action %v, want none", form["testAction"])
	}
	res := h.CoreCall("RunTestAction", `{"owner":"greeter"}`)
	if res.Code == http.StatusOK || !strings.Contains(res.Body.String(), "no test_action") {
		t.Errorf("RunTestAction = %d %s, want a refusal naming test_action", res.Code, res.Body)
	}
}

func fieldNamed(form map[string]any, name string) map[string]any {
	fields, _ := form["fields"].([]any)
	for _, f := range fields {
		if f, _ := f.(map[string]any); f["name"] == name {
			return f
		}
	}
	return nil
}

func lastEntry(h *coretest.Harness) appstate.Entry {
	entries := h.AuditLog()
	if len(entries) == 0 {
		return appstate.Entry{}
	}
	return entries[0]
}
