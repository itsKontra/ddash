// Package aihelper is the ai-helper Component: it drafts a new App with
// Claude through Anthropic's Messages API, as a Compose file and the Add
// App fields, returned as structured output against a fixed JSON schema.
// What it sends is the Operator's description and the context the
// browser showed them (DraftContext), under a fixed system prompt, and
// nothing else: it reads no Compose or .env file and calls no Agent.
// Without an API key it is not set up: it starts, and DraftApp refuses.
package aihelper

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

	"github.com/itsKontra/ddash/components/ai-helper/aihelperpb"
	"github.com/itsKontra/ddash/sdk"
)

//go:embed component.yaml
var manifest []byte

// The config's defaults, for a config the host didn't fill in (a test's).
const (
	DefaultModel  = "claude-sonnet-5-5"
	DefaultServer = "https://api.anthropic.com"
)

// draftTimeout bounds one draft, inside the host's 60 s Action deadline.
const draftTimeout = 55 * time.Second

// maxTokens is the most a draft may take: a Compose file with a few
// services and the fields fit well within it.
const maxTokens = 16000

// Definition is ai-helper, ready for sdk.Serve.
func Definition() sdk.Definition {
	return sdk.Define(sdk.Spec[*aihelperpb.AiHelperConfig]{
		Manifest: manifest,
		Service:  &aihelperpb.AiHelperService_ServiceDesc,
		New:      func() sdk.Component[*aihelperpb.AiHelperConfig] { return &component{} },
	})
}

type component struct {
	aihelperpb.UnimplementedAiHelperServiceServer
	model  string
	client *anthropic.Client
}

func (c *component) Start(_ context.Context, _ *sdk.Env, cfg *aihelperpb.AiHelperConfig) error {
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
			option.WithHTTPClient(&http.Client{Timeout: draftTimeout}),
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

func (c *component) GetStatus(context.Context, *aihelperpb.GetStatusRequest) (*aihelperpb.GetStatusResponse, error) {
	return &aihelperpb.GetStatusResponse{SetUp: c.client != nil, Model: c.model}, nil
}

// errNotSetUp is what DraftApp answers without an API key.
var errNotSetUp = status.Error(codes.FailedPrecondition, "the AI App helper is not set up: enter an Anthropic API key in its settings")

// SystemPrompt is the fixed instruction every draft is made under.
const SystemPrompt = `You draft a new self-hosted app for a homelab dashboard called ddash. The operator describes the app; you answer with a Docker Compose file and the fields of ddash's Add App form. The operator reviews everything before anything runs, so draft something that runs as it is, without placeholders they must find.

The Compose file:
- Uses the app's official image (or the image its docs recommend), pinned to a specific version tag. Never "latest", never a digest.
- Gives every service "restart: unless-stopped".
- Keeps data in bind mounts under the project directory, e.g. "./data:/data" or "./postgres:/var/lib/postgresql/data". No named volumes, no absolute host paths.
- Sets a TZ environment variable on every service ("TZ: Etc/UTC" unless the context says otherwise), and PUID/PGID 1000 where the image supports them.
- Publishes exactly one host port for the web UI, on the main service, choosing a port that is not among the context's used_ports. Databases, caches and workers publish nothing and talk over the project's default network.
- Adds healthchecks where the image documents a simple one, and depends_on with condition: service_healthy for databases and caches.
- Writes any password or secret the app needs as a long random-looking value inline, and says in notes that the operator should change it. Never refer to an .env file; you never see one.
- Has no top-level "version" key and no "container_name". Joins a network from the context's networks only if the description asks for it.

The Add App fields:
- name: the app's display name, e.g. "Paperless-ngx".
- url: "https://<short-name>.<home_domain>" when the context gives a home_domain, else "".
- icon: the app's homarr-labs/dashboard-icons name (lower-case, dashes, e.g. "paperless-ngx", "home-assistant") when you know it, else "".
- group: a short section like "Media", "Documents", "Home", "Monitoring" or "Tools".
- service and port: the Compose service the web UI runs in and its published host port.

The project name ("name" at the top level) is short, lower-case letters, digits and dashes, not among the context's projects. notes: at most a few short sentences on what the operator must change or know; empty when nothing.`

// draftSchema is the structured output every draft follows.
var draftSchema = map[string]any{
	"type":                 "object",
	"additionalProperties": false,
	"required":             []string{"name", "compose", "app", "notes"},
	"properties": map[string]any{
		"name":    map[string]any{"type": "string", "description": "The Compose Project's name."},
		"compose": map[string]any{"type": "string", "description": "The Compose file's YAML text."},
		"notes":   map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
		"app": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"url", "name", "icon", "group", "service", "port"},
			"properties": map[string]any{
				"url":     map[string]any{"type": "string"},
				"name":    map[string]any{"type": "string"},
				"icon":    map[string]any{"type": "string"},
				"group":   map[string]any{"type": "string"},
				"service": map[string]any{"type": "string"},
				"port":    map[string]any{"type": "integer"},
			},
		},
	},
}

// Prompt is the user message sent for req: its description and its
// context as JSON, exactly what the preview shows.
func Prompt(req *aihelperpb.DraftAppRequest) (string, error) {
	ctx, err := json.MarshalIndent(contextJSON(req.GetContext()), "", "  ")
	if err != nil {
		return "", err
	}
	return "Description:\n" + strings.TrimSpace(req.GetDescription()) + "\n\nContext:\n" + string(ctx), nil
}

// contextOut is DraftContext as sent: every field, empty ones too.
type contextOut struct {
	Host       string   `json:"host"`
	Projects   []string `json:"projects"`
	UsedPorts  []uint32 `json:"used_ports"`
	Networks   []string `json:"networks"`
	HomeDomain string   `json:"home_domain"`
}

func contextJSON(c *aihelperpb.DraftContext) contextOut {
	return contextOut{
		Host:       c.GetHost(),
		Projects:   orEmpty(c.GetProjects()),
		UsedPorts:  orEmpty(c.GetUsedPorts()),
		Networks:   orEmpty(c.GetNetworks()),
		HomeDomain: c.GetHomeDomain(),
	}
}

func orEmpty[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}

// draftOut is the structured output, as the schema has it.
type draftOut struct {
	Name    string   `json:"name"`
	Compose string   `json:"compose"`
	Notes   []string `json:"notes"`
	App     struct {
		URL     string `json:"url"`
		Name    string `json:"name"`
		Icon    string `json:"icon"`
		Group   string `json:"group"`
		Service string `json:"service"`
		Port    int64  `json:"port"`
	} `json:"app"`
}

// withFallbacks are the models that take the server-side refusal
// fallback: a declined draft is retried on the model Anthropic routes it
// to, within the same call.
var withFallbacks = []string{"claude-sonnet-5-5", "claude-opus-5-5", "claude-opus-5", "claude-fable-5-1", "claude-fable-5-2"}

func (c *component) DraftApp(ctx context.Context, req *aihelperpb.DraftAppRequest) (*aihelperpb.DraftAppResponse, error) {
	if c.client == nil {
		return nil, errNotSetUp
	}
	if strings.TrimSpace(req.GetDescription()) == "" {
		return nil, status.Error(codes.InvalidArgument, "describe the app to draft, e.g. \"Paperless-ngx with Postgres and Redis\"")
	}
	prompt, err := Prompt(req)
	if err != nil {
		return nil, err
	}
	params := anthropic.BetaMessageNewParams{
		Model:     c.model,
		MaxTokens: maxTokens,
		System:    []anthropic.BetaTextBlockParam{{Text: SystemPrompt}},
		Messages:  []anthropic.BetaMessageParam{anthropic.NewBetaUserMessage(anthropic.NewBetaTextBlock(prompt))},
		// A draft is routine work, and must fit the Action's deadline.
		OutputConfig: anthropic.BetaOutputConfigParam{
			Effort: anthropic.BetaOutputConfigEffortLow,
			Format: anthropic.BetaJSONOutputFormatParam{Schema: draftSchema},
		},
	}
	if slices.Contains(withFallbacks, c.model) {
		params.Betas = []anthropic.AnthropicBeta{anthropic.AnthropicBetaServerSideFallback2026_07_01}
		params.Fallbacks = anthropic.BetaFallbacksParamOfDefault()
	}
	ctx, cancel := context.WithTimeout(ctx, draftTimeout)
	defer cancel()
	msg, err := c.client.Beta.Messages.New(ctx, params)
	if err != nil {
		return nil, apiError(err)
	}
	switch msg.StopReason {
	case anthropic.BetaStopReasonRefusal:
		return nil, status.Error(codes.FailedPrecondition, "Claude declined to draft this: describe the app differently")
	case anthropic.BetaStopReasonMaxTokens:
		return nil, status.Error(codes.Unavailable, "the draft was cut off: describe a smaller app, or try again")
	}
	var text strings.Builder
	for _, b := range msg.Content {
		if t, ok := b.AsAny().(anthropic.BetaTextBlock); ok {
			text.WriteString(t.Text)
		}
	}
	var out draftOut
	if err := json.Unmarshal([]byte(text.String()), &out); err != nil {
		return nil, status.Errorf(codes.Unavailable, "Claude's draft isn't the JSON asked for: %v", err)
	}
	if strings.TrimSpace(out.Compose) == "" {
		return nil, status.Error(codes.Unavailable, "Claude's draft has no Compose file: try again")
	}
	port := uint32(0)
	if out.App.Port > 0 && out.App.Port <= 65535 {
		port = uint32(out.App.Port)
	}
	return &aihelperpb.DraftAppResponse{Draft: &aihelperpb.Draft{
		Name:    strings.TrimSpace(out.Name),
		Compose: out.Compose,
		Notes:   out.Notes,
		Model:   string(msg.Model),
		App: &aihelperpb.DraftedApp{
			Url: strings.TrimSpace(out.App.URL), Name: strings.TrimSpace(out.App.Name),
			Icon:  strings.ToLower(strings.TrimSpace(out.App.Icon)),
			Group: strings.TrimSpace(out.App.Group), Service: strings.TrimSpace(out.App.Service), Port: port,
		},
	}}, nil
}

// apiError is a failed Messages API call as the Operator reads it.
func apiError(err error) error {
	var e *anthropic.Error
	if errors.As(err, &e) {
		switch e.StatusCode {
		case http.StatusUnauthorized, http.StatusForbidden:
			return status.Error(codes.FailedPrecondition, "Anthropic refused the API key: enter a valid one in the AI App helper's settings")
		case http.StatusNotFound:
			return status.Error(codes.FailedPrecondition, "Anthropic doesn't know the model: pick another in the AI App helper's settings")
		case http.StatusTooManyRequests:
			return status.Error(codes.ResourceExhausted, "Anthropic is rate-limiting this key: try again in a minute")
		}
		return status.Errorf(codes.Unavailable, "Anthropic answered %d: %s", e.StatusCode, firstLine(e.Message))
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return status.Error(codes.DeadlineExceeded, "Claude took too long to draft: try again")
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
