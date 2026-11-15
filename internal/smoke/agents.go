package smoke

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/itsKontra/ddash/agent"
	dockeragentapi "github.com/itsKontra/ddash/components/docker-agent-api"
	"github.com/itsKontra/ddash/components/docker-agent-api/dockeragentapipb"
	"github.com/itsKontra/ddash/sdk"
)

// DockerAgentsVar is the smoke.env variable naming the Docker Agents
// smoke reads Docker through, as JSON: {"agents": [{"name", "url",
// "token", "fingerprint"}]}, each the pairing the deployed ddash keeps.
// An Agent accepts only the token of its one pairing, so smoke borrows
// that ddash's, read-only, and never logs it.
const DockerAgentsVar = "SMOKE_DOCKER_AGENT_API"

// probeTimeout bounds asking one Agent which Release it runs.
const probeTimeout = 5 * time.Second

// dockerAgents reads DockerAgentsVar's pairings; none when it is unset.
// Its errors never hold a token.
func dockerAgents(env map[string]string) ([]dockeragentapi.Pairing, error) {
	text := strings.TrimSpace(env[DockerAgentsVar])
	if text == "" {
		return nil, nil
	}
	var v struct {
		Agents []dockeragentapi.Pairing `json:"agents"`
	}
	dec := json.NewDecoder(strings.NewReader(text))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&v); err != nil {
		var syntax *json.SyntaxError
		var typ *json.UnmarshalTypeError
		switch {
		case errors.As(err, &syntax):
			return nil, fmt.Errorf("%s isn't valid JSON (at byte %d)", DockerAgentsVar, syntax.Offset)
		case errors.As(err, &typ):
			return nil, fmt.Errorf("%s: %s must be a %s", DockerAgentsVar, typ.Field, typ.Type)
		default:
			return nil, fmt.Errorf("%s: %w", DockerAgentsVar, err)
		}
	}
	if len(v.Agents) == 0 {
		return nil, fmt.Errorf("%s names no Agent: drop it, or list the Agents as smoke.env.example shows", DockerAgentsVar)
	}
	return v.Agents, nil
}

// startDockerAgents runs docker-agent-api over the Docker Agents
// smoke.env names (possibly none) and returns its client, for docker-api.
// Its DB is a temp file, removed by stop, so no copy of a token stays.
//
// It runs as the Release the Agents run, which the deployed ddash updated
// them to: this build's own Release would find them outdated and read
// nothing.
func startDockerAgents(ctx context.Context, opts Options) (clients []any, stop func(), err error) {
	pairings, err := dockerAgents(opts.Env)
	if err != nil {
		return nil, nil, err
	}
	dir, err := os.MkdirTemp("", "ddash-smoke-agents-")
	if err != nil {
		return nil, nil, err
	}
	removeDir := func() { os.RemoveAll(dir) }
	db := filepath.Join(dir, "docker-agent-api.db")
	if err := dockeragentapi.KeepPairings(ctx, db, pairings); err != nil {
		removeDir()
		return nil, nil, fmt.Errorf("%s: %w", DockerAgentsVar, err)
	}
	release := agentsRelease(ctx, pairings)
	if release != sdk.Release {
		fmt.Fprintf(opts.Log, "docker-agent-api runs as %s, the Release the Docker Agents run (this build is %s)\n", release, sdk.Release)
	}
	conn, stopAgents, err := launchAs(ctx, dockeragentapi.Definition(), &dockeragentapipb.DockerAgentApiConfig{}, db, release, opts.Log)
	if err != nil {
		removeDir()
		return nil, nil, fmt.Errorf("docker-agent-api: %w", err)
	}
	return []any{dockeragentapipb.NewDockerAgentApiServiceClient(conn)}, func() { stopAgents(); removeDir() }, nil
}

// agentsRelease is the Release of the first of pairings' Agents that
// says hello (which takes no token), else this build's.
func agentsRelease(ctx context.Context, pairings []dockeragentapi.Pairing) string {
	for _, p := range pairings {
		ctx, cancel := context.WithTimeout(ctx, probeTimeout)
		hello, _, err := agent.Probe(ctx, p.URL)
		cancel()
		if err == nil && hello.Release != "" {
			return hello.Release
		}
	}
	return sdk.Release
}
