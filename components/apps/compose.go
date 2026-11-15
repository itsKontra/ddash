package apps

import (
	"context"
	"fmt"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/itsKontra/ddash/components/internal/dockerread"

	containerpb "github.com/itsKontra/ddash/proto/ddash/container"

	"go.yaml.in/yaml/v3"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	"github.com/itsKontra/ddash/components/apps/appspb"
	dapb "github.com/itsKontra/ddash/components/docker-agent-api/dockeragentapipb"
	"github.com/itsKontra/ddash/components/docker-api/dockerapipb"
	proxyhostpb "github.com/itsKontra/ddash/proto/ddash/proxyhost"
)

// Add App from a new Compose Project: its Plan's Docker steps (write the
// Compose file and its .env, `up -d`, wait until the App's container runs), which come
// before the Proxy Host and DNS steps, and running them through
// docker-agent-api. The Agent runs `up` as a job; RunAction follows it
// until ComposeWait has passed since the Action began, and then stops,
// so the whole Action fits the host's 60 s deadline.

// ComposeWait bounds how long an Add App follows its Compose Project's
// start, counted from when the Action began.
const ComposeWait = 40 * time.Second

// pollEvery is how often a running Compose step re-reads the Agent.
const pollEvery = 300 * time.Millisecond

// startedFor is how long a container an Add App started counts as listed
// while docker-api doesn't list it yet (its cache, the next sync).
const startedFor = 2 * time.Minute

// composeFile is the Compose file's name in a new Compose Project's
// directory.
const composeFile = "compose.yaml"

// composeRun is how a planned Add App's Docker steps run.
type composeRun struct {
	host, address, name, dir, content, env string
	// service is the Compose service whose container the App is; port the
	// host port it publishes, 0 for none.
	service string
	port    uint32
}

// container is the name Compose gives the service's first container.
func (r *composeRun) container() string { return r.name + "-" + r.service + "-1" }

// dockerRead is a read of docker-agent-api's Docker hosts for a Plan.
type dockerRead struct {
	hosts []*dapb.HostProjects
	err   error
}

// readDocker reads the Docker hosts with a paired Agent.
func (c *component) readDocker(ctx context.Context) *dockerRead {
	resp, err := c.agents.ListComposeProjects(ctx, &dapb.ListComposeProjectsRequest{})
	return &dockerRead{hosts: resp.GetHosts(), err: err}
}

func (d *dockerRead) host(name string) *dapb.HostProjects {
	for _, h := range d.hosts {
		if h.GetHost() == name {
			return h
		}
	}
	return nil
}

var projectNameRE = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)

// newCompose plans a new Compose Project's Docker steps into out, blocking
// with why it can't be added, and sets how they run; reach is the App's.
// It returns the project as planned, for the steps after.
func (p planning) newCompose(out *planned, a *appspb.NewComposeProject, reach appspb.Reach) *composeRun {
	r := &composeRun{
		host: strings.TrimSpace(a.GetHost()), name: strings.TrimSpace(a.GetName()),
		dir: strings.TrimSpace(a.GetDirectory()), content: a.GetContent(), env: a.GetEnv(),
		service: strings.TrimSpace(a.GetService()), port: a.GetPort(),
	}
	ok := true
	block := func(format string, args ...any) {
		out.block(fmt.Sprintf(format, args...))
		ok = false
	}

	var h *dapb.HostProjects
	switch {
	case r.host == "":
		block("Pick the Docker host the Compose Project runs on.")
	case p.docker == nil || p.docker.err != nil:
		block("%s", cantCheck("the Docker Agents"))
	default:
		if h = p.docker.host(r.host); h == nil {
			block("%s has no paired Docker Agent.", r.host)
		} else if h.GetError() != "" {
			block("Can't reach %s's Docker Agent: %s", r.host, h.GetError())
			h = nil
		}
	}
	if h != nil {
		r.address = h.GetAddress()
	}

	switch {
	case r.name == "":
		block("Name the Compose Project, e.g. paperless.")
	case !projectNameRE.MatchString(r.name):
		block("The Compose Project's name takes lower-case letters, digits, - and _, e.g. paperless.")
	case h != nil && slices.ContainsFunc(h.GetProjects(), func(x *dapb.Project) bool { return x.GetName() == r.name }):
		block("%s already has a Compose Project %s.", r.host, r.name)
	}

	if h != nil {
		roots := h.GetRoots()
		if r.dir == "" && len(roots) > 0 && r.name != "" {
			r.dir = path.Join(roots[0], r.name)
		}
		switch under := slices.ContainsFunc(roots, func(root string) bool { return strings.HasPrefix(r.dir, strings.TrimSuffix(root, "/")+"/") }); {
		case len(roots) == 0:
			block("%s's Docker Agent mounts no Compose root to put the Compose Project in.", r.host)
		case r.dir == "":
		case !path.IsAbs(r.dir) || path.Clean(r.dir) != r.dir || !under:
			block("The directory must be a path under one of %s's Compose roots: %s.", r.host, strings.Join(roots, ", "))
		default:
			for _, x := range h.GetProjects() {
				if x.GetWorkingDir() == r.dir {
					block("%s is already the Compose Project %s's directory.", r.dir, x.GetName())
				}
			}
		}
	}

	services, err := parseServices(r.content)
	out.plan.ComposeServices = services
	switch {
	case strings.TrimSpace(r.content) == "":
		block("Give the Compose file.")
	case err != nil:
		block("The Compose file doesn't parse: %v.", err)
	case len(services) == 0:
		block("The Compose file defines no services.")
	}

	if len(services) > 0 {
		if r.service == "" {
			r.service = services[0].GetName()
			for _, s := range services {
				if len(s.GetPorts()) > 0 {
					r.service = s.GetName()
					break
				}
			}
		}
		i := slices.IndexFunc(services, func(s *appspb.ComposeService) bool { return s.GetName() == r.service })
		switch {
		case i < 0:
			block("The Compose file has no service %s.", r.service)
		case r.port == 0 && len(services[i].GetPorts()) > 0:
			r.port = services[i].GetPorts()[0]
		case r.port != 0 && !slices.Contains(services[i].GetPorts(), r.port):
			block("The Compose service %s doesn't publish port %d.", r.service, r.port)
		}
		if i >= 0 && r.port == 0 && reach == appspb.Reach_REACH_PROXIED {
			block("The Compose service %s publishes no port for the Proxy Host to forward to: publish one, e.g. \"8080:80\".", r.service)
		}
	}
	if h != nil && r.port != 0 {
		for _, ct := range h.GetContainers() {
			for _, pt := range ct.GetPorts() {
				if pt.GetPublicPort() == r.port && (pt.GetProtocol() == containerpb.Protocol_PROTOCOL_UNSPECIFIED || pt.GetProtocol() == containerpb.Protocol_PROTOCOL_TCP) {
					block("Port %d is already published on %s, by %s.", r.port, r.host, ct.GetName())
				}
			}
		}
	}

	var names []string
	for _, s := range services {
		names = append(names, s.GetName())
	}
	file := path.Join(r.dir, composeFile)
	if r.dir == "" {
		file = ""
	}
	write := &appspb.PlanStep{System: appspb.System_SYSTEM_DOCKER, Title: fmt.Sprintf("Write the Compose file of %s on %s", r.name, r.host), Values: []*appspb.PlanValue{
		{Label: "Compose file", After: file},
		{Label: "Compose services", After: strings.Join(names, ", ")},
	}}
	if r.env != "" {
		env := ""
		if r.dir != "" {
			env = path.Join(r.dir, ".env")
		}
		write.Values = append(write.Values, &appspb.PlanValue{Label: ".env", After: env})
	}
	out.plan.Steps = append(out.plan.Steps, write,
		&appspb.PlanStep{System: appspb.System_SYSTEM_DOCKER, Title: fmt.Sprintf("Start %s on %s", r.name, r.host), Values: []*appspb.PlanValue{
			{Label: "Runs", After: "docker compose up -d, pulling its images first"},
		}},
		&appspb.PlanStep{System: appspb.System_SYSTEM_DOCKER, Title: fmt.Sprintf("Wait until %s runs", r.service), Values: waitValues(r)},
	)
	if ok {
		out.compose = r
	}
	return r
}

// upstream is a new Compose Project's App's Proxy Host fields in: its
// upstream host the Docker host's address unless given, its port the
// Compose service's unless given, http unless given.
func (r *composeRun) upstream(in *proxyhostpb.ProxyHostFields) *proxyhostpb.ProxyHostFields {
	f := &proxyhostpb.ProxyHostFields{}
	if in != nil {
		f = proto.CloneOf(in)
	}
	if strings.TrimSpace(f.GetForwardHost()) == "" {
		f.ForwardHost = r.address
	}
	if f.GetForwardPort() == 0 {
		f.ForwardPort = int32(r.port)
	}
	if strings.TrimSpace(f.GetForwardScheme()) == "" {
		f.ForwardScheme = "http"
	}
	return f
}

func waitValues(r *composeRun) []*appspb.PlanValue {
	out := []*appspb.PlanValue{{Label: "Container", After: r.container()}}
	if r.port != 0 {
		out = append(out, &appspb.PlanValue{Label: "Published at", After: fmt.Sprintf("%s:%d", r.address, r.port)})
	}
	return out
}

// parseServices reads a Compose file's services, in the file's order,
// each with the host ports it publishes over tcp. A port Compose would
// resolve from a variable, or a range, isn't listed.
func parseServices(content string) ([]*appspb.ComposeService, error) {
	if strings.TrimSpace(content) == "" {
		return nil, nil
	}
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(content), &doc); err != nil {
		return nil, err
	}
	if len(doc.Content) == 0 {
		return nil, nil
	}
	services := mapValue(doc.Content[0], "services")
	if services == nil || services.Kind != yaml.MappingNode {
		return nil, nil
	}
	var out []*appspb.ComposeService
	for i := 0; i+1 < len(services.Content); i += 2 {
		s := &appspb.ComposeService{Name: services.Content[i].Value}
		if ports := mapValue(services.Content[i+1], "ports"); ports != nil && ports.Kind == yaml.SequenceNode {
			for _, p := range ports.Content {
				if n := publishedPort(p); n != 0 && !slices.Contains(s.Ports, n) {
					s.Ports = append(s.Ports, n)
				}
			}
		}
		out = append(out, s)
	}
	return out, nil
}

// mapValue is the value of key in the mapping node m; nil for none.
func mapValue(m *yaml.Node, key string) *yaml.Node {
	if m == nil || m.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}

// publishedPort is the tcp host port a Compose ports entry publishes: the
// short syntax "[ip:]host:container[/proto]" or the long one with
// published; 0 for none.
func publishedPort(p *yaml.Node) uint32 {
	switch p.Kind {
	case yaml.ScalarNode:
		spec, proto, _ := strings.Cut(p.Value, "/")
		if proto != "" && proto != "tcp" {
			return 0
		}
		parts := strings.Split(spec, ":")
		if len(parts) < 2 {
			return 0 // only the container's port: Docker picks one
		}
		return portNumber(parts[len(parts)-2])
	case yaml.MappingNode:
		if proto := mapValue(p, "protocol"); proto != nil && proto.Value != "tcp" {
			return 0
		}
		if pub := mapValue(p, "published"); pub != nil {
			return portNumber(pub.Value)
		}
	}
	return 0
}

func portNumber(s string) uint32 {
	n, err := strconv.ParseUint(strings.Trim(s, `"' `), 10, 16)
	if err != nil {
		return 0
	}
	return uint32(n)
}

// runCompose runs a planned Add App's Docker steps, the Plan's first
// three, marking progress; begun is when the Action began. It returns how
// many steps it did, and why it stopped.
func (c *component) runCompose(ctx context.Context, r *composeRun, progress *progress, begun time.Time) (int, error) {
	deadline := begun.Add(c.composeWait)

	progress.mark(0, 1, appspb.StepState_STEP_STATE_RUNNING)
	created, err := c.agents.CreateComposeProject(ctx, &dapb.CreateComposeProjectRequest{Host: r.host, Name: r.name, Directory: r.dir, Content: r.content, Env: r.env})
	if err != nil {
		return 0, err
	}
	progress.mark(0, 1, appspb.StepState_STEP_STATE_DONE)

	progress.mark(1, 2, appspb.StepState_STEP_STATE_RUNNING)
	up, err := c.agents.UpProject(ctx, &dapb.UpProjectRequest{
		Target: &dapb.ComposeTarget{Host: r.host, Project: r.name, WorkingDir: r.dir, File: created.GetFile().GetPath()},
		Pull:   true, Wait: true,
	})
	if err != nil {
		return 1, err
	}
	if err := c.followJob(ctx, r, up.GetJob().GetId(), deadline); err != nil {
		return 1, err
	}
	progress.mark(1, 2, appspb.StepState_STEP_STATE_DONE)

	progress.mark(2, 3, appspb.StepState_STEP_STATE_RUNNING)
	if err := c.waitRunning(ctx, r, deadline); err != nil {
		return 2, err
	}
	progress.mark(2, 3, appspb.StepState_STEP_STATE_DONE)
	return 3, nil
}

// stillStarting is why an Add App stops while its Compose Project is
// still starting.
func stillStarting(r *composeRun, what string) error {
	return status.Errorf(codes.DeadlineExceeded,
		"%s on %s after %s; it goes on there, and the Docker Page shows how it ends. Once %s runs, add the App with Reverse proxy to its upstream %s:%d",
		what, r.host, ComposeWait, r.service, r.address, r.port)
}

// followJob waits until the Agent's job id ends, then collects it: a
// failed job fails with its error. A job already collected elsewhere
// counts as ended; the next step sees how.
func (c *component) followJob(ctx context.Context, r *composeRun, id string, deadline time.Time) error {
	for {
		resp, err := c.agents.GetJob(ctx, &dapb.GetJobRequest{Host: r.host, Id: id})
		if status.Code(err) == codes.NotFound {
			break
		}
		if err != nil {
			return err
		}
		if resp.GetJob().GetEnd() != nil {
			break
		}
		if time.Now().After(deadline) {
			return stillStarting(r, "still starting")
		}
		if err := sleep(ctx, pollEvery); err != nil {
			return err
		}
	}
	if _, err := c.agents.CollectJob(ctx, &dapb.CollectJobRequest{Host: r.host, Id: id}); err != nil && status.Code(err) != codes.NotFound {
		return err
	}
	return nil
}

// waitRunning waits until the service's container runs, and lists it
// until docker-api does.
func (c *component) waitRunning(ctx context.Context, r *composeRun, deadline time.Time) error {
	state := "not created"
	for {
		resp, err := c.agents.ListContainers(ctx, &dapb.ListContainersRequest{})
		if err != nil {
			return err
		}
		for _, h := range resp.GetHosts() {
			if h.GetHost() != r.host {
				continue
			}
			for _, ct := range h.GetContainers() {
				if ct.GetProject() != r.name || ct.GetService() != r.service {
					continue
				}
				if state = dockerread.StateWord(ct.GetState()); ct.GetState() == containerpb.State_STATE_RUNNING {
					c.startedContainer(h.GetHost(), h.GetAddress(), ct)
					return nil
				}
			}
		}
		if time.Now().After(deadline) {
			if state == "not created" {
				return stillStarting(r, r.container()+" isn't created yet")
			}
			return status.Errorf(codes.FailedPrecondition, "%s is %s, not running: its logs on %s say why", r.container(), state, r.host)
		}
		if err := sleep(ctx, pollEvery); err != nil {
			return err
		}
	}
}

func sleep(ctx context.Context, d time.Duration) error {
	select {
	case <-ctx.Done():
		return status.FromContextError(ctx.Err()).Err()
	case <-time.After(d):
		return nil
	}
}

// started is a container an Add App started, listed as a Workload until
// docker-api lists it or startedFor has passed.
type started struct {
	host, address string
	container     *containerpb.Container
	until         time.Time
}

// startedContainer keeps the container ct an Add App started on host, so
// the App's Workload matches it at once, as wroteProxyHost keeps a Proxy
// Host.
func (c *component) startedContainer(host, address string, ct *containerpb.Container) {
	out := ct
	c.mu.Lock()
	defer c.mu.Unlock()
	c.started = append(c.started, started{host: host, address: address, container: out, until: time.Now().Add(startedFor)})
	// A copy: views of the last read may be in use.
	docker := slices.Clone(c.src.docker)
	if i := slices.IndexFunc(docker, func(h *dockerapipb.HostContainers) bool { return h.GetHost() == host }); i >= 0 {
		h := proto.CloneOf(docker[i])
		h.Containers = append(h.Containers, out)
		docker[i] = h
	} else {
		docker = append(docker, &dockerapipb.HostContainers{Host: host, Address: address, Containers: []*containerpb.Container{out}})
	}
	c.src.docker = docker
}

// withStarted is docker-api's hosts with the containers Add Apps started
// that it doesn't list yet. It forgets those it lists, and expired ones.
func (c *component) withStarted(hosts []*dockerapipb.HostContainers) []*dockerapipb.HostContainers {
	c.mu.Lock()
	defer c.mu.Unlock()
	keep := c.started[:0]
	for _, s := range c.started {
		i := slices.IndexFunc(hosts, func(h *dockerapipb.HostContainers) bool { return h.GetHost() == s.host })
		listed := i >= 0 && slices.ContainsFunc(hosts[i].GetContainers(), func(x *containerpb.Container) bool { return x.GetName() == s.container.GetName() })
		if listed || time.Now().After(s.until) {
			continue
		}
		keep = append(keep, s)
		if i < 0 {
			hosts = append(hosts, &dockerapipb.HostContainers{Host: s.host, Address: s.address})
			i = len(hosts) - 1
		}
		hosts[i].Containers = append(hosts[i].Containers, s.container)
	}
	c.started = keep
	return hosts
}

func (c *component) ListDockerHosts(ctx context.Context, _ *appspb.ListDockerHostsRequest) (*appspb.ListDockerHostsResponse, error) {
	resp, err := c.agents.ListComposeProjects(ctx, &dapb.ListComposeProjectsRequest{})
	if err != nil {
		return nil, err
	}
	out := &appspb.ListDockerHostsResponse{}
	for _, h := range resp.GetHosts() {
		d := &appspb.DockerHost{Name: h.GetHost(), Address: h.GetAddress(), Roots: h.GetRoots(), Error: h.GetError()}
		for _, p := range h.GetProjects() {
			d.Projects = append(d.Projects, p.GetName())
		}
		for _, ct := range h.GetContainers() {
			for _, p := range ct.GetPorts() {
				if p.GetPublicPort() != 0 && !slices.Contains(d.UsedPorts, p.GetPublicPort()) {
					d.UsedPorts = append(d.UsedPorts, p.GetPublicPort())
				}
			}
		}
		slices.Sort(d.Projects)
		slices.Sort(d.UsedPorts)
		out.Hosts = append(out.Hosts, d)
	}
	return out, nil
}
