// Package anthropicapi is the anthropic-api Component: it asks Claude
// through Anthropic's Messages API for an answer as structured output
// against the caller's JSON schema. It sends the system prompt and the
// prompt it is given, under the Operator's API key, and nothing else.
// Without an API key it is not set up: it starts, and Generate refuses.
package anthropicapi

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/anthropics/anthropic-sdk-go/v2"
	"github.com/anthropics/anthropic-sdk-go/v2/option"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pb "github.com/itsKontra/ddash/components/anthropic-api/anthropicapipb"
	"github.com/itsKontra/ddash/sdk"
)

//go:embed component.yaml
var manifest []byte

// The config's defaults, for a config the host didn't fill in (a test's).
const (
	DefaultModel  = "claude-sonnet-5-5"
	DefaultServer = "https://api.anthropic.com"
)

// requestTimeout bounds one request, inside the host's 60 s Action
// deadline and the caller's hop to here.
const requestTimeout = 55 * time.Second

// maxTokens is the most an answer may take: a Compose file with a few
// services and its fields fit well within it.
const maxTokens = 16000

// Definition is anthropic-api, ready for sdk.Serve.
func Definition() sdk.Definition {
	return sdk.Define(sdk.Spec[*pb.AnthropicApiConfig]{
		Manifest: manifest,
		Service:  &pb.AnthropicApiService_ServiceDesc,
		New:      func() sdk.Component[*pb.AnthropicApiConfig] { return &component{} },
	})
}

type component struct {
	pb.UnimplementedAnthropicApiServiceServer
	model  string
	client *anthropic.Client
}

func (c *component) Start(_ context.Context, _ *sdk.Env, cfg *pb.AnthropicApiConfig) error {
	var errs sdk.FieldErrors
	server := DefaultServer
	if cfg.Server != nil {
		server = strings.TrimSpace(cfg.GetServer())
	}
	if u, err := url.Parse(server); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		errs.Add("server", "must be an http or https URL")
	}
	c.model = DefaultModel
	if cfg.Model != nil {
		c.model = strings.TrimSpace(cfg.GetModel())
	}
	if c.model == "" || strings.ContainsAny(c.model, " /") {
		errs.Add("model", "must be a Claude model id, e.g. "+DefaultModel)
	}
	key := strings.TrimSpace(cfg.GetApiKey())
	if key != "" && !strings.HasPrefix(key, "sk-ant-") {
		errs.Add("api_key", "must be an Anthropic API key, starting sk-ant-")
	}
	if err := errs.Err(); err != nil {
		return err
	}
	if key != "" {
		// Only the config's key and server: never the environment's
		// credentials or profiles.
		cl := anthropic.NewClient(
			option.WithoutEnvironmentDefaults(),
			option.WithAPIKey(key),
			option.WithBaseURL(server),
			option.WithMaxRetries(1),
			option.WithHTTPClient(&http.Client{Timeout: requestTimeout}),
		)
		c.client = &cl
	}
	return nil
}

func (c *component) Health(context.Context) sdk.Health {
	if c.client == nil {
		return sdk.Health{State: sdk.OK, Message: "not set up"}
	}
	return sdk.Health{State: sdk.OK}
}

func (c *component) GetStatus(context.Context, *pb.GetStatusRequest) (*pb.GetStatusResponse, error) {
	return &pb.GetStatusResponse{SetUp: c.client != nil, Model: c.model}, nil
}

// errNotSetUp is what Generate answers without an API key.
var errNotSetUp = status.Error(codes.FailedPrecondition, "Claude is not set up: enter an Anthropic API key in anthropic-api's config")

// withFallbacks are the models that take the server-side refusal
// fallback: a declined request is retried on the model Anthropic routes
// it to, within the same call.
var withFallbacks = []string{"claude-sonnet-5-5", "claude-opus-5-5", "claude-opus-5", "claude-fable-5-1", "claude-fable-5-2"}

func (c *component) Generate(ctx context.Context, req *pb.GenerateRequest) (*pb.GenerateResponse, error) {
	if c.client == nil {
		return nil, errNotSetUp
	}
	if strings.TrimSpace(req.GetPrompt()) == "" {
		return nil, status.Error(codes.InvalidArgument, "a request needs a prompt")
	}
	var schema map[string]any
	if err := json.Unmarshal([]byte(req.GetJsonSchema()), &schema); err != nil || len(schema) == 0 {
		return nil, status.Error(codes.InvalidArgument, "json_schema must be a JSON Schema object")
	}
	params := anthropic.BetaMessageNewParams{
		Model:     c.model,
		MaxTokens: maxTokens,
		Messages:  []anthropic.BetaMessageParam{anthropic.NewBetaUserMessage(anthropic.NewBetaTextBlock(req.GetPrompt()))},
		// Routine work, which must fit the Action's deadline.
		OutputConfig: anthropic.BetaOutputConfigParam{
			Effort: anthropic.BetaOutputConfigEffortLow,
			Format: anthropic.BetaJSONOutputFormatParam{Schema: schema},
		},
	}
	if req.GetSystem() != "" {
		params.System = []anthropic.BetaTextBlockParam{{Text: req.GetSystem()}}
	}
	if slices.Contains(withFallbacks, c.model) {
		params.Betas = []anthropic.AnthropicBeta{anthropic.AnthropicBetaServerSideFallback2026_07_01}
		params.Fallbacks = anthropic.BetaFallbacksParamOfDefault()
	}
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	msg, err := c.client.Beta.Messages.New(ctx, params)
	if err != nil {
		return nil, apiError(err)
	}
	out := &pb.GenerateResponse{Stop: pb.Stop_STOP_COMPLETE, Model: string(msg.Model)}
	switch msg.StopReason {
	case anthropic.BetaStopReasonRefusal:
		out.Stop = pb.Stop_STOP_DECLINED
		return out, nil
	case anthropic.BetaStopReasonMaxTokens:
		out.Stop = pb.Stop_STOP_CUT_OFF
	}
	var text strings.Builder
	for _, b := range msg.Content {
		if t, ok := b.AsAny().(anthropic.BetaTextBlock); ok {
			text.WriteString(t.Text)
		}
	}
	out.Text = text.String()
	return out, nil
}

// apiError is a failed Messages API call as the Operator reads it.
func apiError(err error) error {
	var e *anthropic.Error
	if errors.As(err, &e) {
		switch e.StatusCode {
		case http.StatusUnauthorized, http.StatusForbidden:
			return status.Error(codes.PermissionDenied, "Anthropic refused the API key: enter a valid one in anthropic-api's config")
		case http.StatusNotFound:
			return status.Error(codes.FailedPrecondition, "Anthropic doesn't know the model: pick another in anthropic-api's config")
		case http.StatusTooManyRequests:
			return status.Error(codes.ResourceExhausted, "Anthropic is rate-limiting this key: try again in a minute")
		}
		return status.Errorf(codes.Unavailable, "Anthropic answered %d: %s", e.StatusCode, firstLine(e.Message))
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return status.Error(codes.DeadlineExceeded, "Claude took too long to answer: try again")
	}
	return status.Errorf(codes.Unavailable, "can't reach Anthropic: %v", err)
}

func firstLine(s string) string {
	s, _, _ = strings.Cut(s, "\n")
	if s == "" {
		return "no message"
	}
	return fmt.Sprintf("%.200s", s)
}
