package compose_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/itsKontra/ddash/agent/agentfake"
	"github.com/itsKontra/ddash/components/checks/checkspb"
	"github.com/itsKontra/ddash/components/compose"
	pb "github.com/itsKontra/ddash/components/compose/composepb"
	dockeragentapi "github.com/itsKontra/ddash/components/docker-agent-api"
	dapb "github.com/itsKontra/ddash/components/docker-agent-api/dockeragentapipb"
	"github.com/itsKontra/ddash/components/notify/notifypb"
	"github.com/itsKontra/ddash/sdk/sdktest"
)

const (
	media    = "services:\n  jellyfin:\n    image: jellyfin/jellyfin:10.9\n    ports: [\"8096:8096\"]\n  nginx:\n    image: nginx:1.27\n"
	infra    = "services:\n  proxy:\n    image: nginx:1.27\n    labels: [ddash.protected=true]\n"
	homebrew = "services:\n  app:\n    build: .\n"
)

type checksFake struct {
	checkspb.ChecksServiceClient
	mu      sync.Mutex
	started []string
	ended   []string
}

func (f *checksFake) StartMaintenance(_ context.Context, req *checkspb.StartMaintenanceRequest, _ ...grpc.CallOption) (*checkspb.StartMaintenanceResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.started = append(f.started, req.GetScope().GetWorkload().GetId())
	return &checkspb.StartMaintenanceResponse{}, nil
}

func (f *checksFake) EndMaintenance(_ context.Context, req *checkspb.EndMaintenanceRequest, _ ...grpc.CallOption) (*checkspb.EndMaintenanceResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ended = append(f.ended, req.GetScope().GetWorkload().GetId())
	return &checkspb.EndMaintenanceResponse{}, nil
}

type notifyFake struct {
	notifypb.NotifyServiceClient
	mu   sync.Mutex
	sent []*notifypb.Notification
}

func (f *notifyFake) Send(_ context.Context, req *notifypb.SendRequest, _ ...grpc.CallOption) (*notifypb.SendResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, req.GetNotification())
	return &notifypb.SendResponse{}, nil
}

func (f *notifyFake) titles() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, n := range f.sent {
		out = append(out, n.GetTitle())
	}
	return out
}

type lab struct {
	c        pb.ComposeServiceClient
	docker   *agentfake.Docker
	registry *agentfake.Registry
	checks   *checksFake
	notify   *notifyFake
}

func setup(t *testing.T) lab {
	t.Helper()
	ctx := context.Background()
	docker, err := agentfake.NewDocker(t.TempDir(), agentfake.DockerSeed{
		Stacks: []agentfake.Stack{
			{Dir: "media", Compose: media, Running: true},
			{Dir: "infra", Compose: infra, Running: true},
			{Dir: "homebrew", Compose: homebrew, Running: true},
			{Dir: "idle", Compose: "services:\n  web:\n    image: nginx:1.27\n"},
		},
		Images: []agentfake.Image{
			{Ref: "jellyfin/jellyfin:10.9", ID: "sha256:j1", Latest: "sha256:j2"},
			{Ref: "nginx:1.27", ID: "sha256:n1"},
			{Ref: "redis:7.4", ID: "sha256:r1"},
		},
	}, agentfake.Options{StateDir: t.TempDir(), Release: "dev"})
	if err != nil {
		t.Fatal(err)
	}
	u, stop, err := docker.Serve("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(stop)
	registry := agentfake.NewRegistry(docker.Engine)
	reg := httptest.NewServer(registry)
	t.Cleanup(reg.Close)

	ah := sdktest.New(t, dockeragentapi.Definition())
	ah.MustStart(&dapb.DockerAgentApiConfig{})
	agents := dapb.NewDockerAgentApiServiceClient(ah.Conn)
	if _, err := agents.AddAgent(ctx, &dapb.AddAgentRequest{Name: "apps", Url: u, Roots: []string{docker.Root}}); err != nil {
		t.Fatal(err)
	}
	if _, err := agents.PairAgent(ctx, &dapb.PairAgentRequest{Name: "apps"}); err != nil {
		t.Fatal(err)
	}
	l := lab{docker: docker, registry: registry, checks: &checksFake{}, notify: &notifyFake{}}
	h := sdktest.New(t, compose.Definition(),
		sdktest.WithClient[dapb.DockerAgentApiServiceClient](agents),
		sdktest.WithClient[checkspb.ChecksServiceClient](l.checks),
		sdktest.WithClient[notifypb.NotifyServiceClient](l.notify),
	)
	h.MustStart(&pb.ComposeConfig{DockerHubRegistry: &reg.URL})
	l.c = pb.NewComposeServiceClient(h.Conn)
	return l
}

func (l lab) project(t *testing.T, name string) *pb.Project {
	t.Helper()
	res, err := l.c.ListProjects(context.Background(), &pb.ListProjectsRequest{})
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range res.GetHosts()[0].GetProjects() {
		if p.GetName() == name {
			return p
		}
	}
	t.Fatalf("no project %s", name)
	return nil
}

func image(p *pb.Project, service string) *pb.ImageStatus {
	for _, s := range p.GetServices() {
		if s.GetName() == service && len(s.GetContainers()) > 0 {
			return s.GetContainers()[0].GetImageStatus()
		}
	}
	return nil
}

// run plans action, runs it and returns its last state.
func (l lab) run(t *testing.T, a *pb.ComposeAction) (*pb.Run, error) {
	t.Helper()
	ctx := context.Background()
	plan, err := l.c.PlanAction(ctx, &pb.PlanActionRequest{Action: a})
	if err != nil {
		t.Fatal(err)
	}
	stream, err := l.c.RunAction(ctx, &pb.RunActionRequest{Action: a, Plan: plan.GetPlan()})
	if err != nil {
		t.Fatal(err)
	}
	var last *pb.Run
	for {
		msg, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			return last, nil
		}
		if err != nil {
			return last, err
		}
		last = msg.GetRun()
	}
}

func TestImageChecks(t *testing.T) {
	ctx := context.Background()
	l := setup(t)
	if st := image(l.project(t, "media"), "jellyfin"); st.GetState() != pb.ImageState_IMAGE_STATE_UNKNOWN {
		t.Fatalf("before a check: %v", st)
	}
	if _, err := l.c.CheckImages(ctx, &pb.CheckImagesRequest{}); err != nil {
		t.Fatal(err)
	}
	p := l.project(t, "media")
	if st := image(p, "jellyfin"); st.GetState() != pb.ImageState_IMAGE_STATE_STALE || st.GetLatestDigest() == st.GetCurrentDigest() {
		t.Fatalf("jellyfin: %v", st)
	}
	if st := image(p, "nginx"); st.GetState() != pb.ImageState_IMAGE_STATE_UP_TO_DATE {
		t.Fatalf("nginx: %v", st)
	}
	if !p.GetUpdateAvailable() {
		t.Fatal("media doesn't say update available")
	}
	if st := image(l.project(t, "homebrew"), "app"); st.GetState() != pb.ImageState_IMAGE_STATE_NOT_CHECKED {
		t.Fatalf("a local image: %v", st)
	}
	if got := l.notify.titles(); len(got) != 1 || got[0] != "Image update for media-jellyfin-1" {
		t.Fatalf("notified %v", got)
	}
	// The same digest isn't notified twice; one HEAD per distinct image.
	before := l.registry.Requests()
	if _, err := l.c.CheckImages(ctx, &pb.CheckImagesRequest{}); err != nil {
		t.Fatal(err)
	}
	if got := l.notify.titles(); len(got) != 1 {
		t.Fatalf("notified again: %v", got)
	}
	// jellyfin and nginx: their bearer tokens are kept, so one HEAD each.
	if n := l.registry.Requests() - before; n != 2 {
		t.Fatalf("second check made %d requests", n)
	}

	l.registry.RateLimit(true)
	res, err := l.c.CheckImages(ctx, &pb.CheckImagesRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.GetChecks().GetProblems()) != 1 || !strings.Contains(res.GetChecks().GetProblems()[0].GetMessage(), "rate limited") {
		t.Fatalf("problems = %v", res.GetChecks().GetProblems())
	}
	if st := image(l.project(t, "media"), "jellyfin"); st.GetState() != pb.ImageState_IMAGE_STATE_ERROR {
		t.Fatalf("rate limited: %v", st)
	}
}

func TestUpdate(t *testing.T) {
	ctx := context.Background()
	l := setup(t)
	if _, err := l.c.CheckImages(ctx, &pb.CheckImagesRequest{}); err != nil {
		t.Fatal(err)
	}
	a := &pb.ComposeAction{Action: &pb.ComposeAction_Update{Update: &pb.Update{Host: "apps", Project: "media"}}}
	plan, err := l.c.PlanAction(ctx, &pb.PlanActionRequest{Action: a})
	if err != nil {
		t.Fatal(err)
	}
	var pull *pb.PlanStep
	for _, s := range plan.GetPlan().GetSteps() {
		if s.GetTitle() == "Pull images" {
			pull = s
		}
	}
	if pull == nil || plan.GetPlan().GetUpToDate() {
		t.Fatalf("plan = %v", plan)
	}
	var jelly *pb.PlanValue
	for _, v := range pull.GetValues() {
		if strings.HasPrefix(v.GetLabel(), "jellyfin") {
			jelly = v
		}
	}
	if jelly == nil || jelly.GetBefore() == jelly.GetAfter() {
		t.Fatalf("the Plan doesn't list jellyfin's current and new digest: %v", pull)
	}

	r, err := l.run(t, a)
	if err != nil {
		t.Fatal(err)
	}
	if r.GetEnded() == nil || r.GetError() != "" || !strings.Contains(r.GetOutcome(), "jellyfin") {
		t.Fatalf("run = %v", r)
	}
	if len(l.checks.started) != 1 || l.checks.started[0] != "media-jellyfin-1" {
		t.Fatalf("Maintenance for %v", l.checks.started)
	}
	if st := image(l.project(t, "media"), "jellyfin"); st.GetState() != pb.ImageState_IMAGE_STATE_UP_TO_DATE {
		t.Fatalf("after the update: %v", st)
	}

	// Again: nothing changed, nothing recreated.
	r, err = l.run(t, a)
	if err != nil {
		t.Fatal(err)
	}
	if r.GetOutcome() != "already up to date" || r.GetStates()[len(r.GetStates())-1] != pb.StepState_STEP_STATE_SKIPPED {
		t.Fatalf("second update = %v", r)
	}
}

func TestProtected(t *testing.T) {
	ctx := context.Background()
	l := setup(t)
	a := &pb.ComposeAction{Action: &pb.ComposeAction_Stop{Stop: &pb.Stop{Host: "apps", Project: "infra"}}}
	plan, err := l.c.PlanAction(ctx, &pb.PlanActionRequest{Action: a})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.GetPlan().GetBlocked()) == 0 || len(plan.GetPlan().GetProtected()) != 1 {
		t.Fatalf("plan = %v", plan)
	}
	if !l.project(t, "infra").GetProtected() {
		t.Fatal("infra isn't Protected")
	}
	a.OverrideProtected = true
	r, err := l.run(t, a)
	if err != nil || r.GetError() != "" {
		t.Fatalf("with the override: %v %v", r, err)
	}
	if st := l.docker.State("infra-proxy-1"); st != "exited" {
		t.Fatalf("infra-proxy-1 is %s", st)
	}
}

func TestSave(t *testing.T) {
	ctx := context.Background()
	l := setup(t)
	file := filepath.Join(l.docker.Root, "media/compose.yaml")
	edited := strings.Replace(media, "10.9", "10.10", 1)
	a := &pb.ComposeAction{Action: &pb.ComposeAction_Save{Save: &pb.SaveFile{Host: "apps", Project: "media", Path: file, Content: edited, ExpectSha256: "stale"}}}
	plan, err := l.c.PlanAction(ctx, &pb.PlanActionRequest{Action: a})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(plan.GetPlan().GetDiff(), "-    image: jellyfin/jellyfin:10.9\n+    image: jellyfin/jellyfin:10.10") {
		t.Fatalf("diff = %q", plan.GetPlan().GetDiff())
	}
	if len(plan.GetPlan().GetBlocked()) == 0 {
		t.Fatal("a stale hash didn't block")
	}
	b, _ := os.ReadFile(file)
	a.GetSave().ExpectSha256 = sha(b)
	a.GetSave().Content = "services:\n  broken: {}\n"
	if plan, _ = l.c.PlanAction(ctx, &pb.PlanActionRequest{Action: a}); !strings.Contains(strings.Join(plan.GetPlan().GetBlocked(), " "), "docker compose config refuses it") {
		t.Fatalf("invalid content: %v", plan.GetPlan().GetBlocked())
	}
	a.GetSave().Content = edited
	r, err := l.run(t, a)
	if err != nil || r.GetError() != "" {
		t.Fatalf("save: %v %v", r, err)
	}
	if got, _ := os.ReadFile(file); string(got) != edited {
		t.Fatalf("file = %s", got)
	}
	if bak, _ := os.ReadFile(file + ".bak"); string(bak) != media {
		t.Fatalf("backup = %s", bak)
	}
	// The confirmed Plan no longer holds: refused with the new one.
	plan, _ = l.c.PlanAction(ctx, &pb.PlanActionRequest{Action: &pb.ComposeAction{Action: &pb.ComposeAction_Restart{Restart: &pb.Restart{Host: "apps", Project: "media"}}}})
	l.docker.FailNext("never", "")
	stream, _ := l.c.RunAction(ctx, &pb.RunActionRequest{Action: &pb.ComposeAction{Action: &pb.ComposeAction_Restart{Restart: &pb.Restart{Host: "apps", Project: "media", Services: []string{"nginx"}}}}, Plan: plan.GetPlan()})
	if _, err := stream.Recv(); status.Code(err) != codes.Aborted {
		t.Fatalf("a changed Plan: %v", err)
	}
}

func TestFailedStepStops(t *testing.T) {
	l := setup(t)
	l.docker.FailNext("restart", "Error response from daemon: driver failed programming external connectivity")
	r, err := l.run(t, &pb.ComposeAction{Action: &pb.ComposeAction_Restart{Restart: &pb.Restart{Host: "apps", Project: "media"}}})
	if status.Code(err) != codes.Aborted || !strings.Contains(r.GetError(), "driver failed") {
		t.Fatalf("run = %v, %v", r, err)
	}
	last := r.GetStates()[len(r.GetStates())-1]
	if last != pb.StepState_STEP_STATE_FAILED {
		t.Fatalf("states = %v", r.GetStates())
	}
	if titles := l.notify.titles(); len(titles) != 1 || titles[0] != "Restart media failed" {
		t.Fatalf("notified %v", titles)
	}
}

func sha(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

func TestAnImageNeverCheckedIsCheckedSoon(t *testing.T) {
	ctx := context.Background()
	l := setup(t)
	if _, err := l.c.CheckImages(ctx, &pb.CheckImagesRequest{}); err != nil {
		t.Fatal(err)
	}
	// A Compose Project started since: its image was never checked.
	add := &pb.ComposeAction{Action: &pb.ComposeAction_Add{Add: &pb.AddProject{Host: "apps", Name: "cache", Directory: filepath.Join(l.docker.Root, "cache"), Content: "services:\n  redis:\n    image: redis:7.4\n", Start: true}}}
	if r, err := l.run(t, add); err != nil || r.GetError() != "" {
		t.Fatalf("add: %v %v", r, err)
	}
	if st := image(l.project(t, "cache"), "redis"); st.GetState() != pb.ImageState_IMAGE_STATE_UNKNOWN {
		t.Fatalf("before: %v", st)
	}
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
		if st := image(l.project(t, "cache"), "redis"); st.GetState() == pb.ImageState_IMAGE_STATE_UP_TO_DATE {
			return
		}
	}
	t.Fatal("the new image wasn't checked")
}
