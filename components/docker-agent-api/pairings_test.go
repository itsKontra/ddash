package dockeragentapi_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/itsKontra/ddash/agent"
	"github.com/itsKontra/ddash/agent/agentfake"
	dockeragentapi "github.com/itsKontra/ddash/components/docker-agent-api"
	pb "github.com/itsKontra/ddash/components/docker-agent-api/dockeragentapipb"
	"github.com/itsKontra/ddash/sdk"
	"github.com/itsKontra/ddash/sdk/sdktest"
)

// pairedElsewhere serves a fake Docker Agent on release and pairs with it
// as another ddash would, returning that ddash's pairing.
func pairedElsewhere(t *testing.T, name, release string) dockeragentapi.Pairing {
	t.Helper()
	fake, err := agentfake.NewDocker(t.TempDir(), agentfake.DockerSeed{
		Stacks: []agentfake.Stack{{Dir: "media", Compose: jellyfin, Running: true}},
		Images: []agentfake.Image{{Ref: "jellyfin/jellyfin:10.9", ID: "sha256:a"}},
	}, agentfake.Options{StateDir: t.TempDir(), Release: release})
	if err != nil {
		t.Fatal(err)
	}
	u, stop, err := fake.Serve("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(stop)
	_, fp, err := agent.Probe(t.Context(), u)
	if err != nil {
		t.Fatal(err)
	}
	token, _, err := agent.Pair(t.Context(), u, fp, "any code")
	if err != nil {
		t.Fatal(err)
	}
	return dockeragentapi.Pairing{Name: name, URL: u, Token: token, Fingerprint: fp}
}

// startOn starts docker-agent-api on the DB at dbPath, as release.
func startOn(t *testing.T, dbPath, release string) pb.DockerAgentApiServiceClient {
	t.Helper()
	h := sdktest.New(t, dockeragentapi.Definition(), sdktest.WithDB(dbPath), func(o *sdk.InstanceOptions) { o.Release = release })
	h.MustStart(&pb.DockerAgentApiConfig{})
	return pb.NewDockerAgentApiServiceClient(h.Conn)
}

func TestKeptPairingsAreReadWithoutPairingAgain(t *testing.T) {
	ctx := context.Background()
	media, backup := pairedElsewhere(t, "media", "dev"), pairedElsewhere(t, "backup", "dev")
	db := filepath.Join(t.TempDir(), "docker-agent-api.db")

	if err := dockeragentapi.KeepPairings(ctx, db, []dockeragentapi.Pairing{media, backup}); err != nil {
		t.Fatal(err)
	}
	c := startOn(t, db, "dev")

	res, err := c.ListContainers(ctx, &pb.ListContainersRequest{})
	if err != nil {
		t.Fatal(err)
	}
	var read []string
	for _, h := range res.GetHosts() {
		if h.GetError() != "" || len(h.GetContainers()) != 1 || h.GetContainers()[0].GetService() != "jellyfin" {
			t.Errorf("host %s: %v", h.GetHost(), h)
		}
		read = append(read, h.GetHost())
	}
	if strings.Join(read, " ") != "backup media" {
		t.Errorf("hosts read = %v, want backup and media", read)
	}
}

func TestKeepingPairingsReplacesTheAgentsTheDBHeld(t *testing.T) {
	ctx := context.Background()
	db := filepath.Join(t.TempDir(), "docker-agent-api.db")
	if err := dockeragentapi.KeepPairings(ctx, db, []dockeragentapi.Pairing{pairedElsewhere(t, "old", "dev")}); err != nil {
		t.Fatal(err)
	}

	if err := dockeragentapi.KeepPairings(ctx, db, []dockeragentapi.Pairing{pairedElsewhere(t, "new", "dev")}); err != nil {
		t.Fatal(err)
	}

	res, err := startOn(t, db, "dev").ListAgents(ctx, &pb.ListAgentsRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if as := res.GetAgents(); len(as) != 1 || as[0].GetName() != "new" || as[0].GetState() != pb.AgentState_AGENT_STATE_PAIRED {
		t.Errorf("agents = %v, want only new, paired", as)
	}
}

// Their Release is the one ddash runs as, not the one built in: an Agent
// on the Release docker-agent-api is launched as is paired, not outdated.
func TestAnAgentIsOutdatedOnlyAgainstTheReleaseDdashRunsAs(t *testing.T) {
	ctx := context.Background()
	db := filepath.Join(t.TempDir(), "docker-agent-api.db")
	if err := dockeragentapi.KeepPairings(ctx, db, []dockeragentapi.Pairing{pairedElsewhere(t, "media", "v0.0.3")}); err != nil {
		t.Fatal(err)
	}

	res, err := startOn(t, db, "v0.0.3").ListAgents(ctx, &pb.ListAgentsRequest{})

	if err != nil || len(res.GetAgents()) != 1 || res.GetAgents()[0].GetState() != pb.AgentState_AGENT_STATE_PAIRED {
		t.Errorf("agents = %v, %v; want media, paired", res.GetAgents(), err)
	}
}

func TestABadPairingIsRefusedWithoutItsToken(t *testing.T) {
	const token = "0123456789abcdef0123456789abcdef-secret"
	db := filepath.Join(t.TempDir(), "docker-agent-api.db")

	err := dockeragentapi.KeepPairings(context.Background(), db, []dockeragentapi.Pairing{
		{Name: "Media", URL: "http://198.18.2.10:7443", Token: token, Fingerprint: "3fa9"},
		{Name: "backup", URL: "https://198.18.2.11:7443", Fingerprint: strings.Repeat("ab", 32)},
	})

	if err == nil {
		t.Fatal("bad pairings were kept")
	}
	for _, want := range []string{"agent 1 (Media): name", "agent 1 (Media): url", "agent 1 (Media): fingerprint", "agent 2 (backup): token is required"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("err lacks %q:\n%v", want, err)
		}
	}
	if strings.Contains(err.Error(), token) {
		t.Errorf("err holds the token:\n%v", err)
	}
}
