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
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	containerpb "github.com/itsKontra/ddash/proto/ddash/container"
	notificationpb "github.com/itsKontra/ddash/proto/ddash/notification"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/itsKontra/ddash/agent/agentfake"
	"github.com/itsKontra/ddash/components/checks/checkspb"
	"github.com/itsKontra/ddash/components/compose"
	pb "github.com/itsKontra/ddash/components/compose/composepb"
	dockeragentapi "github.com/itsKontra/ddash/components/docker-agent-api"
	dapb "github.com/itsKontra/ddash/components/docker-agent-api/dockeragentapipb"
	"github.com/itsKontra/ddash/components/notify/notifypb"
	registryapi "github.com/itsKontra/ddash/components/registry-api"
	regpb "github.com/itsKontra/ddash/components/registry-api/registryapipb"
	"github.com/itsKontra/ddash/sdk"
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
	probed  []string
}

func (f *checksFake) ProbeNow(_ context.Context, req *checkspb.ProbeNowRequest, _ ...grpc.CallOption) (*checkspb.ProbeNowResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	w := req.GetScope().GetWorkload()
	f.probed = append(f.probed, w.GetHost()+"/"+w.GetId())
	return &checkspb.ProbeNowResponse{}, nil
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
	sent []*notificationpb.Notification
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
			{Dir: "homebrew", Compose: homebrew, Dockerfile: "FROM alpine:3.20\n", Running: true},
			{Dir: "idle", Compose: "services:\n  web:\n    image: nginx:1.27\n"},
		},
		Images: []agentfake.Image{
			{Ref: "jellyfin/jellyfin:10.9", ID: "sha256:j1", Latest: "sha256:j2"},
			{Ref: "nginx:1.27", ID: "sha256:n1"},
			{Ref: "redis:7.4", ID: "sha256:r1"},
		},
		Agent: true,
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
	// The real registry-api, reading the fake registry.
	rh := sdktest.New(t, registryapi.Definition())
	rh.MustStart(&regpb.RegistryApiConfig{DockerHubRegistry: &reg.URL})

	l := lab{docker: docker, registry: registry, checks: &checksFake{}, notify: &notifyFake{}}
	h := sdktest.New(t, compose.Definition(),
		sdktest.WithClient[dapb.DockerAgentApiServiceClient](agents),
		sdktest.WithClient[regpb.RegistryApiServiceClient](regpb.NewRegistryApiServiceClient(rh.Conn)),
		sdktest.WithClient[checkspb.ChecksServiceClient](l.checks),
		sdktest.WithClient[notifypb.NotifyServiceClient](l.notify),
	)
	h.MustStart(&pb.ComposeConfig{})
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
	if st := image(l.project(t, "homebrew"), "app"); st.GetState() != pb.ImageState_IMAGE_STATE_NOT_CHECKED || st.GetNotChecked() != pb.ImageStatus_NOT_CHECKED_LOCAL {
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

func TestAnImagePinnedByDigestIsNotCheckedAndSaysSo(t *testing.T) {
	st := compose.ImageStatusOf(&containerpb.Container{
		Image:       "ghcr.io/itskontra/ddash-docker-agent:v1.2.3@sha256:abc",
		RepoDigests: []string{"ghcr.io/itskontra/ddash-docker-agent@sha256:abc"},
		State:       containerpb.State_STATE_RUNNING,
	})
	if st.GetState() != pb.ImageState_IMAGE_STATE_NOT_CHECKED || st.GetNotChecked() != pb.ImageStatus_NOT_CHECKED_PINNED || st.GetCurrentDigest() != "sha256:abc" {
		t.Fatalf("pinned: %v", st)
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
	// Only the override keeps it from running: nothing else blocks it.
	if plan.GetPlan().GetOverrideNeeded() == "" || len(plan.GetPlan().GetBlocked()) != 0 || len(plan.GetPlan().GetProtected()) != 1 {
		t.Fatalf("plan = %v, want it to need the override for its one Protected container and nothing else", plan)
	}
	stream, err := l.c.RunAction(ctx, &pb.RunActionRequest{Action: a, Plan: plan.GetPlan()})
	if err == nil {
		_, err = stream.Recv()
	}
	if status.Code(err) != codes.FailedPrecondition || !strings.Contains(err.Error(), "Protected") {
		t.Fatalf("run without the override: %v, want FailedPrecondition naming the Protected container", err)
	}
	if st := l.docker.State("infra-proxy-1"); st != "running" {
		t.Fatalf("infra-proxy-1 is %s after the refused run, want it left running", st)
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

func TestSaveCreatesEnv(t *testing.T) {
	ctx := context.Background()
	l := setup(t)
	env := filepath.Join(l.docker.Root, "idle/.env")
	a := &pb.ComposeAction{Action: &pb.ComposeAction_Save{Save: &pb.SaveFile{Host: "apps", Project: "idle", Path: env, Content: "TZ=Europe/Berlin\n"}}}
	plan, err := l.c.PlanAction(ctx, &pb.PlanActionRequest{Action: a})
	if err != nil {
		t.Fatal(err)
	}
	p := plan.GetPlan()
	if len(p.GetBlocked()) > 0 || p.GetSteps()[0].GetTitle() != "Create "+env || !strings.Contains(p.GetDiff(), "+TZ=Europe/Berlin") {
		t.Fatalf("plan = %v", p)
	}
	r, err := l.run(t, a)
	if err != nil || r.GetError() != "" {
		t.Fatalf("save: %v %v", r, err)
	}
	if fi, err := os.Stat(env); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf(".env: %v %v", fi, err)
	}
	// There now: a create expecting none is stale.
	if plan, _ = l.c.PlanAction(ctx, &pb.PlanActionRequest{Action: a}); !strings.Contains(strings.Join(plan.GetPlan().GetBlocked(), " "), "changed on disk") {
		t.Fatalf("again: %v", plan.GetPlan().GetBlocked())
	}
}

func TestSaveDockerfileRebuilds(t *testing.T) {
	ctx := context.Background()
	l := setup(t)
	file := filepath.Join(l.docker.Root, "homebrew/Dockerfile")
	b, _ := os.ReadFile(file)
	before := l.project(t, "homebrew").GetServices()[0].GetContainers()[0].GetContainer().GetId()
	a := &pb.ComposeAction{Action: &pb.ComposeAction_Save{Save: &pb.SaveFile{Host: "apps", Project: "homebrew", Path: file, Content: "FROM alpine:3.21\n", ExpectSha256: sha(b), Recreate: true}}}
	plan, err := l.c.PlanAction(ctx, &pb.PlanActionRequest{Action: a})
	if err != nil {
		t.Fatal(err)
	}
	var titles []string
	for _, s := range plan.GetPlan().GetSteps() {
		titles = append(titles, s.GetTitle())
	}
	// Not validated as a Compose file; only its Compose services rebuilt.
	if p := plan.GetPlan(); len(p.GetBlocked()) > 0 || !slices.Contains(titles, "Write "+file) || titles[len(titles)-1] != "Rebuild and recreate app" {
		t.Fatalf("plan = %v", p)
	}
	r, err := l.run(t, a)
	if err != nil || r.GetError() != "" {
		t.Fatalf("save: %v %v", r, err)
	}
	if got, _ := os.ReadFile(file); string(got) != "FROM alpine:3.21\n" {
		t.Fatalf("Dockerfile = %q", got)
	}
	if !slices.Contains(l.docker.Calls(), "up homebrew -d --remove-orphans --build app") {
		t.Fatalf("calls = %v", l.docker.Calls())
	}
	if after := l.project(t, "homebrew").GetServices()[0].GetContainers()[0].GetContainer().GetId(); after == before {
		t.Fatal("app wasn't recreated from its new image")
	}

	// Only a Dockerfile its Compose file builds from.
	a.GetSave().Path = filepath.Join(l.docker.Root, "homebrew/main.go")
	if _, err := l.c.PlanAction(ctx, &pb.PlanActionRequest{Action: a}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("main.go: %v", err)
	}
}

func TestAddProjectWithEnv(t *testing.T) {
	l := setup(t)
	dir := filepath.Join(l.docker.Root, "paperless")
	a := &pb.ComposeAction{Action: &pb.ComposeAction_Add{Add: &pb.AddProject{Host: "apps", Name: "paperless", Directory: dir, Content: "services:\n  web:\n    image: redis:7.4\n", Env: "SECRET=x\n"}}}
	r, err := l.run(t, a)
	if err != nil || r.GetError() != "" {
		t.Fatalf("add: %v %v", r, err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, ".env")); string(b) != "SECRET=x\n" {
		t.Fatalf(".env = %q", b)
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

func TestARunThatBroughtContainersUpHasTheirChecksProbedAtOnce(t *testing.T) {
	l := setup(t)
	restart := &pb.ComposeAction{Action: &pb.ComposeAction_Restart{Restart: &pb.Restart{Host: "apps", Project: "media", Services: []string{"jellyfin"}}}}
	if _, err := l.run(t, restart); err != nil {
		t.Fatal(err)
	}
	if got, want := l.checks.probed, []string{"apps/media-jellyfin-1"}; !slices.Equal(got, want) {
		t.Errorf("after Restart jellyfin, probed %v, want %v", got, want)
	}

	// A failed run, or a Stop, probes nothing.
	l.checks.probed = nil
	l.docker.FailNext("restart", "Error response from daemon: no")
	if _, err := l.run(t, restart); status.Code(err) != codes.Aborted {
		t.Fatalf("failing Restart: %v", err)
	}
	if _, err := l.run(t, &pb.ComposeAction{Action: &pb.ComposeAction_Stop{Stop: &pb.Stop{Host: "apps", Project: "media"}}}); err != nil {
		t.Fatal(err)
	}
	if len(l.checks.probed) != 0 {
		t.Errorf("after a failed Restart and a Stop, probed %v", l.checks.probed)
	}

	if _, err := l.run(t, &pb.ComposeAction{Action: &pb.ComposeAction_Start{Start: &pb.Start{Host: "apps", Project: "media"}}}); err != nil {
		t.Fatal(err)
	}
	if got := l.checks.probed; len(got) != 2 || !slices.Contains(got, "apps/media-jellyfin-1") {
		t.Errorf("after Start media, probed %v, want both its containers", got)
	}
}

// start plans action and runs it in the background, returning the run's
// id once its first state arrived and a func waiting for its end.
func (l lab) start(t *testing.T, a *pb.ComposeAction) (string, func() (*pb.Run, error)) {
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
	first, err := stream.Recv()
	if err != nil {
		t.Fatal(err)
	}
	return first.GetRun().GetId(), func() (*pb.Run, error) {
		last := first.GetRun()
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
}

// asAlice is a call the host routed for the Operator alice.
func asAlice() context.Context {
	return metadata.AppendToOutgoingContext(context.Background(), sdk.ActorMetadata, "operator:alice")
}

func TestACancelledUpdateStopsItsPullAndSaysWhatItLeft(t *testing.T) {
	ctx := context.Background()
	l := setup(t)
	if _, err := l.c.CheckImages(ctx, &pb.CheckImagesRequest{}); err != nil {
		t.Fatal(err)
	}
	pulling := l.docker.HoldNext("pull")
	id, wait := l.start(t, &pb.ComposeAction{Action: &pb.ComposeAction_Update{Update: &pb.Update{Host: "apps", Project: "media"}}})
	<-pulling
	// The run reads its job once a second: cancel once it shows the job.
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(50 * time.Millisecond) {
		res, err := l.c.ListRuns(ctx, &pb.ListRunsRequest{})
		if err != nil {
			t.Fatal(err)
		}
		if r := res.GetRuns()[0]; r.GetJob().GetId() != "" && r.GetJob().GetEnd() == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the run never showed its job")
		}
	}
	res, err := l.c.CancelRun(asAlice(), &pb.CancelRunRequest{Id: id})
	if err != nil {
		t.Fatal(err)
	}
	if res.GetRun().GetCancelledBy() != "operator:alice" || res.GetRun().GetEnded() != nil {
		t.Errorf("right after the cancel: %v, want it cancelled by alice and still stopping", res.GetRun())
	}
	if _, err := l.c.CancelRun(ctx, &pb.CancelRunRequest{Id: id}); status.Code(err) != codes.FailedPrecondition {
		t.Errorf("cancelling it again: %v, want FailedPrecondition", err)
	}

	r, err := wait()
	if status.Code(err) != codes.Canceled {
		t.Errorf("RunAction ended %v, want Canceled", err)
	}
	if r.GetEnded() == nil || r.GetError() != "" || r.GetCancelledBy() != "operator:alice" {
		t.Fatalf("run = %v, want ended, cancelled and not failed", r)
	}
	want := []pb.StepState{pb.StepState_STEP_STATE_DONE, pb.StepState_STEP_STATE_CANCELLED, pb.StepState_STEP_STATE_PENDING, pb.StepState_STEP_STATE_PENDING}
	if !slices.Equal(r.GetStates(), want) {
		t.Errorf("states = %v, want the Maintenance done, the pull cancelled, the rest not run", r.GetStates())
	}
	for _, says := range []string{"Cancelled by alice", "“Pull images”", "images pulled until then stay", "no container was changed", "Nothing was rolled back", "Maintenance it started"} {
		if !strings.Contains(r.GetOutcome(), says) {
			t.Errorf("outcome %q doesn't say %q", r.GetOutcome(), says)
		}
	}
	if st := image(l.project(t, "media"), "jellyfin"); st.GetState() != pb.ImageState_IMAGE_STATE_STALE {
		t.Errorf("jellyfin after the cancelled update: %v, want still stale", st)
	}
	if _, err := l.c.CancelRun(ctx, &pb.CancelRunRequest{Id: id}); status.Code(err) != codes.FailedPrecondition {
		t.Errorf("cancelling an ended run: %v, want FailedPrecondition", err)
	}
	if _, err := l.c.CancelRun(ctx, &pb.CancelRunRequest{Id: "nope"}); status.Code(err) != codes.NotFound {
		t.Errorf("cancelling no run: %v, want NotFound", err)
	}
	// Another run on the Compose Project can start.
	if r, err := l.run(t, &pb.ComposeAction{Action: &pb.ComposeAction_Update{Update: &pb.Update{Host: "apps", Project: "media"}}}); err != nil || r.GetError() != "" {
		t.Fatalf("the update again: %v, %v", r, err)
	}
}

func TestACancelledRestartSaysItsComposeServicesMayBeHalfDone(t *testing.T) {
	l := setup(t)
	restarting := l.docker.HoldNext("restart")
	id, wait := l.start(t, &pb.ComposeAction{Action: &pb.ComposeAction_Restart{Restart: &pb.Restart{Host: "apps", Project: "media"}}})
	<-restarting
	// Cancelled at once: whether or not the run showed its job yet, the
	// job is stopped.
	if _, err := l.c.CancelRun(asAlice(), &pb.CancelRunRequest{Id: id}); err != nil {
		t.Fatal(err)
	}
	r, err := wait()
	if status.Code(err) != codes.Canceled || r.GetCancelledBy() == "" {
		t.Fatalf("run = %v, %v; want cancelled", r, err)
	}
	if last := r.GetStates()[len(r.GetStates())-1]; last != pb.StepState_STEP_STATE_CANCELLED {
		t.Errorf("states = %v, want the restart cancelled", r.GetStates())
	}
	if !strings.Contains(r.GetOutcome(), "stopped part-way") || !strings.Contains(r.GetOutcome(), "Run it again") {
		t.Errorf("outcome %q doesn't say its Compose services may be half-done", r.GetOutcome())
	}
	if titles := l.notify.titles(); len(titles) != 0 {
		t.Errorf("a run cancelled while watched notified %v", titles)
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

// The Docker Agent's own Compose Project is listed as the Agent, its
// file outside the roots unread, and no Compose Project Action runs on
// it, not even with the override its Protected label asks for.
func TestTheDockerAgentsOwnProjectIsTheAgent(t *testing.T) {
	ctx := context.Background()
	l := setup(t)

	own := l.project(t, agentfake.AgentProject)

	if !own.GetDockerAgent() || own.GetError() != "" || own.GetState() != pb.ProjectState_PROJECT_STATE_RUNNING {
		t.Fatalf("the Agent's own Project = %v, want it the Docker Agent, running, without an error", own)
	}
	if l.project(t, "media").GetDockerAgent() {
		t.Error("media is the Docker Agent too")
	}
	for _, a := range []*pb.ComposeAction{
		{Action: &pb.ComposeAction_Restart{Restart: &pb.Restart{Host: "apps", Project: agentfake.AgentProject}}, OverrideProtected: true},
		{Action: &pb.ComposeAction_Stop{Stop: &pb.Stop{Host: "apps", Project: agentfake.AgentProject}}, OverrideProtected: true},
		{Action: &pb.ComposeAction_Update{Update: &pb.Update{Host: "apps", Project: agentfake.AgentProject}}, OverrideProtected: true},
	} {
		plan, err := l.c.PlanAction(ctx, &pb.PlanActionRequest{Action: a})
		if err != nil {
			t.Fatal(err)
		}
		if b := plan.GetPlan().GetBlocked(); len(b) != 1 || !strings.Contains(b[0], "Update Agent") {
			t.Errorf("%v: blocked %q, want it refused for Update Agent", a.GetAction(), b)
		}
		stream, err := l.c.RunAction(ctx, &pb.RunActionRequest{Action: a, Plan: plan.GetPlan()})
		if err == nil {
			_, err = stream.Recv()
		}
		if status.Code(err) != codes.FailedPrecondition {
			t.Errorf("%v run: %v, want FailedPrecondition", a.GetAction(), err)
		}
	}
	if st := l.docker.State(agentfake.AgentContainer); st != "running" {
		t.Errorf("the Agent is %s, want it left running", st)
	}
}
