package smoke_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/itsKontra/ddash/agent"
	"github.com/itsKontra/ddash/agent/agentfake"
	dockeragentapi "github.com/itsKontra/ddash/components/docker-agent-api"
	"github.com/itsKontra/ddash/internal/scrub"
	"github.com/itsKontra/ddash/internal/smoke"
)

// dockerAgent serves a fake Docker Agent on release running one container,
// pairs with it as the deployed ddash did, and returns that pairing: what
// the Operator copies into smoke.env.
func dockerAgent(t *testing.T, name, release string) dockeragentapi.Pairing {
	t.Helper()
	p, _ := fakeDockerAgent(t, name, release)
	return p
}

// fakeDockerAgent is dockerAgent, also returning the fake.
func fakeDockerAgent(t *testing.T, name, release string) (dockeragentapi.Pairing, *agentfake.Docker) {
	t.Helper()
	fake, err := agentfake.NewDocker(t.TempDir(), agentfake.DockerSeed{
		Stacks: []agentfake.Stack{{Dir: "media", Running: true, Compose: "services:\n  jellyfin:\n    image: jellyfin/jellyfin:10.10.3\n"}},
		Images: []agentfake.Image{{Ref: "jellyfin/jellyfin:10.10.3", ID: "sha256:jf"}},
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
	token, _, err := agent.Pair(t.Context(), u, fp, "the deployed ddash's code")
	if err != nil {
		t.Fatal(err)
	}
	return dockeragentapi.Pairing{Name: name, URL: u, Token: token, Fingerprint: fp}, fake
}

// agentsVar is DockerAgentsVar's value naming pairings.
func agentsVar(t *testing.T, pairings ...dockeragentapi.Pairing) string {
	t.Helper()
	b, err := json.Marshal(map[string]any{"agents": pairings})
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// noToken fails t when the run's log or error holds one of pairings'
// tokens.
func noToken(t *testing.T, log string, err error, pairings ...dockeragentapi.Pairing) {
	t.Helper()
	for _, p := range pairings {
		if strings.Contains(log, p.Token) || (err != nil && strings.Contains(err.Error(), p.Token)) {
			t.Errorf("%s's token was logged:\n%s\n%v", p.Name, log, err)
		}
	}
}

func TestSmokeReadsDockerThroughTheAgentsOfALabWithoutASocketProxy(t *testing.T) {
	apps, backup := dockerAgent(t, "apps", agent.Release), dockerAgent(t, "backup", agent.Release)
	env := envFor(t, lab(t, repoRoot))
	delete(env, "SMOKE_DOCKER_API")
	env[smoke.DockerAgentsVar] = agentsVar(t, apps, backup)

	log, err := run(t, env, nil)

	if err != nil {
		t.Fatalf("smoke failed: %v\n%s", err, log)
	}
	for _, want := range []string{"ok docker-api ListContainers\n", "ok docker-api health\n"} {
		if !strings.Contains(log, want) {
			t.Errorf("log lacks %q:\n%s", want, log)
		}
	}
	noToken(t, log, err, apps, backup)
}

func TestTheSmokeRunOnlyReadsAnAgentAndLeavesTheDeployedPairingAsItWas(t *testing.T) {
	deployed, fake := fakeDockerAgent(t, "apps", agent.Release)
	env := envFor(t, lab(t, repoRoot))
	delete(env, "SMOKE_DOCKER_API")
	env[smoke.DockerAgentsVar] = agentsVar(t, deployed)
	before := len(fake.Requests()) // the deployed ddash's pairing

	log, err := run(t, env, nil)

	if err != nil {
		t.Fatalf("smoke failed: %v\n%s", err, log)
	}
	reads := []string{"GET " + agent.PathHello, "GET " + agent.PathInfo, "GET " + agent.PathProjects}
	for _, r := range fake.Requests()[before:] {
		if !slices.Contains(reads, r) {
			t.Errorf("the smoke run sent the Agent %s; it may only read (%v)", r, reads)
		}
	}
	if fake.Fingerprint() != deployed.Fingerprint {
		t.Error("the Agent's certificate changed")
	}
	var info agent.Info
	if err := agent.Dial(deployed.URL, deployed.Token, deployed.Fingerprint).Get(t.Context(), agent.PathInfo, nil, &info); err != nil {
		t.Errorf("the deployed ddash can't read the Agent after the smoke run: %v", err)
	}
}

func TestSmokeReadsASocketProxyAndAnAgentTogether(t *testing.T) {
	env := envFor(t, lab(t, repoRoot))
	env[smoke.DockerAgentsVar] = agentsVar(t, dockerAgent(t, "apps", agent.Release))

	log, err := run(t, env, nil)

	if err != nil || !strings.Contains(log, "ok docker-api ListContainers\n") {
		t.Fatalf("smoke failed: %v\n%s", err, log)
	}
}

func TestADockerAgentThatCantBeReadFailsNamingItBesidesASocketProxy(t *testing.T) {
	refused, moved := dockerAgent(t, "apps", agent.Release), dockerAgent(t, "backup", agent.Release)
	refused.Token = agent.NewToken()
	moved.Fingerprint = strings.Repeat("ab", 32)
	env := envFor(t, lab(t, repoRoot))
	env[smoke.DockerAgentsVar] = agentsVar(t, refused, moved)

	log, err := run(t, env, nil)

	for _, host := range []string{"apps", "backup"} {
		if err == nil || !strings.Contains(err.Error(), "host "+host+":") {
			t.Errorf("err = %v, want the Agent %s named\n%s", err, host, log)
		}
	}
	if !strings.Contains(log, "ok adguard-api ListQueries\n") {
		t.Errorf("the other systems didn't run:\n%s", log)
	}
	noToken(t, log, err, refused, moved)
}

func TestAnAgentOnTheDeployedReleaseIsReadAsThatRelease(t *testing.T) {
	deployed := dockerAgent(t, "apps", "v0.0.3-rc.2")
	env := envFor(t, lab(t, repoRoot))
	delete(env, "SMOKE_DOCKER_API")
	env[smoke.DockerAgentsVar] = agentsVar(t, deployed)

	log, err := run(t, env, nil)

	if err != nil {
		t.Fatalf("smoke failed: %v\n%s", err, log)
	}
	if !strings.Contains(log, "docker-agent-api runs as v0.0.3-rc.2, the Release the Docker Agents run") {
		t.Errorf("log doesn't say which Release it reads the Agents as:\n%s", log)
	}
}

func TestALabWithNeitherASocketProxyNorAnAgentSkipsDocker(t *testing.T) {
	env := envFor(t, lab(t, repoRoot))
	delete(env, "SMOKE_DOCKER_API")

	log, err := run(t, env, nil)

	if err != nil {
		t.Fatalf("smoke failed: %v\n%s", err, log)
	}
	if want := "skip docker-api: neither SMOKE_DOCKER_API nor SMOKE_DOCKER_AGENT_API is set in smoke.env\n"; !strings.Contains(log, want) {
		t.Errorf("log lacks %q:\n%s", want, log)
	}
}

func TestABadDockerAgentsVariableFailsWithoutEchoingTheToken(t *testing.T) {
	const token = "c0ffee-not-to-be-logged-c0ffee-c0ffee"
	for _, value := range []string{
		`[{"name": "apps", "token": "` + token + `"]`,
		`{"agents": [{"name": "apps", "url": "https://198.18.2.10:7443", "token": "` + token + `", "fingerprint": 5}]}`,
		`{"agents": [{"name": "apps", "url": "https://198.18.2.10:7443", "token": "` + token + `", "fingerprint": "<sha256>"}]}`,
		`{"agents": [{"name": "apps", "url": "https://198.18.2.10:7443", "tokn": "` + token + `"}]}`,
		`{"agents": []}`,
	} {
		env := envFor(t, lab(t, repoRoot))
		env[smoke.DockerAgentsVar] = value

		log, err := run(t, env, nil)

		if err == nil || !strings.Contains(err.Error(), "docker-api "+smoke.DockerAgentsVar) {
			t.Errorf("%s: err = %v, want the variable named", value, err)
		}
		if strings.Contains(log, token) || (err != nil && strings.Contains(err.Error(), token)) {
			t.Errorf("%s: the token was logged:\n%s\n%v", value, log, err)
		}
	}
}

func TestRecordingALabWithAgentsOnlyLeavesDockerUnrecorded(t *testing.T) {
	root := t.TempDir()
	s, err := scrub.New(scrub.Config{Key: []byte("test key"), Subnets: []scrub.Subnet{{Prefix: mustPrefix(t, "198.18.1.0/24"), N: 7}}})
	if err != nil {
		t.Fatal(err)
	}
	env := envFor(t, lab(t, repoRoot))
	delete(env, "SMOKE_DOCKER_API")
	env[smoke.DockerAgentsVar] = agentsVar(t, dockerAgent(t, "apps", agent.Release))

	log, err := run(t, env, &smoke.Record{Root: root, Scenario: "baseline", Scrubber: s})

	if err != nil {
		t.Fatalf("record failed: %v\n%s", err, log)
	}
	if !strings.Contains(log, "not recorded docker-api: nothing to record") || !strings.Contains(log, "ok docker-api ListContainers\n") {
		t.Errorf("docker-api wasn't read unrecorded:\n%s", log)
	}
	if _, err := os.Stat(filepath.Join(root, "components/docker-api")); err == nil {
		t.Error("docker-api was recorded")
	}
}
