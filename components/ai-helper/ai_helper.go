// Package aihelper is the ai-helper Component: it drafts a new App with
// Claude, as a Compose file and the Add App fields, returned as
// structured output against a fixed JSON schema. It asks Claude through
// anthropic-api, the one Component that talks to Anthropic. What it sends
// is the Operator's description and the context the browser showed them
// (DraftContext), under a fixed system prompt, and nothing else: it reads
// no Compose or .env file and calls no Agent. While anthropic-api has no
// API key it is not set up, and DraftApp refuses.
package aihelper

import (
	"context"
	_ "embed"
	"encoding/json"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/itsKontra/ddash/components/ai-helper/aihelperpb"
	apb "github.com/itsKontra/ddash/components/anthropic-api/anthropicapipb"
	"github.com/itsKontra/ddash/sdk"
)

//go:embed component.yaml
var manifest []byte

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
	// claude asks Claude: the only way ai-helper reaches Anthropic.
	claude apb.AnthropicApiServiceClient
}

func (c *component) Start(_ context.Context, env *sdk.Env, _ *aihelperpb.AiHelperConfig) error {
	c.claude = sdk.Require(env, apb.NewAnthropicApiServiceClient)
	return nil
}

// Health is ok: ai-helper keeps nothing; whether Claude can be asked is
// anthropic-api's.
func (c *component) Health(context.Context) sdk.Health { return sdk.Health{State: sdk.OK} }

// GetStatus is anthropic-api's: whether an API key is saved, and the model.
func (c *component) GetStatus(ctx context.Context, _ *aihelperpb.GetStatusRequest) (*aihelperpb.GetStatusResponse, error) {
	st, err := c.claude.GetStatus(ctx, &apb.GetStatusRequest{})
	if err != nil {
		return nil, err
	}
	return &aihelperpb.GetStatusResponse{SetUp: st.GetSetUp(), Model: st.GetModel()}, nil
}

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
// context as JSON indented by two spaces, exactly what the preview shows
// (JSON.stringify(context, null, 2) in the browser).
func Prompt(req *aihelperpb.DraftAppRequest) (string, error) {
	var ctx strings.Builder
	enc := json.NewEncoder(&ctx)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(contextJSON(req.GetContext())); err != nil {
		return "", err
	}
	return "Description:\n" + strings.TrimSpace(req.GetDescription()) + "\n\nContext:\n" + strings.TrimSuffix(ctx.String(), "\n"), nil
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

func (c *component) DraftApp(ctx context.Context, req *aihelperpb.DraftAppRequest) (*aihelperpb.DraftAppResponse, error) {
	if strings.TrimSpace(req.GetDescription()) == "" {
		return nil, status.Error(codes.InvalidArgument, "describe the app to draft, e.g. \"Paperless-ngx with Postgres and Redis\"")
	}
	prompt, err := Prompt(req)
	if err != nil {
		return nil, err
	}
	schema, err := json.Marshal(draftSchema)
	if err != nil {
		return nil, err
	}
	// anthropic-api's refusals (no API key, a refused key, an unknown
	// model, a limit) already read as the Operator's fix.
	ans, err := c.claude.Generate(ctx, &apb.GenerateRequest{System: SystemPrompt, Prompt: prompt, JsonSchema: string(schema)})
	if err != nil {
		return nil, err
	}
	switch ans.GetStop() {
	case apb.Stop_STOP_DECLINED:
		return nil, status.Error(codes.FailedPrecondition, "Claude declined to draft this: describe the app differently")
	case apb.Stop_STOP_CUT_OFF:
		return nil, status.Error(codes.Unavailable, "the draft was cut off: describe a smaller app, or try again")
	}
	var out draftOut
	if err := json.Unmarshal([]byte(ans.GetText()), &out); err != nil {
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
		Model:   ans.GetModel(),
		App: &aihelperpb.DraftedApp{
			Url: strings.TrimSpace(out.App.URL), Name: strings.TrimSpace(out.App.Name),
			Icon:  strings.ToLower(strings.TrimSpace(out.App.Icon)),
			Group: strings.TrimSpace(out.App.Group), Service: strings.TrimSpace(out.App.Service), Port: port,
		},
	}}, nil
}
