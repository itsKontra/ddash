package anthropicapi_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	anthropicapi "github.com/itsKontra/ddash/components/anthropic-api"
	pb "github.com/itsKontra/ddash/components/anthropic-api/anthropicapipb"
	"github.com/itsKontra/ddash/components/anthropic-api/anthropicfake"
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

func start(t *testing.T, cfg *pb.AnthropicApiConfig) pb.AnthropicApiServiceClient {
	t.Helper()
	h := sdktest.New(t, anthropicapi.Definition())
	h.MustStart(cfg)
	return pb.NewAnthropicApiServiceClient(h.Conn)
}

var ask = &pb.GenerateRequest{
	System:     "Answer as JSON.",
	Prompt:     "Draft Paperless-ngx.",
	JsonSchema: `{"type":"object","additionalProperties":false,"required":["name"],"properties":{"name":{"type":"string"}}}`,
}

// A request sends the system prompt, the prompt and the schema, as
// structured output, and nothing else; the answer is the text as it came.
func TestGenerateSendsWhatItIsGivenAndReturnsTheAnswer(t *testing.T) {
	a, url := api(t)
	c := start(t, &pb.AnthropicApiConfig{ApiKey: anthropicfake.DefaultKey, Server: ptr(url)})

	st, err := c.GetStatus(t.Context(), &pb.GetStatusRequest{})
	if err != nil || !st.GetSetUp() || st.GetModel() != "claude-sonnet-5-5" {
		t.Fatalf("GetStatus = %v, %v", st, err)
	}
	got, err := c.Generate(t.Context(), ask)
	if err != nil {
		t.Fatal(err)
	}
	if got.GetStop() != pb.Stop_STOP_COMPLETE || !strings.Contains(got.GetText(), `"compose"`) || got.GetModel() != "claude-sonnet-5-5" {
		t.Errorf("Generate = %v", got)
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
	if len(system) != 1 || system[0].(map[string]any)["text"] != "Answer as JSON." {
		t.Errorf("system = %v", body["system"])
	}
	msgs, _ := body["messages"].([]any)
	if len(msgs) != 1 {
		t.Fatalf("messages = %v", body["messages"])
	}
	content, _ := msgs[0].(map[string]any)["content"].([]any)
	if len(content) != 1 || content[0].(map[string]any)["text"] != "Draft Paperless-ngx." {
		t.Errorf("message = %v", msgs[0])
	}
	out, _ := body["output_config"].(map[string]any)
	format, _ := out["format"].(map[string]any)
	schema, _ := format["schema"].(map[string]any)
	if format["type"] != "json_schema" || schema["type"] != "object" || schema["additionalProperties"] != false {
		t.Errorf("output_config = %v", body["output_config"])
	}
	if _, forced := body["tool_choice"]; forced {
		t.Error("the request forces a tool call, which this model refuses")
	}
	if !strings.Contains(reqs[0].Header.Get("anthropic-beta"), "server-side-fallback") {
		t.Errorf("anthropic-beta = %q, want the refusal fallback", reqs[0].Header.Get("anthropic-beta"))
	}
}

// Without a key it starts, says so, and asks nothing.
func TestAnEmptyConfigIsNotSetUp(t *testing.T) {
	c := start(t, &pb.AnthropicApiConfig{})

	st, err := c.GetStatus(t.Context(), &pb.GetStatusRequest{})
	if err != nil || st.GetSetUp() {
		t.Fatalf("GetStatus = %v, %v", st, err)
	}
	if _, err := c.Generate(t.Context(), ask); status.Code(err) != codes.FailedPrecondition || !strings.Contains(status.Convert(err).Message(), "anthropic-api's config") {
		t.Fatalf("Generate = %v, want FailedPrecondition saying where the key goes", err)
	}
}

func TestABadConfigIsRefused(t *testing.T) {
	h := sdktest.New(t, anthropicapi.Definition())
	if err := h.Start(&pb.AnthropicApiConfig{ApiKey: "hunter2", Server: ptr("ftp://x")}); err == nil {
		t.Fatal("a key that isn't Anthropic's and an ftp server were accepted")
	}
}

func TestARequestNeedsAPromptAndASchema(t *testing.T) {
	_, url := api(t)
	c := start(t, &pb.AnthropicApiConfig{ApiKey: anthropicfake.DefaultKey, Server: ptr(url)})

	if _, err := c.Generate(t.Context(), &pb.GenerateRequest{JsonSchema: ask.GetJsonSchema()}); status.Code(err) != codes.InvalidArgument {
		t.Errorf("no prompt: %v", err)
	}
	if _, err := c.Generate(t.Context(), &pb.GenerateRequest{Prompt: "x", JsonSchema: "not json"}); status.Code(err) != codes.InvalidArgument {
		t.Errorf("a schema that isn't JSON: %v", err)
	}
}

func TestWhatAnthropicRefusesReadsAsTheOperatorsFixAndWhatClaudeDeclinesIsAnAnswer(t *testing.T) {
	a, url := api(t)
	wrong := start(t, &pb.AnthropicApiConfig{ApiKey: "sk-ant-wrong", Server: ptr(url)})
	if _, err := wrong.Generate(t.Context(), ask); status.Code(err) != codes.PermissionDenied || !strings.Contains(status.Convert(err).Message(), "API key") {
		t.Errorf("a wrong key: %v", err)
	}

	c := start(t, &pb.AnthropicApiConfig{ApiKey: anthropicfake.DefaultKey, Server: ptr(url), Model: ptr("claude-haiku-5-5")})
	a.StopNext("refusal")
	if got, err := c.Generate(t.Context(), ask); err != nil || got.GetStop() != pb.Stop_STOP_DECLINED || got.GetText() != "" {
		t.Errorf("a refusal = %v, %v; want declined, as an answer", got, err)
	}
	a.StopNext("max_tokens")
	if got, err := c.Generate(t.Context(), ask); err != nil || got.GetStop() != pb.Stop_STOP_CUT_OFF {
		t.Errorf("an answer at the output limit = %v, %v; want cut off", got, err)
	}
	a.FailNext(http.StatusTooManyRequests)
	a.FailNext(http.StatusTooManyRequests)
	if _, err := c.Generate(t.Context(), ask); status.Code(err) != codes.ResourceExhausted {
		t.Errorf("a 429 after the retry: %v, want ResourceExhausted", err)
	}
	a.FailNext(http.StatusNotFound)
	if _, err := c.Generate(t.Context(), ask); status.Code(err) != codes.FailedPrecondition || !strings.Contains(status.Convert(err).Message(), "model") {
		t.Errorf("a model Anthropic doesn't know: %v", err)
	}
	// A model without the refusal fallback isn't sent one.
	last := a.Requests()[len(a.Requests())-1]
	if last.Body["model"] != "claude-haiku-5-5" || last.Body["fallbacks"] != nil {
		t.Errorf("haiku's request = %v", last.Body)
	}
}
