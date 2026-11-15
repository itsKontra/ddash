// Package compose is the compose Component, the logic behind the Docker
// Plugin's Compose Projects. It reads every Docker Agent's Compose
// Projects through docker-agent-api, checks every running container's
// image against its registry (a manifest HEAD per distinct image, every 6
// hours and on Refresh), notifies once per newly stale image, and runs
// Compose Project Actions through a Plan, as runs that outlast the
// Action's deadline (the Agent's jobs).
package compose

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"

	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/itsKontra/ddash/agent"
	"github.com/itsKontra/ddash/components/checks/checkspb"
	pb "github.com/itsKontra/ddash/components/compose/composepb"
	dapb "github.com/itsKontra/ddash/components/docker-agent-api/dockeragentapipb"
	"github.com/itsKontra/ddash/components/notify/notifypb"
	regpb "github.com/itsKontra/ddash/components/registry-api/registryapipb"
	"github.com/itsKontra/ddash/sdk"
)

//go:embed component.yaml
var manifest []byte

//go:embed migrations/*.sql
var migrations embed.FS

// Definition is compose, ready for sdk.Serve.
func Definition() sdk.Definition {
	return sdk.Define(sdk.Spec[*pb.ComposeConfig]{
		Manifest:   manifest,
		Service:    &pb.ComposeService_ServiceDesc,
		Migrations: migrations,
		New:        func() sdk.Component[*pb.ComposeConfig] { return &component{} },
	})
}

type component struct {
	pb.UnimplementedComposeServiceServer
	agents dapb.DockerAgentApiServiceClient
	checks checkspb.ChecksServiceClient
	notify notifypb.NotifyServiceClient
	db     *sql.DB
	log    *slog.Logger
	public string
	life   context.Context
	// registry reads image digests: the only way compose asks a registry.
	registry regpb.RegistryApiServiceClient
	// kick asks the check loop for a check now.
	kick chan struct{}
	// kicked is when ListProjects last asked for a check of images it
	// hadn't seen checked, e.g. on an Agent paired since the last check.
	kicked time.Time

	mu         sync.Mutex
	images     map[string]digestResult // by ref.String()
	imageCheck checkState
	runs       map[string]*run
	seq        int
}

// digestResult is what a registry said about one tag.
type digestResult struct {
	digest  string
	err     string
	checked time.Time
}

type checkState struct {
	last, next time.Time
	running    bool
	requests   int
	problems   map[string]string
}

func (c *component) Start(ctx context.Context, env *sdk.Env, _ *pb.ComposeConfig) error {
	c.agents = sdk.Require(env, dapb.NewDockerAgentApiServiceClient)
	c.registry = sdk.Require(env, regpb.NewRegistryApiServiceClient)
	c.checks = sdk.Require(env, checkspb.NewChecksServiceClient)
	c.notify = sdk.Require(env, notifypb.NewNotifyServiceClient)
	c.db, c.log, c.public, c.life = env.DB, env.Log, strings.TrimRight(env.PublicURL, "/"), ctx
	c.images = map[string]digestResult{}
	c.runs = map[string]*run{}
	c.kick = make(chan struct{}, 1)
	go c.checkLoop(ctx)
	return nil
}

// Health is ok: compose keeps nothing that can fail but its DB; the
// Agents' health is docker-agent-api's.
func (c *component) Health(ctx context.Context) sdk.Health {
	if err := c.db.PingContext(ctx); err != nil {
		return sdk.Health{State: sdk.Down, Message: err.Error()}
	}
	return sdk.Health{State: sdk.OK}
}

// firstCheck waits a little after start, so the Agents are paired and
// read before the first registry check.
const firstCheck = 20 * time.Second

func (c *component) checkLoop(ctx context.Context) {
	timer := time.NewTimer(firstCheck)
	defer timer.Stop()
	c.mu.Lock()
	c.imageCheck.next = time.Now().Add(firstCheck)
	c.mu.Unlock()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		case <-c.kick:
		}
		c.checkImages(ctx)
		c.mu.Lock()
		c.imageCheck.next = time.Now().Add(CheckInterval)
		c.mu.Unlock()
		timer.Reset(CheckInterval)
	}
}

// unseenEvery is how often images never checked (on an Agent paired since
// the last check) start a check of their own, at most.
const unseenEvery = time.Minute

// checkSoon asks the check loop for a check, at most every unseenEvery and
// not before the first scheduled one.
func (c *component) checkSoon() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.imageCheck.last.IsZero() || c.imageCheck.running || time.Since(c.kicked) < unseenEvery {
		return
	}
	c.kicked = time.Now()
	select {
	case c.kick <- struct{}{}:
	default:
	}
}

func (c *component) CheckImages(ctx context.Context, _ *pb.CheckImagesRequest) (*pb.CheckImagesResponse, error) {
	c.checkImages(ctx)
	return &pb.CheckImagesResponse{Checks: c.checkProto()}, nil
}

// checkImages asks each registry once per distinct running image, then
// notifies the images that turned stale.
func (c *component) checkImages(ctx context.Context) {
	c.mu.Lock()
	if c.imageCheck.running {
		c.mu.Unlock()
		return
	}
	c.imageCheck.running = true
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		c.imageCheck.running = false
		c.mu.Unlock()
	}()
	res, err := c.agents.ListComposeProjects(ctx, &dapb.ListComposeProjectsRequest{})
	if err != nil {
		c.log.Warn("image check: can't read the Docker Agents", "err", err)
		return
	}
	refs := map[string]ref{}
	for _, h := range res.GetHosts() {
		for _, ct := range h.GetContainers() {
			if ct.GetState() != "running" || len(ct.GetRepoDigests()) == 0 {
				continue
			}
			r := parseRef(ct.GetImage())
			if r.digest == "" {
				refs[r.String()] = r
			}
		}
	}
	problems := map[string]string{}
	requests := 0
	keys := slices.Sorted(func(yield func(string) bool) {
		for k := range refs {
			if !yield(k) {
				return
			}
		}
	})
	for i, k := range keys {
		r := refs[k]
		if _, limited := problems[r.registry]; limited {
			continue
		}
		if i > 0 {
			select {
			case <-ctx.Done():
				return
			case <-time.After(requestGap):
			}
		}
		result := digestResult{checked: time.Now()}
		got, err := c.registry.GetDigest(ctx, &regpb.GetDigestRequest{Registry: r.registry, Repository: r.repo, Tag: r.tag})
		requests += int(got.GetRequests())
		switch {
		case err != nil:
			// registry-api itself didn't answer.
			result.err = status.Convert(err).Message()
			problems[r.registry] = result.err
		case got.GetOutcome() == regpb.Outcome_OUTCOME_FOUND:
			result.digest = got.GetDigest()
		case got.GetOutcome() == regpb.Outcome_OUTCOME_RATE_LIMITED:
			result.err = got.GetMessage()
			problems[r.registry] = "rate limited: ddash waits for the next check"
		case got.GetOutcome() == regpb.Outcome_OUTCOME_NO_SUCH_IMAGE:
			// The image's problem, not the registry's.
			result.err = got.GetMessage()
		default:
			result.err = got.GetMessage()
			problems[r.registry] = result.err
		}
		c.mu.Lock()
		c.images[k] = result
		c.mu.Unlock()
	}
	c.mu.Lock()
	c.imageCheck.last, c.imageCheck.requests, c.imageCheck.problems = time.Now(), requests, problems
	c.mu.Unlock()
	c.notifyStale(ctx, res)
}

// status is a container's image state from the last check.
func (c *component) status(ct *dapb.Container) *pb.ImageStatus {
	r := parseRef(ct.GetImage())
	current := ""
	for _, d := range ct.GetRepoDigests() {
		if digest, ok := r.sameRepo(d); ok {
			current = digest
		}
	}
	switch {
	case r.digest != "":
		return &pb.ImageStatus{State: pb.ImageState_IMAGE_STATE_NOT_CHECKED, Reason: "pinned by digest", CurrentDigest: r.digest}
	case len(ct.GetRepoDigests()) == 0:
		return &pb.ImageStatus{State: pb.ImageState_IMAGE_STATE_NOT_CHECKED, Reason: "a local image: it has no registry to check"}
	case ct.GetState() != "running":
		return &pb.ImageStatus{State: pb.ImageState_IMAGE_STATE_UNKNOWN, Reason: "not running", CurrentDigest: current}
	}
	c.mu.Lock()
	res, ok := c.images[r.String()]
	c.mu.Unlock()
	out := &pb.ImageStatus{CurrentDigest: current}
	switch {
	case !ok:
		out.State, out.Reason = pb.ImageState_IMAGE_STATE_UNKNOWN, "not checked yet"
		c.checkSoon()
		return out
	case res.err != "":
		out.State, out.Reason = pb.ImageState_IMAGE_STATE_ERROR, res.err
	case slices.ContainsFunc(ct.GetRepoDigests(), func(d string) bool { digest, same := r.sameRepo(d); return same && digest == res.digest }):
		out.State = pb.ImageState_IMAGE_STATE_UP_TO_DATE
	default:
		out.State = pb.ImageState_IMAGE_STATE_STALE
	}
	out.LatestDigest, out.Checked = res.digest, timestamppb.New(res.checked)
	return out
}

// notifyStale sends one Notification for every container whose image
// turned stale with a digest not notified before.
func (c *component) notifyStale(ctx context.Context, res *dapb.ListComposeProjectsResponse) {
	type stale struct{ host, name, image, digest string }
	var fresh []stale
	for _, h := range res.GetHosts() {
		for _, ct := range h.GetContainers() {
			st := c.status(ct)
			if st.GetState() != pb.ImageState_IMAGE_STATE_STALE {
				continue
			}
			key := h.GetHost() + "/" + ct.GetName()
			var seen int
			if err := c.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM notified WHERE container = ? AND digest = ?`, key, st.GetLatestDigest()).Scan(&seen); err != nil {
				c.log.Warn("image check: can't read what was notified", "err", err)
				return
			}
			if seen == 0 {
				fresh = append(fresh, stale{h.GetHost(), ct.GetName(), ct.GetImage(), st.GetLatestDigest()})
			}
		}
	}
	if len(fresh) == 0 {
		return
	}
	n := &notifypb.Notification{Category: notifypb.Category_CATEGORY_IMAGE_UPDATES, Tags: []string{"package"}, Click: c.public + "/docker", Priority: 3}
	if len(fresh) == 1 {
		n.Title = "Image update for " + fresh[0].name
	} else {
		n.Title = fmt.Sprintf("%d image updates", len(fresh))
	}
	var lines []string
	for _, s := range fresh {
		lines = append(lines, fmt.Sprintf("%s on %s: %s", s.name, s.host, s.image))
	}
	n.Message = strings.Join(lines, "\n")
	if _, err := c.notify.Send(ctx, &notifypb.SendRequest{Notification: n}); err != nil {
		c.log.Warn("image check: can't notify", "err", err)
		return
	}
	for _, s := range fresh {
		if _, err := c.db.ExecContext(ctx, `INSERT OR IGNORE INTO notified (container, digest, at) VALUES (?, ?, ?)`, s.host+"/"+s.name, s.digest, time.Now().UTC().Format(time.RFC3339)); err != nil {
			c.log.Warn("image check: can't keep what was notified", "err", err)
		}
	}
}

func (c *component) checkProto() *pb.ImageChecks {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := &pb.ImageChecks{Requests: int32(c.imageCheck.requests), Running: c.imageCheck.running}
	if !c.imageCheck.last.IsZero() {
		out.Last = timestamppb.New(c.imageCheck.last)
	}
	if !c.imageCheck.next.IsZero() {
		out.Next = timestamppb.New(c.imageCheck.next)
	}
	for _, reg := range slices.Sorted(func(yield func(string) bool) {
		for k := range c.imageCheck.problems {
			if !yield(k) {
				return
			}
		}
	}) {
		out.Problems = append(out.Problems, &pb.RegistryProblem{Registry: reg, Message: c.imageCheck.problems[reg]})
	}
	return out
}

func (c *component) ListProjects(ctx context.Context, _ *pb.ListProjectsRequest) (*pb.ListProjectsResponse, error) {
	res, err := c.agents.ListComposeProjects(ctx, &dapb.ListComposeProjectsRequest{})
	if err != nil {
		return nil, err
	}
	out := &pb.ListProjectsResponse{Checks: c.checkProto()}
	for _, h := range res.GetHosts() {
		out.Hosts = append(out.Hosts, c.host(h))
	}
	return out, nil
}

// host is h's Compose Projects as the Operator sees them.
func (c *component) host(h *dapb.HostProjects) *pb.Host {
	out := &pb.Host{Name: h.GetHost(), Address: h.GetAddress(), Error: h.GetError(), Roots: h.GetRoots()}
	byID := map[string]*dapb.Container{}
	for _, ct := range h.GetContainers() {
		byID[ct.GetId()] = ct
	}
	used := map[string]bool{}
	for _, p := range h.GetProjects() {
		pp := &pb.Project{Host: h.GetHost(), Name: p.GetName(), WorkingDir: p.GetWorkingDir(), Error: p.GetError(), RunId: c.runOn(h.GetHost(), p.GetName())}
		if len(p.GetConfigFiles()) > 0 {
			pp.ConfigFile = p.GetConfigFiles()[0]
		}
		var services, running, containers int
		for _, s := range p.GetServices() {
			ss := &pb.Service{Name: s.GetName(), Image: s.GetImage(), Ports: s.GetPorts()}
			up := false
			for _, id := range s.GetContainerIds() {
				ct, ok := byID[id]
				if !ok {
					continue
				}
				used[id] = true
				containers++
				cp := c.container(ct)
				ss.Containers = append(ss.Containers, cp)
				if ct.GetState() == "running" {
					up = true
				}
				if cp.GetImageStatus().GetState() == pb.ImageState_IMAGE_STATE_STALE {
					pp.UpdateAvailable = true
				}
				if cp.GetProtected() {
					pp.Protected = true
				}
			}
			services++
			if up {
				running++
			}
			pp.Services = append(pp.Services, ss)
		}
		switch {
		case containers == 0:
			pp.State = pb.ProjectState_PROJECT_STATE_NOT_STARTED
		case running == services:
			pp.State = pb.ProjectState_PROJECT_STATE_RUNNING
		case running == 0:
			pp.State = pb.ProjectState_PROJECT_STATE_STOPPED
		default:
			pp.State = pb.ProjectState_PROJECT_STATE_PARTIAL
		}
		out.Projects = append(out.Projects, pp)
	}
	for _, ct := range h.GetContainers() {
		if !used[ct.GetId()] {
			out.Containers = append(out.Containers, c.container(ct))
		}
	}
	return out
}

func (c *component) container(ct *dapb.Container) *pb.Container {
	out := &pb.Container{
		Id: ct.GetId(), Name: ct.GetName(), Image: ct.GetImage(), State: ct.GetState(), Status: ct.GetStatus(),
		ImageStatus: c.status(ct), Protected: ct.GetLabels()[agent.LabelProtected] == "true", Created: ct.GetCreated(),
	}
	for _, p := range ct.GetPorts() {
		out.Ports = append(out.Ports, &pb.Port{Ip: p.GetIp(), PublicPort: p.GetPublicPort(), PrivatePort: p.GetPrivatePort(), Protocol: p.GetProtocol()})
	}
	return out
}
