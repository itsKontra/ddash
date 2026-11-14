package apps_test

import (
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	"github.com/itsKontra/ddash/agent/agentfake"
	"github.com/itsKontra/ddash/components/apps/appspb"
	dapb "github.com/itsKontra/ddash/components/docker-agent-api/dockeragentapipb"
	proxyhostpb "github.com/itsKontra/ddash/proto/ddash/proxyhost"
	workloadpb "github.com/itsKontra/ddash/proto/ddash/workload"
)

// Add App from a new Compose Project runs against the real
// docker-agent-api paired with agentfake's Docker Agent, serving on
// 127.0.0.1 (the lab's Reverse Proxy's address too, which proxied Apps
// don't mind), with a Compose root holding the Compose Project media
// (jellyfin, publishing 8096).

const jellyfin = "services:\n  jellyfin:\n    image: jellyfin/jellyfin:10.9\n    ports: [\"8096:8096\"]\n"

// paperless is a new Compose Project's file: its App's service publishes
// 8010; its database nothing.
const paperless = `services:
  paperless:
    image: ghcr.io/paperless-ngx/paperless-ngx:2.14.7
    restart: unless-stopped
    ports:
      - "127.0.0.1:9000:9000"
      - "8010:8000"
  db:
    image: postgres:16.6
    restart: unless-stopped
`

// withAgent pairs a fake Docker Agent named media, each Compose step
// taking delay, and returns it.
func (l *lab) withAgent(delay time.Duration) *agentfake.Docker {
	l.t.Helper()
	fake, err := agentfake.NewDocker(l.t.TempDir(), agentfake.DockerSeed{
		Stacks: []agentfake.Stack{{Dir: "media", Compose: jellyfin, Running: true}},
		Images: []agentfake.Image{{Ref: "jellyfin/jellyfin:10.9", ID: "sha256:a"}},
	}, agentfake.Options{StateDir: l.t.TempDir(), Release: "dev", StepDelay: delay})
	if err != nil {
		l.t.Fatal(err)
	}
	u, stop, err := fake.Serve("127.0.0.1:0")
	if err != nil {
		l.t.Fatal(err)
	}
	l.t.Cleanup(stop)
	l.h.Install("docker")
	l.dockerCall("AddAgent", &dapb.AddAgentRequest{Name: "media", Url: u, Roots: []string{fake.Root}}, &dapb.AddAgentResponse{})
	paired := &dapb.PairAgentResponse{}
	l.dockerCall("PairAgent", &dapb.PairAgentRequest{Name: "media"}, paired)
	if paired.GetAgent().GetState() != dapb.AgentState_AGENT_STATE_PAIRED {
		l.t.Fatalf("the Agent didn't pair: %v", paired)
	}
	return fake
}

// dockerCall calls docker-agent-api's method as the Docker Plugin.
func (l *lab) dockerCall(method string, req, out proto.Message) {
	l.t.Helper()
	body, err := protojson.Marshal(req)
	if err != nil {
		l.t.Fatal(err)
	}
	res := l.h.Call("docker", "docker-agent-api", method, string(body))
	if res.Code != http.StatusOK {
		l.t.Fatalf("%s: %d %s", method, res.Code, res.Body)
	}
	if err := protojson.Unmarshal(res.Body.Bytes(), out); err != nil {
		l.t.Fatal(err)
	}
}

// addPaperless is Add App of Paperless from a new Compose Project on
// media, proxied, its upstream left to the Compose service.
func addPaperless(p *appspb.NewComposeProject) *appspb.AppAction {
	return addApp(&appspb.AddApp{
		Url: "https://paperless.home.example", Name: "Paperless", Group: "Documents", Reach: appspb.Reach_REACH_PROXIED,
		ProxyHost: &proxyhostpb.ProxyHostFields{CertificateId: 3, BlockExploits: true, ForceSsl: true},
		Compose:   p,
	})
}

func newPaperless() *appspb.NewComposeProject {
	return &appspb.NewComposeProject{Host: "media", Name: "paperless", Content: paperless}
}

// The Plan writes the Compose file and starts it before the Proxy Host
// and DNS steps; the upstream is the Docker host's address and the
// Compose service's first published port; the new container becomes the
// App's Workload.
func TestAddAnAppFromANewComposeProject(t *testing.T) {
	l := newLab(t, labOptions{})
	fake := l.withAgent(0)
	action := addPaperless(newPaperless())

	p := l.plan(action)
	if len(p.GetBlocked()) != 0 {
		t.Fatalf("blocked: %q", p.GetBlocked())
	}
	dir := filepath.Join(fake.Root, "paperless")
	want := []string{
		"SYSTEM_DOCKER: Write the Compose file of paperless on media",
		"SYSTEM_DOCKER: Start paperless on media",
		"SYSTEM_DOCKER: Wait until paperless runs",
		"SYSTEM_NGINX_PROXY_MANAGER: Create Proxy Host paperless.home.example",
		"SYSTEM_PFSENSE: Add alias paperless.home.example to nginx.home.example",
		"SYSTEM_DDASH: Create the App Paperless",
	}
	if got := titles(p); !slices.Equal(got, want) {
		t.Errorf("steps = %q, want %q", got, want)
	}
	if d := diff([]*appspb.PlanValue{{Label: "Compose file", After: dir + "/compose.yaml"}, {Label: "Compose services", After: "paperless, db"}}, p.GetSteps()[0].GetValues()); d != "" {
		t.Errorf("the write's values (-want +got):\n%s", d)
	}
	if d := diff([]*appspb.PlanValue{{Label: "Container", After: "paperless-paperless-1"}, {Label: "Published at", After: "127.0.0.1:9000"}}, p.GetSteps()[2].GetValues()); d != "" {
		t.Errorf("the wait's values (-want +got):\n%s", d)
	}
	if up := p.GetSteps()[3].GetValues()[1]; up.GetAfter() != "http://127.0.0.1:9000" {
		t.Errorf("upstream = %v, want the Docker host's address and the first published port", up)
	}
	wantServices := []*appspb.ComposeService{{Name: "paperless", Ports: []uint32{9000, 8010}}, {Name: "db"}}
	if d := diff(wantServices, p.GetComposeServices()); d != "" {
		t.Errorf("services (-want +got):\n%s", d)
	}

	// The Operator picks the other port.
	pick := newPaperless()
	pick.Port = 8010
	action = addPaperless(pick)
	p = l.plan(action)
	if up := p.GetSteps()[3].GetValues()[1]; up.GetAfter() != "http://127.0.0.1:8010" {
		t.Errorf("upstream = %v, want port 8010", up)
	}

	msgs, cerr := l.runStream(action, p)
	if cerr != nil {
		t.Fatalf("RunAction: %v", cerr)
	}
	resp := msgs[len(msgs)-1]
	if resp.GetDone() != 6 {
		t.Fatalf("RunAction = %v, want 6 steps done", resp)
	}
	// Each Docker step streamed running, then done, in order.
	if first := msgs[0].GetSteps(); first[0] != appspb.StepState_STEP_STATE_RUNNING || first[1] != appspb.StepState_STEP_STATE_PENDING {
		t.Errorf("first message = %v, want the write running", first)
	}

	calls := fake.Calls()
	if !slices.ContainsFunc(calls, func(c string) bool { return strings.HasPrefix(c, "up paperless") }) {
		t.Errorf("compose calls = %q, want an up of paperless", calls)
	}
	if fake.State("paperless-paperless-1") != "running" {
		t.Error("paperless isn't running on the host")
	}
	a := l.app(resp.GetAppId())
	w := a.GetWorkload()
	if w.GetRef().GetKind() != workloadpb.Kind_KIND_DOCKER || w.GetRef().GetHost() != "media" || w.GetName() != "paperless-paperless-1" || w.GetOverride() {
		t.Errorf("Workload = %v, want the new container, matched", w)
	}
	if slices.Contains(kinds(a), appspb.WarningKind_WARNING_KIND_NO_WORKLOAD) {
		t.Errorf("Warnings = %v", kinds(a))
	}
	// The job was collected: it's no longer on the Agent.
	jobs := &dapb.ListJobsResponse{}
	l.dockerCall("ListJobs", &dapb.ListJobsRequest{}, jobs)
	for _, h := range jobs.GetHosts() {
		if len(h.GetJobs()) != 0 {
			t.Errorf("jobs left on %s: %v", h.GetHost(), h.GetJobs())
		}
	}
}

// What can't be added is blocked in the Plan, before anything runs.
func TestANewComposeProjectThatCantBeAddedIsBlocked(t *testing.T) {
	l := newLab(t, labOptions{})
	fake := l.withAgent(0)
	for _, c := range []struct {
		name string
		edit func(*appspb.NewComposeProject)
		why  string
	}{
		{"no host", func(p *appspb.NewComposeProject) { p.Host = "" }, "Pick the Docker host"},
		{"unknown host", func(p *appspb.NewComposeProject) { p.Host = "nas" }, "nas has no paired Docker Agent."},
		{"bad name", func(p *appspb.NewComposeProject) { p.Name = "Paper Less" }, "lower-case letters"},
		{"taken name", func(p *appspb.NewComposeProject) { p.Name = "media" }, "media already has a Compose Project media."},
		{"outside the roots", func(p *appspb.NewComposeProject) { p.Directory = "/etc/paperless" }, "under one of media's Compose roots"},
		{"an existing project's directory", func(p *appspb.NewComposeProject) { p.Directory = filepath.Join(fake.Root, "media") }, "is already the Compose Project media's directory."},
		{"bad yaml", func(p *appspb.NewComposeProject) { p.Content = "services: [" }, "The Compose file doesn't parse"},
		{"no services", func(p *appspb.NewComposeProject) { p.Content = "name: x\n" }, "defines no services"},
		{"unknown service", func(p *appspb.NewComposeProject) { p.Service = "web" }, "has no service web"},
		{"unpublished port", func(p *appspb.NewComposeProject) { p.Port = 8000 }, "doesn't publish port 8000"},
		{"nothing published", func(p *appspb.NewComposeProject) { p.Service = "db" }, "publishes no port for the Proxy Host"},
		{"a used port", func(p *appspb.NewComposeProject) { p.Content = strings.ReplaceAll(paperless, "9000:9000", "8096:8096") }, "Port 8096 is already published on media, by media-jellyfin-1."},
	} {
		t.Run(c.name, func(t *testing.T) {
			p := newPaperless()
			c.edit(p)
			plan := l.plan(addPaperless(p))
			if !slices.ContainsFunc(plan.GetBlocked(), func(b string) bool { return strings.Contains(b, c.why) }) {
				t.Errorf("blocked = %q, want %q", plan.GetBlocked(), c.why)
			}
		})
	}
	if calls := fake.Calls(); slices.ContainsFunc(calls, func(c string) bool { return strings.HasPrefix(c, "up ") }) {
		t.Errorf("compose calls = %q, want no up", calls)
	}
}

// A failed step after the Compose steps stops there, nothing rolled back:
// the Compose Project runs, the Proxy Host exists, and the App shows Not
// registered, as any half-done Add App.
func TestAFailureAfterTheComposeStepsLeavesWarnings(t *testing.T) {
	l := newLab(t, labOptions{})
	fake := l.withAgent(0)
	l.fw.FailDNSWrite(1)
	action := addPaperless(newPaperless())

	_, err := l.tryRun(action, l.plan(action))
	if err == nil {
		t.Fatal("RunAction succeeded, want it stopped at pfSense")
	}
	var resp appspb.RunActionResponse
	if !err.detail(&resp) || resp.GetDone() != 4 {
		t.Fatalf("RunAction = %v (%v), want 4 steps done", err, &resp)
	}
	if !strings.HasPrefix(err.message, "Stopped at step 5 of 6, Add alias paperless.home.example to nginx.home.example: ") ||
		!strings.HasSuffix(err.message, "Done: Write the Compose file of paperless on media; Start paperless on media; Wait until paperless runs; Create Proxy Host paperless.home.example. Nothing was rolled back.") {
		t.Errorf("message = %q", err.message)
	}
	if fake.State("paperless-paperless-1") != "running" {
		t.Error("the Compose Project was rolled back")
	}
	a := l.byURL("https://paperless.home.example")
	if !slices.Contains(kinds(a), appspb.WarningKind_WARNING_KIND_NOT_REGISTERED) {
		t.Errorf("Warnings = %v, want Not registered", kinds(a))
	}
	if a.GetWorkload().GetName() != "paperless-paperless-1" {
		t.Errorf("Workload = %v, want the new container", a.GetWorkload())
	}
}

// A Compose Project whose up fails stops at its start, saying why; the
// Proxy Host isn't created.
func TestAFailedUpStopsTheAdd(t *testing.T) {
	l := newLab(t, labOptions{})
	fake := l.withAgent(0)
	fake.FailNext("up", "Error response from daemon: pull access denied for paperless")
	npmLog := len(l.npm.Log())
	action := addPaperless(newPaperless())

	_, err := l.tryRun(action, l.plan(action))
	if err == nil || !strings.HasPrefix(err.message, "Stopped at step 2 of 6, Start paperless on media: ") || !strings.Contains(err.message, "pull access denied") {
		t.Fatalf("RunAction = %v, want it stopped at the start with Compose's error", err)
	}
	if w := writes(l.npm.Log(), npmLog); len(w) != 0 {
		t.Errorf("Nginx Proxy Manager writes = %q, want none", w)
	}
}

// A Compose Project still starting when the Action's time runs out stops
// the Action there with how to go on; the job goes on on the host.
func TestAComposeProjectStillStartingStopsTheAddInTime(t *testing.T) {
	l := newLab(t, labOptions{composeWait: 300 * time.Millisecond})
	fake := l.withAgent(2 * time.Second)
	npmLog := len(l.npm.Log())
	action := addPaperless(newPaperless())

	start := time.Now()
	_, err := l.tryRun(action, l.plan(action))
	if took := time.Since(start); took > 2*time.Second {
		t.Errorf("RunAction took %v, want it to stop at its wait", took)
	}
	if err == nil || err.code != "deadline_exceeded" || !strings.HasPrefix(err.message, "Stopped at step 2 of 6, Start paperless on media: still starting on media") ||
		!strings.Contains(err.message, "add the App with Reverse proxy to its upstream 127.0.0.1:9000") {
		t.Fatalf("RunAction = %v, want it stopped, still starting", err)
	}
	if w := writes(l.npm.Log(), npmLog); len(w) != 0 {
		t.Errorf("Nginx Proxy Manager writes = %q, want none", w)
	}
	// The job ends on the host all the same.
	for deadline := time.Now().Add(5 * time.Second); fake.State("paperless-paperless-1") != "running"; time.Sleep(50 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the job didn't go on on the host")
		}
	}
}

// ListDockerHosts lists the paired Agents' hosts to add on.
func TestListDockerHosts(t *testing.T) {
	l := newLab(t, labOptions{})
	fake := l.withAgent(0)
	resp := &appspb.ListDockerHostsResponse{}
	l.call("ListDockerHosts", &appspb.ListDockerHostsRequest{}, resp)
	want := []*appspb.DockerHost{{Name: "media", Address: "127.0.0.1", Roots: []string{fake.Root}, Projects: []string{"media"}, UsedPorts: []uint32{8096}}}
	if d := diff(want, resp.GetHosts()); d != "" {
		t.Errorf("hosts (-want +got):\n%s", d)
	}
}
