// Package dockeragentapi is the docker-agent-api Component: it pairs with
// the Docker Agents the Operator adds and reaches them over HTTPS, pinned
// to the certificate each showed at pairing, with the token both keep.
// It refuses every operation but Update Agent on an Agent of another
// Release, so the Agents never drift from the core.
package dockeragentapi

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"path"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/itsKontra/ddash/components/internal/dockerread"
	containerpb "github.com/itsKontra/ddash/proto/ddash/container"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/itsKontra/ddash/agent"
	"github.com/itsKontra/ddash/agent/jobproto"
	pb "github.com/itsKontra/ddash/components/docker-agent-api/dockeragentapipb"
	"github.com/itsKontra/ddash/components/internal/agentcall"
	"github.com/itsKontra/ddash/sdk"
	"github.com/itsKontra/ddash/sdk/migrate"
)

//go:embed component.yaml
var manifest []byte

//go:embed migrations/*.sql
var migrations embed.FS

// Image is the Docker Agent's image by digest, stamped by the release
// build (-X); empty: the image by the Release's tag.
var Image = ""

// imageRepo is where the Docker Agent's image is published.
const imageRepo = "ghcr.io/itskontra/ddash-docker-agent"

// PairInterval is how often an enrolling Agent is tried.
const PairInterval = 10 * time.Second

// Definition is docker-agent-api, ready for sdk.Serve.
func Definition() sdk.Definition {
	return sdk.Define(sdk.Spec[*pb.DockerAgentApiConfig]{
		Manifest:   manifest,
		Service:    &pb.DockerAgentApiService_ServiceDesc,
		Migrations: migrations,
		New:        func() sdk.Component[*pb.DockerAgentApiConfig] { return &component{} },
	})
}

type component struct {
	pb.UnimplementedDockerAgentApiServiceServer
	db  *sql.DB
	log *slog.Logger
	// release is the Release ddash runs: an Agent on another is outdated.
	release string

	// reads caches every paired Agent's info, containers and projects.
	reads *sdk.Cache[[]reading]

	mu sync.Mutex
	// pairErr is why the last pairing attempt of an enrolling Agent failed.
	pairErr map[string]string
	// pairing serialises pairing: the loop and PairAgent never pair the
	// same Agent at once, which would leave it with the other's token.
	pairing sync.Mutex
}

func (c *component) Start(ctx context.Context, env *sdk.Env, _ *pb.DockerAgentApiConfig) error {
	c.db, c.log, c.release = env.DB, env.Log, env.Release
	c.pairErr = map[string]string{}
	c.reads = sdk.Cached(sdk.TTLFast, c.readAll)
	sdk.Every(ctx, PairInterval, c.pairPending)
	return nil
}

// Health is degraded while a paired Agent can't be read or runs another
// Release; ok without any Agent.
func (c *component) Health(ctx context.Context) sdk.Health {
	rs, err := c.reads.Get(ctx)
	if err != nil {
		return sdk.Health{State: sdk.Down, Message: err.Error()}
	}
	var problems []string
	for _, r := range rs {
		if r.state != pb.AgentState_AGENT_STATE_ENROLLING && r.message != "" {
			problems = append(problems, r.name+": "+r.message)
		}
	}
	if len(problems) > 0 {
		return sdk.Health{State: sdk.Degraded, Message: strings.Join(problems, "; ")}
	}
	return sdk.Health{State: sdk.OK}
}

// row is one Agent as stored.
type row struct {
	name, url, code, token, fingerprint string
	roots                               []string
	added, paired                       time.Time
}

func (c *component) rows(ctx context.Context) ([]row, error) {
	rs, err := c.db.QueryContext(ctx, `SELECT name, url, roots, code, token, fingerprint, added, paired FROM agent ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rs.Close()
	var out []row
	for rs.Next() {
		var r row
		var roots, added, paired string
		if err := rs.Scan(&r.name, &r.url, &roots, &r.code, &r.token, &r.fingerprint, &added, &paired); err != nil {
			return nil, err
		}
		r.roots = splitRoots(roots)
		r.added, _ = time.Parse(time.RFC3339, added)
		r.paired, _ = time.Parse(time.RFC3339, paired)
		out = append(out, r)
	}
	return out, rs.Err()
}

func splitRoots(s string) []string {
	var out []string
	for r := range strings.SplitSeq(s, "\n") {
		if r = strings.TrimSpace(r); r != "" {
			out = append(out, r)
		}
	}
	return out
}

func (c *component) row(ctx context.Context, name string) (row, error) {
	rows, err := c.rows(ctx)
	if err != nil {
		return row{}, err
	}
	for _, r := range rows {
		if r.name == name {
			return r, nil
		}
	}
	return row{}, status.Errorf(codes.NotFound, "no Docker Agent %q", name)
}

func (r row) client() *agent.Client { return agent.Dial(r.url, r.token, r.fingerprint) }

func (r row) address() string {
	u, err := url.Parse(r.url)
	if err != nil {
		return ""
	}
	return u.Hostname()
}

// reading is one Agent as last read.
type reading struct {
	row
	state    pb.AgentState
	message  string
	info     agent.Info
	projects agent.ProjectsResponse
}

// readTimeout bounds one Agent's read, so a dead one can't hold a getter.
const readTimeout = 5 * time.Second

// readAll reads every paired Agent in parallel; one that fails is listed
// with why, so readAll fails only when the DB does.
func (c *component) readAll(ctx context.Context) ([]reading, error) {
	rows, err := c.rows(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]reading, len(rows))
	var wg sync.WaitGroup
	for i, r := range rows {
		out[i].row = r
		if r.token == "" {
			out[i].state = pb.AgentState_AGENT_STATE_ENROLLING
			c.mu.Lock()
			out[i].message = c.pairErr[r.name]
			c.mu.Unlock()
			continue
		}
		wg.Go(func() { c.read(ctx, &out[i]) })
	}
	wg.Wait()
	return out, nil
}

func (c *component) read(ctx context.Context, r *reading) {
	ctx, cancel := context.WithTimeout(ctx, readTimeout)
	defer cancel()
	cl := r.client()
	if err := cl.Get(ctx, agent.PathInfo, nil, &r.info); err != nil {
		r.state, r.message = stateOf(err)
		return
	}
	if r.info.Release != c.release {
		r.state = pb.AgentState_AGENT_STATE_OUTDATED
		r.message = fmt.Sprintf("it runs Release %s and ddash runs %s: update the Agent", r.info.Release, c.release)
		return
	}
	r.state = pb.AgentState_AGENT_STATE_PAIRED
	if err := cl.Get(ctx, agent.PathProjects, nil, &r.projects); err != nil {
		var refused *agent.APIError
		if errors.As(err, &refused) && refused.Code == agent.CodeFailed {
			// The Agent answers, its Docker doesn't.
			r.message = "the Agent can't read Docker: " + refused.Message
			return
		}
		r.state, r.message = stateOf(err)
	}
}

// stateOf is the state an Agent is in when a call to it failed with err.
func stateOf(err error) (pb.AgentState, string) {
	st, message := agentcall.StateOf(err)
	return callStates[st], message
}

var callStates = map[agentcall.State]pb.AgentState{
	agentcall.Unreachable:        pb.AgentState_AGENT_STATE_UNREACHABLE,
	agentcall.FingerprintChanged: pb.AgentState_AGENT_STATE_FINGERPRINT_CHANGED,
	agentcall.Refused:            pb.AgentState_AGENT_STATE_REFUSED,
}

func (c *component) ListAgents(ctx context.Context, _ *pb.ListAgentsRequest) (*pb.ListAgentsResponse, error) {
	rs, err := c.reads.Get(ctx)
	if err != nil {
		return nil, err
	}
	out := &pb.ListAgentsResponse{}
	for _, r := range rs {
		out.Agents = append(out.Agents, r.proto())
	}
	return out, nil
}

func (r reading) proto() *pb.Agent {
	a := &pb.Agent{
		Name: r.name, Url: r.url, Address: r.address(), State: r.state, Message: r.message,
		Roots: r.roots, Fingerprint: r.fingerprint, Added: timestamppb.New(r.added),
		Release: r.info.Release, Hostname: r.info.Hostname, DockerVersion: r.info.Docker, ComposeVersion: r.info.Compose,
	}
	if len(r.info.Roots) > 0 {
		a.Roots = r.info.Roots
	}
	if !r.paired.IsZero() {
		a.Paired = timestamppb.New(r.paired)
	}
	return a
}

var validName = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,30}[a-z0-9])?$`)

func (c *component) AddAgent(ctx context.Context, req *pb.AddAgentRequest) (*pb.AddAgentResponse, error) {
	var errs sdk.FieldErrors
	if !validName.MatchString(req.GetName()) {
		errs.Add("name", "is lower-case letters, digits and -, like media")
	}
	if u, err := url.Parse(req.GetUrl()); err != nil || u.Scheme != "https" || u.Hostname() == "" {
		errs.Add("url", "must be the Agent's https URL, like https://198.18.2.10:%d", agent.DockerPort)
	}
	if len(req.GetRoots()) == 0 {
		errs.Add("roots", "needs at least one directory holding Compose Projects")
	}
	for i, r := range req.GetRoots() {
		if !path.IsAbs(r) || path.Clean(r) != r || r == "/" {
			errs.Add(fmt.Sprintf("roots[%d]", i), "must be an absolute directory other than /, like /opt/stacks")
		}
	}
	if err := errs.Err(); err != nil {
		return nil, err
	}
	code := agent.NewCode()
	_, err := c.db.ExecContext(ctx, `INSERT INTO agent (name, url, roots, code, added) VALUES (?, ?, ?, ?, ?)`,
		req.GetName(), strings.TrimRight(req.GetUrl(), "/"), strings.Join(req.GetRoots(), "\n"), code, time.Now().UTC().Format(time.RFC3339))
	if err != nil {
		if migrate.IsTaken(err) {
			return nil, status.Errorf(codes.AlreadyExists, "a Docker Agent is already named %s", req.GetName())
		}
		return nil, err
	}
	c.reads.Invalidate()
	r, err := c.row(ctx, req.GetName())
	if err != nil {
		return nil, err
	}
	return &pb.AddAgentResponse{Agent: reading{row: r, state: pb.AgentState_AGENT_STATE_ENROLLING}.proto(), Setup: setup(r, c.release)}, nil
}

func (c *component) GetAgentSetup(ctx context.Context, req *pb.GetAgentSetupRequest) (*pb.GetAgentSetupResponse, error) {
	r, err := c.row(ctx, req.GetName())
	if err != nil {
		return nil, err
	}
	return &pb.GetAgentSetupResponse{Setup: setup(r, c.release)}, nil
}

// image is the Agent's image for release, the one ddash runs.
func image(release string) string {
	if Image != "" {
		return Image
	}
	return imageRepo + ":" + release
}

// agentDir is where the setup puts the Agent's Compose file.
const agentDir = "/opt/ddash-docker-agent"

// setup is the Compose file that runs r's Agent on release: the Docker
// socket and each root mounted at the same path, its state in a volume,
// its port published, and the enrolment code until it is paired.
func setup(r row, release string) *pb.AgentSetup {
	var b strings.Builder
	fmt.Fprintf(&b, "# The ddash Docker Agent for %s, generated by ddash %s.\n", r.name, release)
	b.WriteString("# It has root-equivalent control of this host through the Docker socket:\n")
	b.WriteString("# ddash can read, change, start and stop every Compose Project under the\n")
	b.WriteString("# directories mounted below. Remove it from ddash to revoke that.\n")
	b.WriteString("name: ddash-docker-agent\n")
	b.WriteString("services:\n")
	b.WriteString("  agent:\n")
	fmt.Fprintf(&b, "    image: %s\n", image(release))
	b.WriteString("    restart: unless-stopped\n")
	fmt.Fprintf(&b, "    ports: [\"%d:%d\"]\n", agent.DockerPort, agent.DockerPort)
	b.WriteString("    environment:\n")
	fmt.Fprintf(&b, "      DDASH_HOST_NAME: %s\n", r.name)
	fmt.Fprintf(&b, "      DDASH_COMPOSE_ROOTS: %s\n", strings.Join(r.roots, ","))
	if r.code != "" {
		b.WriteString("      # One-time: ddash pairs with the Agent with it, then it is ignored.\n")
		fmt.Fprintf(&b, "      DDASH_ENROLMENT_CODE: %s\n", r.code)
	}
	b.WriteString("    volumes:\n")
	b.WriteString("      - /var/run/docker.sock:/var/run/docker.sock\n")
	b.WriteString("      # Each Compose root at the same path, so relative paths and .env\n")
	b.WriteString("      # files resolve as on the host.\n")
	for _, root := range r.roots {
		fmt.Fprintf(&b, "      - %s:%s\n", root, root)
	}
	b.WriteString("      - state:/data\n")
	b.WriteString("    # The Docker socket from a container: SELinux hosts (Fedora) refuse it\n")
	b.WriteString("    # otherwise; elsewhere it changes nothing.\n")
	b.WriteString("    security_opt: [\"label=disable\"]\n")
	b.WriteString("    labels:\n")
	b.WriteString("      # ddash never stops or recreates its own Agent without an override.\n")
	b.WriteString("      " + agent.LabelProtected + ": \"true\"\n")
	b.WriteString("volumes:\n")
	b.WriteString("  state:\n")
	return &pb.AgentSetup{
		ComposeFile:   b.String(),
		EnrolmentCode: r.code,
		Image:         image(release),
		Commands: []string{
			"mkdir -p " + agentDir + " && cd " + agentDir,
			"# save the file above as compose.yaml there, then:",
			"docker compose up -d",
		},
	}
}

// pairPending tries every enrolling Agent once.
func (c *component) pairPending(ctx context.Context) {
	rows, err := c.rows(ctx)
	if err != nil {
		c.log.Warn("can't read the Agents", "err", err)
		return
	}
	for _, r := range rows {
		if r.token == "" && r.code != "" {
			c.pair(ctx, r)
		}
	}
}

// pair pairs with r's Agent, keeping why it failed for ListAgents.
func (c *component) pair(ctx context.Context, r row) {
	c.pairing.Lock()
	defer c.pairing.Unlock()
	// What the caller read may be stale: another pairing may have won.
	fresh, err := c.row(ctx, r.name)
	if err != nil || fresh.token != "" || fresh.code == "" {
		return
	}
	r = fresh
	err = func() error {
		ctx, cancel := context.WithTimeout(ctx, readTimeout)
		defer cancel()
		token, fp, err := agentcall.Pairing{URL: r.url, Code: r.code, Kind: agent.KindDocker, KindName: "Docker Agent", IfPaired: "remove its state volume and start it again"}.Pair(ctx)
		if err != nil {
			return err
		}
		_, err = c.db.ExecContext(ctx, `UPDATE agent SET token = ?, fingerprint = ?, code = '', paired = ? WHERE name = ? AND token = ''`,
			token, fp, time.Now().UTC().Format(time.RFC3339), r.name)
		return err
	}()
	c.mu.Lock()
	if err != nil {
		c.pairErr[r.name] = err.Error()
	} else {
		delete(c.pairErr, r.name)
		c.log.Info("paired with a Docker Agent", "agent", r.name)
	}
	c.mu.Unlock()
	c.reads.Invalidate()
}

func (c *component) PairAgent(ctx context.Context, req *pb.PairAgentRequest) (*pb.PairAgentResponse, error) {
	r, err := c.row(ctx, req.GetName())
	if err != nil {
		return nil, err
	}
	if req.GetReset_() {
		r.code, r.token, r.fingerprint = agent.NewCode(), "", ""
		if _, err := c.db.ExecContext(ctx, `UPDATE agent SET code = ?, token = '', fingerprint = '', paired = '' WHERE name = ?`, r.code, r.name); err != nil {
			return nil, err
		}
	}
	if r.token != "" {
		return nil, status.Errorf(codes.FailedPrecondition, "the Docker Agent %s is already paired", r.name)
	}
	c.pair(ctx, r)
	rs, err := c.reads.Get(ctx)
	if err != nil {
		return nil, err
	}
	for _, x := range rs {
		if x.name == r.name {
			return &pb.PairAgentResponse{Agent: x.proto()}, nil
		}
	}
	return nil, status.Errorf(codes.NotFound, "no Docker Agent %q", r.name)
}

func (c *component) RemoveAgent(ctx context.Context, req *pb.RemoveAgentRequest) (*pb.RemoveAgentResponse, error) {
	if _, err := c.row(ctx, req.GetName()); err != nil {
		return nil, err
	}
	if _, err := c.db.ExecContext(ctx, `DELETE FROM agent WHERE name = ?`, req.GetName()); err != nil {
		return nil, err
	}
	c.reads.Invalidate()
	return &pb.RemoveAgentResponse{Uninstall: []string{
		"cd " + agentDir,
		"docker compose down -v",
		"cd / && rm -rf " + agentDir,
	}}, nil
}

// paired returns host's Agent ready for operations: paired, reachable,
// on ddash's Release.
func (c *component) paired(ctx context.Context, host string) (*agent.Client, row, error) {
	rs, err := c.reads.Get(ctx)
	if err != nil {
		return nil, row{}, err
	}
	for _, r := range rs {
		if r.name != host {
			continue
		}
		switch r.state {
		case pb.AgentState_AGENT_STATE_PAIRED:
			if r.message != "" {
				return nil, row{}, status.Errorf(codes.Unavailable, "the Docker Agent on %s: %s", host, r.message)
			}
			return r.client(), r.row, nil
		case pb.AgentState_AGENT_STATE_ENROLLING:
			return nil, row{}, status.Errorf(codes.FailedPrecondition, "the Docker Agent on %s isn't paired yet", host)
		case pb.AgentState_AGENT_STATE_UNREACHABLE:
			return nil, row{}, status.Errorf(codes.Unavailable, "the Docker Agent on %s is %s", host, r.message)
		default:
			return nil, row{}, status.Errorf(codes.FailedPrecondition, "the Docker Agent on %s: %s", host, r.message)
		}
	}
	return nil, row{}, status.Errorf(codes.NotFound, "no Docker Agent %q", host)
}

func (c *component) UpdateAgent(ctx context.Context, req *pb.UpdateAgentRequest) (*pb.UpdateAgentResponse, error) {
	r, err := c.row(ctx, req.GetName())
	if err != nil {
		return nil, err
	}
	if r.token == "" {
		return nil, status.Errorf(codes.FailedPrecondition, "the Docker Agent %s isn't paired yet", r.name)
	}
	var ref agent.JobRef
	if err := r.client().Post(ctx, agent.PathUpdate, agent.UpdateRequest{Release: c.release, Image: image(c.release)}, &ref); err != nil {
		return nil, agentErr(err)
	}
	c.reads.Invalidate()
	return &pb.UpdateAgentResponse{Job: jobproto.Of(ref.Job, r.name)}, nil
}

// agentErr is an Agent's refusal as a gRPC error.
func agentErr(err error) error {
	return agentcall.Status(err, "the Docker Agent", codes.Aborted)
}

func (c *component) ListContainers(ctx context.Context, _ *pb.ListContainersRequest) (*pb.ListContainersResponse, error) {
	rs, err := c.reads.Get(ctx)
	if err != nil {
		return nil, err
	}
	out := &pb.ListContainersResponse{}
	for _, r := range rs {
		if r.state == pb.AgentState_AGENT_STATE_ENROLLING {
			continue
		}
		h := &pb.HostContainers{Host: r.name, Address: r.address(), Error: r.message}
		for _, ct := range r.projects.Containers {
			h.Containers = append(h.Containers, containerProto(ct))
		}
		out.Hosts = append(out.Hosts, h)
	}
	return out, nil
}

func containerProto(c agent.Container) *containerpb.Container {
	out := &containerpb.Container{
		Id: c.ID, Name: c.Name, Image: c.Image, State: dockerread.State(c.State), Status: c.Status, ImageId: c.ImageID,
		RepoDigests: c.RepoDigests, Labels: c.Labels, Created: timestamppb.New(c.Created),
		Health: dockerread.Health(c.Status), Protected: dockerread.Protected(c.Labels),
	}
	if c.Labels[agent.LabelOneOff] != "True" {
		out.Project, out.Service = c.Labels[agent.LabelProject], c.Labels[agent.LabelService]
	}
	for _, p := range c.Ports {
		out.Ports = append(out.Ports, &containerpb.Port{Ip: p.IP, PublicPort: uint32(p.PublicPort), PrivatePort: uint32(p.PrivatePort), Protocol: dockerread.Protocol(p.Protocol)})
	}
	return out
}

func (c *component) ListComposeProjects(ctx context.Context, _ *pb.ListComposeProjectsRequest) (*pb.ListComposeProjectsResponse, error) {
	rs, err := c.reads.Get(ctx)
	if err != nil {
		return nil, err
	}
	out := &pb.ListComposeProjectsResponse{}
	for _, r := range rs {
		if r.state == pb.AgentState_AGENT_STATE_ENROLLING {
			continue
		}
		h := &pb.HostProjects{Host: r.name, Address: r.address(), Roots: r.roots}
		if len(r.info.Roots) > 0 {
			h.Roots = r.info.Roots
		}
		h.Error = r.message
		for _, p := range r.projects.Projects {
			pp := &pb.Project{Name: p.Name, WorkingDir: p.WorkingDir, ConfigFiles: p.ConfigFiles, Found: p.Found, Error: p.Error, Self: p.Self}
			for _, s := range p.Services {
				pp.Services = append(pp.Services, &pb.Service{Name: s.Name, Image: s.Image, Ports: s.Ports, ContainerIds: s.Containers})
			}
			h.Projects = append(h.Projects, pp)
		}
		for _, ct := range r.projects.Containers {
			h.Containers = append(h.Containers, containerProto(ct))
		}
		out.Hosts = append(out.Hosts, h)
	}
	return out, nil
}

func (c *component) GetComposeFile(ctx context.Context, req *pb.GetComposeFileRequest) (*pb.GetComposeFileResponse, error) {
	cl, _, err := c.paired(ctx, req.GetHost())
	if err != nil {
		return nil, err
	}
	var f agent.File
	if err := cl.Get(ctx, agent.PathFile, url.Values{"path": {req.GetPath()}}, &f); err != nil {
		return nil, agentErr(err)
	}
	out := &pb.GetComposeFileResponse{File: &pb.ComposeFile{Path: f.Path, Content: f.Content, Sha256: f.SHA256}}
	if f.Env != nil {
		out.Env = &pb.ComposeFile{Path: f.Env.Path, Content: f.Env.Content, Sha256: f.Env.SHA256}
	}
	for _, d := range f.Dockerfiles {
		out.Dockerfiles = append(out.Dockerfiles, &pb.Dockerfile{File: &pb.ComposeFile{Path: d.Path, Content: d.Content, Sha256: d.SHA256}, Services: d.Services})
	}
	return out, nil
}

func (c *component) ValidateComposeFile(ctx context.Context, req *pb.ValidateComposeFileRequest) (*pb.ValidateComposeFileResponse, error) {
	cl, _, err := c.paired(ctx, req.GetHost())
	if err != nil {
		return nil, err
	}
	var v agent.ValidateResponse
	if err := cl.Post(ctx, agent.PathValidate, agent.ValidateRequest{Path: req.GetPath(), Content: req.GetContent(), Project: req.GetProject(), Env: req.GetEnv()}, &v); err != nil {
		return nil, agentErr(err)
	}
	return &pb.ValidateComposeFileResponse{Ok: v.OK, Error: v.Error}, nil
}

func (c *component) WriteComposeFile(ctx context.Context, req *pb.WriteComposeFileRequest) (*pb.WriteComposeFileResponse, error) {
	cl, _, err := c.paired(ctx, req.GetHost())
	if err != nil {
		return nil, err
	}
	defer c.reads.Invalidate()
	var f agent.File
	if err := cl.Post(ctx, agent.PathWrite, agent.WriteRequest{Path: req.GetPath(), Content: req.GetContent(), ExpectSHA256: req.GetExpectSha256(), Project: req.GetProject(), File: req.GetComposeFile()}, &f); err != nil {
		return nil, agentErr(err)
	}
	return &pb.WriteComposeFileResponse{File: &pb.ComposeFile{Path: f.Path, Content: f.Content, Sha256: f.SHA256}}, nil
}

func (c *component) CreateComposeProject(ctx context.Context, req *pb.CreateComposeProjectRequest) (*pb.CreateComposeProjectResponse, error) {
	cl, _, err := c.paired(ctx, req.GetHost())
	if err != nil {
		return nil, err
	}
	defer c.reads.Invalidate()
	var f agent.File
	if err := cl.Post(ctx, agent.PathCreate, agent.CreateRequest{Name: req.GetName(), Directory: req.GetDirectory(), Content: req.GetContent(), Env: req.GetEnv()}, &f); err != nil {
		if agent.CodeOf(err) == agent.CodeConflict {
			return nil, status.Error(codes.AlreadyExists, err.Error())
		}
		return nil, agentErr(err)
	}
	out := &pb.CreateComposeProjectResponse{File: &pb.ComposeFile{Path: f.Path, Content: f.Content, Sha256: f.SHA256}}
	if f.Env != nil {
		out.Env = &pb.ComposeFile{Path: f.Env.Path, Content: f.Env.Content, Sha256: f.Env.SHA256}
	}
	return out, nil
}

// compose starts a Compose job on the target's Agent.
func (c *component) compose(ctx context.Context, op agent.ComposeOp, t *pb.ComposeTarget, pull, wait, build bool) (*agent.Job, string, error) {
	cl, r, err := c.paired(ctx, t.GetHost())
	if err != nil {
		return nil, "", err
	}
	defer c.reads.Invalidate()
	var ref agent.JobRef
	err = cl.Post(ctx, agent.PathCompose, agent.ComposeRequest{
		Op: op, Project: t.GetProject(), WorkingDir: t.GetWorkingDir(), File: t.GetFile(),
		Services: t.GetServices(), Pull: pull, Wait: wait, Build: build,
	}, &ref)
	if err != nil {
		return nil, "", agentErr(err)
	}
	return &ref.Job, r.name, nil
}

func (c *component) UpProject(ctx context.Context, req *pb.UpProjectRequest) (*pb.UpProjectResponse, error) {
	j, host, err := c.compose(ctx, agent.Up, req.GetTarget(), req.GetPull(), req.GetWait(), req.GetBuild())
	if err != nil {
		return nil, err
	}
	return &pb.UpProjectResponse{Job: jobproto.Of(*j, host)}, nil
}

func (c *component) DownProject(ctx context.Context, req *pb.DownProjectRequest) (*pb.DownProjectResponse, error) {
	j, host, err := c.compose(ctx, agent.Down, req.GetTarget(), false, false, false)
	if err != nil {
		return nil, err
	}
	return &pb.DownProjectResponse{Job: jobproto.Of(*j, host)}, nil
}

func (c *component) RestartProject(ctx context.Context, req *pb.RestartProjectRequest) (*pb.RestartProjectResponse, error) {
	j, host, err := c.compose(ctx, agent.Restart, req.GetTarget(), false, false, false)
	if err != nil {
		return nil, err
	}
	return &pb.RestartProjectResponse{Job: jobproto.Of(*j, host)}, nil
}

func (c *component) PullProject(ctx context.Context, req *pb.PullProjectRequest) (*pb.PullProjectResponse, error) {
	j, host, err := c.compose(ctx, agent.Pull, req.GetTarget(), false, false, false)
	if err != nil {
		return nil, err
	}
	return &pb.PullProjectResponse{Job: jobproto.Of(*j, host)}, nil
}

func (c *component) UpdateProject(ctx context.Context, req *pb.UpdateProjectRequest) (*pb.UpdateProjectResponse, error) {
	j, host, err := c.compose(ctx, agent.Update, req.GetTarget(), false, false, false)
	if err != nil {
		return nil, err
	}
	return &pb.UpdateProjectResponse{Job: jobproto.Of(*j, host)}, nil
}

func (c *component) ListJobs(ctx context.Context, _ *pb.ListJobsRequest) (*pb.ListJobsResponse, error) {
	rows, err := c.rows(ctx)
	if err != nil {
		return nil, err
	}
	out := &pb.ListJobsResponse{}
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, r := range rows {
		if r.token == "" {
			continue
		}
		wg.Go(func() {
			ctx, cancel := context.WithTimeout(ctx, readTimeout)
			defer cancel()
			h := &pb.HostJobs{Host: r.name}
			var jobs []agent.Job
			if err := r.client().Get(ctx, agent.PathJobs, nil, &jobs); err != nil {
				_, h.Error = stateOf(err)
			}
			for _, j := range jobs {
				h.Jobs = append(h.Jobs, jobproto.Of(j, r.name))
			}
			mu.Lock()
			out.Hosts = append(out.Hosts, h)
			mu.Unlock()
		})
	}
	wg.Wait()
	slices.SortFunc(out.Hosts, func(a, b *pb.HostJobs) int { return strings.Compare(a.GetHost(), b.GetHost()) })
	return out, nil
}

func (c *component) GetJob(ctx context.Context, req *pb.GetJobRequest) (*pb.GetJobResponse, error) {
	r, err := c.row(ctx, req.GetHost())
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, readTimeout)
	defer cancel()
	j, err := agentcall.Job(ctx, r.client(), req.GetId())
	if err != nil {
		return nil, agentErr(err)
	}
	return &pb.GetJobResponse{Job: jobproto.Of(j, r.name)}, nil
}

func (c *component) CollectJob(ctx context.Context, req *pb.CollectJobRequest) (*pb.CollectJobResponse, error) {
	r, err := c.row(ctx, req.GetHost())
	if err != nil {
		return nil, err
	}
	j, failed, err := agentcall.Collect(ctx, r.client(), req.GetId())
	if err != nil {
		return nil, agentErr(err)
	}
	c.reads.Invalidate()
	if failed != nil {
		return nil, failed
	}
	return &pb.CollectJobResponse{Job: jobproto.Of(j, r.name)}, nil
}

func (c *component) CancelJob(ctx context.Context, req *pb.CancelJobRequest) (*pb.CancelJobResponse, error) {
	// An Agent on another Release is refused here, asking for its update.
	client, r, err := c.paired(ctx, req.GetHost())
	if err != nil {
		return nil, err
	}
	j, err := agentcall.Cancel(ctx, client, req.GetId(), "the Docker Agent on "+r.name)
	if err != nil {
		return nil, err
	}
	return &pb.CancelJobResponse{Job: jobproto.Of(j, r.name)}, nil
}
