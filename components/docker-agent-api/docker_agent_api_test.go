package dockeragentapi_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/itsKontra/ddash/agent/agentfake"
	dockeragentapi "github.com/itsKontra/ddash/components/docker-agent-api"
	pb "github.com/itsKontra/ddash/components/docker-agent-api/dockeragentapipb"
	jobpb "github.com/itsKontra/ddash/proto/ddash/job"
	"github.com/itsKontra/ddash/sdk/sdktest"
)

const jellyfin = "services:\n  jellyfin:\n    image: jellyfin/jellyfin:10.9\n    ports: [\"8096:8096\"]\n"

func setup(t *testing.T) (pb.DockerAgentApiServiceClient, *agentfake.Docker, string) {
	t.Helper()
	fake, err := agentfake.NewDocker(t.TempDir(), agentfake.DockerSeed{
		Stacks: []agentfake.Stack{{Dir: "media", Compose: jellyfin, Running: true}},
		Images: []agentfake.Image{{Ref: "jellyfin/jellyfin:10.9", ID: "sha256:a", Latest: "sha256:b"}},
	}, agentfake.Options{StateDir: t.TempDir(), Release: "dev"})
	if err != nil {
		t.Fatal(err)
	}
	u, stop, err := fake.Serve("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(stop)
	h := sdktest.New(t, dockeragentapi.Definition())
	h.MustStart(&pb.DockerAgentApiConfig{})
	return pb.NewDockerAgentApiServiceClient(h.Conn), fake, u
}

func TestAddPairAndRead(t *testing.T) {
	ctx := context.Background()
	c, fake, u := setup(t)
	added, err := c.AddAgent(ctx, &pb.AddAgentRequest{Name: "media", Url: u, Roots: []string{fake.Root}})
	if err != nil {
		t.Fatal(err)
	}
	setup := added.GetSetup()
	if setup.GetEnrolmentCode() == "" || added.GetAgent().GetState() != pb.AgentState_AGENT_STATE_ENROLLING {
		t.Fatalf("added = %v", added)
	}
	for _, want := range []string{"DDASH_ENROLMENT_CODE: " + setup.GetEnrolmentCode(), fake.Root + ":" + fake.Root, "/var/run/docker.sock", "root-equivalent"} {
		if !contains(setup.GetComposeFile(), want) {
			t.Errorf("the setup's Compose file lacks %q:\n%s", want, setup.GetComposeFile())
		}
	}
	if _, err := c.AddAgent(ctx, &pb.AddAgentRequest{Name: "media", Url: u, Roots: []string{"/opt"}}); status.Code(err) != codes.AlreadyExists {
		t.Fatalf("same name again: %v", err)
	}

	paired, err := c.PairAgent(ctx, &pb.PairAgentRequest{Name: "media"})
	if err != nil {
		t.Fatal(err)
	}
	if paired.GetAgent().GetState() != pb.AgentState_AGENT_STATE_PAIRED || paired.GetAgent().GetFingerprint() != fake.Fingerprint() {
		t.Fatalf("paired = %v", paired)
	}
	if s, _ := c.GetAgentSetup(ctx, &pb.GetAgentSetupRequest{Name: "media"}); s.GetSetup().GetEnrolmentCode() != "" {
		t.Fatal("a paired Agent's setup still shows its code")
	}

	projects, err := c.ListComposeProjects(ctx, &pb.ListComposeProjectsRequest{})
	if err != nil {
		t.Fatal(err)
	}
	h := projects.GetHosts()[0]
	if len(h.GetProjects()) != 1 || h.GetProjects()[0].GetName() != "media" || h.GetContainers()[0].GetService() != "jellyfin" {
		t.Fatalf("projects = %v", projects)
	}

	file := filepath.Join(fake.Root, "media/compose.yaml")
	got, err := c.GetComposeFile(ctx, &pb.GetComposeFileRequest{Host: "media", Path: file})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.WriteComposeFile(ctx, &pb.WriteComposeFileRequest{Host: "media", Path: file, Content: "services: {}\n", ExpectSha256: got.GetFile().GetSha256()}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("invalid write: %v", err)
	}
	if _, err := c.WriteComposeFile(ctx, &pb.WriteComposeFileRequest{Host: "media", Path: file, Content: jellyfin + "\n", ExpectSha256: "stale"}); status.Code(err) != codes.Aborted {
		t.Fatalf("stale write: %v", err)
	}

	up, err := c.UpdateProject(ctx, &pb.UpdateProjectRequest{Target: &pb.ComposeTarget{Host: "media", Project: "media", WorkingDir: filepath.Dir(file), File: file}})
	if err != nil {
		t.Fatal(err)
	}
	j := waitJob(t, c, up.GetJob().GetId())
	if j.GetState() != jobpb.State_STATE_DONE || j.GetResult()["jellyfin"] == "" {
		t.Fatalf("update job = %v", j)
	}
	// One job is read by itself, until it is collected.
	one, err := c.GetJob(ctx, &pb.GetJobRequest{Host: "media", Id: j.GetId()})
	if err != nil || one.GetJob().GetId() != j.GetId() || one.GetJob().GetState() != jobpb.State_STATE_DONE {
		t.Fatalf("GetJob = %v, %v", one, err)
	}
	if _, err := c.CollectJob(ctx, &pb.CollectJobRequest{Host: "media", Id: j.GetId()}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.GetJob(ctx, &pb.GetJobRequest{Host: "media", Id: j.GetId()}); status.Code(err) != codes.NotFound {
		t.Fatalf("GetJob of a collected job: %v, want NotFound", err)
	}

	// A failed job's collect fails with its error, for the Audit Log.
	fake.FailNext("restart", "Error response from daemon: port is already allocated")
	re, err := c.RestartProject(ctx, &pb.RestartProjectRequest{Target: &pb.ComposeTarget{Host: "media", Project: "media", WorkingDir: filepath.Dir(file), File: file}})
	if err != nil {
		t.Fatal(err)
	}
	waitJob(t, c, re.GetJob().GetId())
	if _, err := c.CollectJob(ctx, &pb.CollectJobRequest{Host: "media", Id: re.GetJob().GetId()}); status.Code(err) != codes.Aborted || !contains(status.Convert(err).Message(), "port is already allocated") {
		t.Fatalf("collecting a failed job: %v", err)
	}
}

func TestOutdatedAgentAllowsOnlyUpdate(t *testing.T) {
	ctx := context.Background()
	fake, err := agentfake.NewDocker(t.TempDir(), agentfake.DockerSeed{}, agentfake.Options{StateDir: t.TempDir(), Release: "v0.0.1"})
	if err != nil {
		t.Fatal(err)
	}
	u, stop, _ := fake.Serve("127.0.0.1:0")
	t.Cleanup(stop)
	h := sdktest.New(t, dockeragentapi.Definition())
	h.MustStart(&pb.DockerAgentApiConfig{})
	c := pb.NewDockerAgentApiServiceClient(h.Conn)
	if _, err := c.AddAgent(ctx, &pb.AddAgentRequest{Name: "old", Url: u, Roots: []string{fake.Root}}); err != nil {
		t.Fatal(err)
	}
	a, err := c.PairAgent(ctx, &pb.PairAgentRequest{Name: "old"})
	if err != nil {
		t.Fatal(err)
	}
	if a.GetAgent().GetState() != pb.AgentState_AGENT_STATE_OUTDATED {
		t.Fatalf("agent = %v", a)
	}
	if _, err := c.GetComposeFile(ctx, &pb.GetComposeFileRequest{Host: "old", Path: fake.Root + "/x/compose.yaml"}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("an outdated Agent's read: %v", err)
	}
}

func waitJob(t *testing.T, c pb.DockerAgentApiServiceClient, id string) *jobpb.Job {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		res, err := c.ListJobs(context.Background(), &pb.ListJobsRequest{})
		if err != nil {
			t.Fatal(err)
		}
		for _, h := range res.GetHosts() {
			for _, j := range h.GetJobs() {
				if j.GetId() == id && j.GetEnd() != nil {
					return j
				}
			}
		}
	}
	t.Fatalf("job %s didn't end", id)
	return nil
}

func contains(s, sub string) bool { return strings.Contains(s, sub) }
