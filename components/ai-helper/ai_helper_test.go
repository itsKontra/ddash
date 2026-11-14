package aihelper_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	aihelper "github.com/itsKontra/ddash/components/ai-helper"
	"github.com/itsKontra/ddash/components/ai-helper/aihelperpb"
	"github.com/itsKontra/ddash/components/ai-helper/anthropicfake"
	"github.com/itsKontra/ddash/sdk/sdktest"
)

func ptr[T any](v T) *T { return &v }

func api(t *testing.T) (*anthropicfake.API, string) {
	t.Helper()
	a, err := anthropicfake.New("testdata/baseline", anthropicfake.Options{})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(a)
	t.Cleanup(srv.Close)
	return a, srv.URL
}

func start(t *testing.T, cfg *aihelperpb.AiHelperConfig) aihelperpb.AiHelperServiceClient {
	t.Helper()
	h := sdktest.New(t, aihelper.Definition())
	h.MustStart(cfg)
	return aihelperpb.NewAiHelperServiceClient(h.Conn)
}

var paperless = &aihelperpb.DraftAppRequest{
	Description: "Paperless-ngx with Postgres and Redis",
	Context: &aihelperpb.DraftContext{
		Host: "media", Projects: []string{"jellyfin"}, UsedPorts: []uint32{8096}, Networks: []string{"proxy"}, HomeDomain: "home.example",
	},
}

// A draft sends the description and the context under the fixed system
// prompt, as structured output, and returns the Compose file and the Add
// App fields.
func TestDraftAppSendsTheDescriptionAndContextAndReturnsTheDraft(t *testing.T) {
	a, url := api(t)
	c := start(t, &aihelperpb.AiHelperConfig{ApiKey: anthropicfake.DefaultKey, Server: ptr(url)})

	st, err := c.GetStatus(t.Context(), &aihelperpb.GetStatusRequest{})
	if err != nil || !st.GetSetUp() || st.GetModel() != "claude-sonnet-5-5" {
		t.Fatalf("GetStatus = %v, %v", st, err)
	}
	resp, err := c.DraftApp(t.Context(), paperless)
	if err != nil {
		t.Fatal(err)
	}
	d := resp.GetDraft()
	if d.GetName() != "paperless" || !strings.Contains(d.GetCompose(), "image: postgres:16.6") || d.GetApp().GetService() != "paperless" || d.GetApp().GetPort() != 8010 || d.GetApp().GetIcon() != "paperless-ngx" || d.GetModel() != "claude-sonnet-5-5" {
		t.Errorf("draft = %v", d)
	}

	reqs := a.Requests()
	if len(reqs) != 1 {
		t.Fatalf("%d requests, want 1", len(reqs))
	}
	body := reqs[0].Body
	if body["model"] != "claude-sonnet-5-5" {
		t.Errorf("model = %v", body["model"])
	}
	system, _ := body["system"].([]any)
	if len(system) != 1 || system[0].(map[string]any)["text"] != aihelper.SystemPrompt {
		t.Errorf("system = %v", body["system"])
	}
	out, _ := body["output_config"].(map[string]any)
	format, _ := out["format"].(map[string]any)
	if format["type"] != "json_schema" || format["schema"] == nil {
		t.Errorf("output_config = %v", body["output_config"])
	}
	if _, forced := body["tool_choice"]; forced {
		t.Error("the draft forces a tool call, which this model refuses")
	}
	// Exactly the preview's text is the one user message.
	want, err := aihelper.Prompt(paperless)
	if err != nil {
		t.Fatal(err)
	}
	msgs, _ := body["messages"].([]any)
	if len(msgs) != 1 {
		t.Fatalf("messages = %v", body["messages"])
	}
	content, _ := msgs[0].(map[string]any)["content"].([]any)
	if len(content) != 1 || content[0].(map[string]any)["text"] != want {
		t.Errorf("message = %v, want the text %q", msgs[0], want)
	}
	for _, sent := range []string{`"host": "media"`, `"jellyfin"`, "8096", `"proxy"`, `"home_domain": "home.example"`} {
		if !strings.Contains(want, sent) {
			t.Errorf("the prompt lacks %s:\n%s", sent, want)
		}
	}
	if !strings.Contains(reqs[0].Header.Get("anthropic-beta"), "server-side-fallback") {
		t.Errorf("anthropic-beta = %q, want the refusal fallback", reqs[0].Header.Get("anthropic-beta"))
	}
}

// The prompt is the description and the context as the browser's preview
// writes it (JSON.stringify(context, null, 2)): plugins/ai-helper's test
// expects the same text.
func TestThePromptIsWhatThePreviewShows(t *testing.T) {
	got, err := aihelper.Prompt(&aihelperpb.DraftAppRequest{
		Description: "  Paperless-ngx with Postgres & Redis <fast>\n",
		Context:     &aihelperpb.DraftContext{Host: "media", Projects: []string{"jellyfin"}, UsedPorts: []uint32{8096, 9000}},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := `Description:
Paperless-ngx with Postgres & Redis <fast>

Context:
{
  "host": "media",
  "projects": [
    "jellyfin"
  ],
  "used_ports": [
    8096,
    9000
  ],
  "networks": [],
  "home_domain": ""
}`
	if got != want {
		t.Errorf("prompt =\n%s\nwant\n%s", got, want)
	}
}

// Without a key the helper starts, says so, and drafts nothing.
func TestAnEmptyConfigIsNotSetUp(t *testing.T) {
	c := start(t, &aihelperpb.AiHelperConfig{})
	st, err := c.GetStatus(t.Context(), &aihelperpb.GetStatusRequest{})
	if err != nil || st.GetSetUp() {
		t.Fatalf("GetStatus = %v, %v", st, err)
	}
	if _, err := c.DraftApp(t.Context(), paperless); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("DraftApp = %v, want FailedPrecondition", err)
	}
}

func TestABadConfigIsRefused(t *testing.T) {
	h := sdktest.New(t, aihelper.Definition())
	if err := h.Start(&aihelperpb.AiHelperConfig{ApiKey: "hunter2", Server: ptr("ftp://x")}); err == nil {
		t.Fatal("a key that isn't Anthropic's and an ftp server were accepted")
	}
}

func TestAnthropicsRefusalsReadAsTheOperatorsFix(t *testing.T) {
	a, url := api(t)
	wrong := start(t, &aihelperpb.AiHelperConfig{ApiKey: "sk-ant-wrong", Server: ptr(url)})
	if _, err := wrong.DraftApp(t.Context(), paperless); status.Code(err) != codes.FailedPrecondition || !strings.Contains(status.Convert(err).Message(), "API key") {
		t.Errorf("a wrong key: %v", err)
	}

	c := start(t, &aihelperpb.AiHelperConfig{ApiKey: anthropicfake.DefaultKey, Server: ptr(url), Model: ptr("claude-haiku-5-5")})
	a.StopNext("refusal")
	if _, err := c.DraftApp(t.Context(), paperless); status.Code(err) != codes.FailedPrecondition || !strings.Contains(status.Convert(err).Message(), "declined") {
		t.Errorf("a refusal: %v", err)
	}
	a.FailNext(http.StatusTooManyRequests)
	a.FailNext(http.StatusTooManyRequests)
	if _, err := c.DraftApp(t.Context(), paperless); status.Code(err) == codes.OK {
		t.Error("a 429 drafted")
	}
	if _, err := c.DraftApp(t.Context(), &aihelperpb.DraftAppRequest{}); status.Code(err) != codes.InvalidArgument {
		t.Errorf("no description: %v", err)
	}
	// A model without the refusal fallback isn't sent one.
	last := a.Requests()[len(a.Requests())-1]
	if last.Body["model"] != "claude-haiku-5-5" || last.Body["fallbacks"] != nil {
		t.Errorf("haiku's request = %v", last.Body)
	}
}
