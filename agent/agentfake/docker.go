// Package agentfake is a stateful fake Docker Agent and Host Agent: the
// real Agent servers and operations (pairing, TLS, jobs, file writes,
// sshd settings) over a fake Docker Engine and a fake host, so tests and
// `make dev` drive exactly the code a real host runs. Every Compose and
// host command it receives is recorded in order, a step can be made to
// fail, and each step takes StepDelay so progress shows on screen.
package agentfake

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/itsKontra/ddash/agent"
	"github.com/itsKontra/ddash/agent/dockeragent"
)

// Options configure a fake Agent.
type Options struct {
	// StateDir keeps the Agent's certificate and pairing, so its
	// fingerprint survives restarts; empty: a temp dir.
	StateDir string
	// Release is what the Agent reports; empty: agent.Release.
	Release string
	// StepDelay is how long each Compose or package step takes.
	StepDelay time.Duration
	// NoCancel answers a job's cancel as an Agent from before cancel
	// existed does: 404 page not found.
	NoCancel bool
}

// Docker is a fake Docker Agent: a Compose root seeded from stacks, and
// an Engine whose containers follow the Compose commands it gets.
type Docker struct {
	*Engine
	*server
	// Root is the Compose root, a real directory.
	Root string
}

// Engine is the fake Docker Engine and Compose CLI.
type Engine struct {
	delay time.Duration

	mu         sync.Mutex
	containers []agent.Container
	// images maps a local image ref to its id; registry maps it to the id
	// a pull fetches.
	images   map[string]string
	registry map[string]string
	calls    []string
	fail     map[string]string
	hold     map[string]chan struct{}
	seq      int
}

// Image is one image the fake registry holds.
type Image struct {
	Ref string
	// ID is the local image's id; Latest the one a pull fetches (empty:
	// the same).
	ID, Latest string
}

// Stack is a Compose Project to seed: its directory under the root, its
// file, and whether it runs.
type Stack struct {
	Dir     string
	Compose string
	Env     string
	// Dockerfile, when set, is written as Dockerfile beside compose.yaml.
	Dockerfile string
	Running    bool
	// Exited are the Compose services of a running Stack whose container
	// has since exited, each with its exit code (137: killed, e.g. out of
	// memory).
	Exited map[string]int
}

// Loose is a container started without Compose.
type Loose struct {
	Name, Image string
	Ports       []agent.Port
}

// DockerSeed is a fake Docker host's starting state.
type DockerSeed struct {
	Stacks []Stack
	Loose  []Loose
	Images []Image
	// Agent runs the Docker Agent's own container, as docs/deployment.md
	// installs it: Compose Project ddash-docker-agent in
	// /opt/ddash-docker-agent, outside the root, Protected.
	Agent bool
}

// The Docker Agent's own container, when DockerSeed.Agent runs it.
const (
	AgentProject   = "ddash-docker-agent"
	AgentContainer = "ddash-docker-agent-agent-1"
	agentDir       = "/opt/ddash-docker-agent"
)

// NewDocker starts a fake Docker Agent at root (a temp dir when empty)
// with seed, unpaired, accepting any enrolment code.
func NewDocker(root string, seed DockerSeed, o Options) (*Docker, error) {
	if root == "" {
		var err error
		if root, err = os.MkdirTemp("", "ddash-docker-agent-"); err != nil {
			return nil, err
		}
	}
	e := &Engine{delay: o.StepDelay, images: map[string]string{}, registry: map[string]string{}, fail: map[string]string{}, hold: map[string]chan struct{}{}}
	for _, img := range seed.Images {
		e.images[img.Ref] = img.ID
		e.registry[img.Ref] = cmpOr(img.Latest, img.ID)
	}
	d := &Docker{Engine: e, Root: root}
	for _, s := range seed.Stacks {
		dir := filepath.Join(root, s.Dir)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, err
		}
		file := filepath.Join(dir, "compose.yaml")
		if _, err := os.Stat(file); errors.Is(err, fs.ErrNotExist) {
			if err := os.WriteFile(file, []byte(s.Compose), 0o644); err != nil {
				return nil, err
			}
			if s.Env != "" {
				if err := os.WriteFile(filepath.Join(dir, ".env"), []byte(s.Env), 0o600); err != nil {
					return nil, err
				}
			}
			if s.Dockerfile != "" {
				if err := os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte(s.Dockerfile), 0o644); err != nil {
					return nil, err
				}
			}
		}
		if s.Running {
			b, _ := os.ReadFile(file)
			if err := e.up(filepath.Base(dir), dir, file, b, nil); err != nil {
				return nil, fmt.Errorf("seed %s: %w", s.Dir, err)
			}
			for i, c := range e.containers {
				if code, ok := s.Exited[c.Labels[agent.LabelService]]; ok && c.Labels[agent.LabelProject] == filepath.Base(dir) {
					e.containers[i].State, e.containers[i].Status = "exited", fmt.Sprintf("Exited (%d) 6 minutes ago", code)
				}
			}
		}
	}
	for _, l := range seed.Loose {
		e.containers = append(e.containers, agent.Container{
			ID: e.id(l.Name), Name: l.Name, Image: l.Image, ImageID: e.images[l.Image], State: "running",
			Status: "Up 3 days", Ports: l.Ports, Created: time.Now().Add(-72 * time.Hour).UTC(),
			RepoDigests: e.digests(l.Image), Labels: map[string]string{},
		})
	}
	release := cmpOr(o.Release, agent.Release)
	var self string
	if seed.Agent {
		self = AgentContainer
		image := "ghcr.io/itskontra/ddash-docker-agent:" + release
		e.containers = append(e.containers, agent.Container{
			ID: e.id(AgentContainer), Name: AgentContainer, Image: image, State: "running", Status: "Up 3 days",
			Ports: []agent.Port{{PublicPort: agent.DockerPort, PrivatePort: agent.DockerPort, Protocol: "tcp"}}, Created: time.Now().Add(-72 * time.Hour).UTC(),
			Labels: map[string]string{
				agent.LabelProject: AgentProject, agent.LabelService: "agent",
				agent.LabelWorkingDir: agentDir, agent.LabelConfigFiles: agentDir + "/compose.yaml",
				agent.LabelProtected: "true",
			},
		})
	}
	srv, err := newServer(agent.KindDocker, o, func(s *agent.Server) {
		ops, _ := dockeragent.New(dockeragent.Options{Roots: []string{root}, Docker: e, Compose: e, Self: self})
		ops.Register(s, func(context.Context) agent.Info {
			return agent.Info{Kind: agent.KindDocker, Release: release, Hostname: "docker-fake", Arch: "amd64"}
		})
	})
	if err != nil {
		return nil, err
	}
	d.server = srv
	return d, nil
}

func cmpOr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// --- the fake Engine (dockeragent.Docker) ---

func (e *Engine) id(name string) string {
	e.seq++
	sum := sha256.Sum256([]byte(name + strconv.Itoa(e.seq)))
	return hex.EncodeToString(sum[:])
}

// digests is ref's repo digest as a pull by tag leaves it; none for an
// image without a registry (built locally).
func (e *Engine) digests(ref string) []string {
	id, ok := e.images[ref]
	if !ok || strings.HasPrefix(id, "local:") {
		return nil
	}
	repo, _, _ := strings.Cut(ref, ":")
	return []string{repo + "@" + digestFor(id)}
}

// digestFor is the registry digest the fake gives an image id.
func digestFor(id string) string {
	sum := sha256.Sum256([]byte("manifest:" + id))
	return "sha256:" + hex.EncodeToString(sum[:])
}

// RegistryDigest is the digest the fake registry serves for ref now: what
// a registry check should find. ok is false for an image it doesn't have.
func (e *Engine) RegistryDigest(ref string) (string, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	id, ok := e.registry[ref]
	if !ok || strings.HasPrefix(id, "local:") {
		return "", false
	}
	return digestFor(id), true
}

// Publish makes the fake registry serve a new image for ref, so the next
// check finds it stale and the next pull fetches it.
func (e *Engine) Publish(ref, id string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.registry[ref] = id
}

// FailNext makes the next Compose command named cmd ("pull", "up", …)
// fail with message.
func (e *Engine) FailNext(cmd, message string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.fail[cmd] = message
}

// HoldNext makes the next Compose command named cmd run until it is
// stopped, as a pull of a large image runs on: it ends only when its job
// is cancelled or times out, changing nothing. The channel closes once it
// runs.
func (e *Engine) HoldNext(cmd string) <-chan struct{} {
	e.mu.Lock()
	defer e.mu.Unlock()
	running := make(chan struct{})
	e.hold[cmd] = running
	return running
}

// Calls returns every Compose command received, in order.
func (e *Engine) Calls() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return slices.Clone(e.calls)
}

// State returns a container's state by name ("" when it doesn't exist).
func (e *Engine) State(name string) string {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, c := range e.containers {
		if c.Name == name {
			return c.State
		}
	}
	return ""
}

func (e *Engine) Containers(context.Context) ([]agent.Container, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := slices.Clone(e.containers)
	for i := range out {
		out[i].Labels = clone(out[i].Labels)
		out[i].RepoDigests = slices.Clone(out[i].RepoDigests)
	}
	return out, nil
}

func clone(m map[string]string) map[string]string {
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func (e *Engine) ImageDigests(_ context.Context, ref string) (string, []string, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	id, ok := e.images[ref]
	if !ok {
		return "", nil, fmt.Errorf("no such image: %s", ref)
	}
	return id, e.digests(ref), nil
}

func (e *Engine) Version(context.Context) (string, error) { return "27.5.1", nil }

// composeFile is what the fake reads of a Compose file.
type composeFile struct {
	Name     string `yaml:"name"`
	Services map[string]struct {
		Image  string `yaml:"image"`
		Build  any    `yaml:"build"`
		Ports  []any  `yaml:"ports"`
		Labels any    `yaml:"labels"`
	} `yaml:"services"`
}

func parse(b []byte) (composeFile, error) {
	var f composeFile
	if err := yaml.Unmarshal(b, &f); err != nil {
		return f, fmt.Errorf("yaml: %v", err)
	}
	if len(f.Services) == 0 {
		return f, errors.New("no service defined in the Compose file")
	}
	for name, s := range f.Services {
		if s.Image == "" && s.Build == nil {
			return f, fmt.Errorf("service %q has neither an image nor a build context specified: invalid compose project", name)
		}
	}
	return f, nil
}

// Run is the fake `docker compose`.
func (e *Engine) Run(ctx context.Context, _ string, args []string, out func(string)) error {
	if out == nil {
		out = func(string) {}
	}
	if len(args) > 0 && args[0] == "--docker" {
		e.record("docker " + strings.Join(args[1:], " "))
		return nil
	}
	flags := map[string]string{}
	i := 0
	for i < len(args) && strings.HasPrefix(args[i], "-") {
		flags[args[i]] = args[i+1]
		i += 2
	}
	if i >= len(args) {
		return errors.New("no command")
	}
	cmd, rest := args[i], args[i+1:]
	e.record(cmd + " " + flags["-p"] + " " + strings.Join(rest, " "))
	if cmd == "version" {
		out("2.32.4")
		return nil
	}
	e.mu.Lock()
	msg, failing := e.fail[cmd]
	delete(e.fail, cmd)
	held, holding := e.hold[cmd]
	delete(e.hold, cmd)
	e.mu.Unlock()
	if holding {
		out(" Pulling fs layer")
		close(held)
		<-ctx.Done()
		return errors.New("signal: killed") // as exec reports a command killed with its ctx
	}
	if failing {
		e.sleep(ctx)
		out(msg)
		return errors.New("exit status 1")
	}
	b, err := os.ReadFile(flags["-f"])
	if err != nil {
		out("open " + flags["-f"] + ": no such file or directory")
		return err
	}
	f, err := parse(b)
	if err != nil {
		out(fmt.Sprintf("validating %s: %v", flags["-f"], err))
		return errors.New("exit status 15")
	}
	project, dir := flags["-p"], flags["--project-directory"]
	var services []string
	for _, a := range rest {
		if !strings.HasPrefix(a, "-") && a != "always" && a != "60" {
			services = append(services, a)
		}
	}
	switch cmd {
	case "config":
		return nil
	case "pull":
		for _, name := range sortedServices(f) {
			s := f.Services[name]
			if (len(services) > 0 && !slices.Contains(services, name)) || s.Image == "" {
				continue
			}
			out(fmt.Sprintf(" %s Pulling", name))
			e.sleep(ctx)
			e.mu.Lock()
			latest, ok := e.registry[s.Image]
			if ok && !strings.HasPrefix(latest, "local:") {
				e.images[s.Image] = latest
			}
			e.mu.Unlock()
			out(fmt.Sprintf(" %s Pulled", name))
		}
		return nil
	case "up":
		e.sleep(ctx)
		if slices.Contains(rest, "--build") {
			e.build(project, f, services)
		}
		err := e.up(project, dir, flags["-f"], b, services)
		for _, name := range sortedServices(f) {
			if len(services) == 0 || slices.Contains(services, name) {
				out(fmt.Sprintf(" Container %s-%s-1  Started", project, name))
			}
		}
		return err
	case "down", "stop":
		e.sleep(ctx)
		e.mu.Lock()
		defer e.mu.Unlock()
		kept := e.containers[:0]
		for _, c := range e.containers {
			mine := c.Labels[agent.LabelProject] == project
			if mine && cmd == "down" {
				out(" Container " + c.Name + "  Removed")
				continue
			}
			if mine && slices.Contains(services, c.Labels[agent.LabelService]) {
				c.State, c.Status = "exited", "Exited (0) Less than a second ago"
				out(" Container " + c.Name + "  Stopped")
			}
			kept = append(kept, c)
		}
		e.containers = kept
		return nil
	case "restart":
		e.sleep(ctx)
		e.mu.Lock()
		defer e.mu.Unlock()
		for i, c := range e.containers {
			if c.Labels[agent.LabelProject] == project && (len(services) == 0 || slices.Contains(services, c.Labels[agent.LabelService])) {
				e.containers[i].State, e.containers[i].Status = "running", "Up Less than a second"
				out(" Container " + c.Name + "  Started")
			}
		}
		return nil
	}
	return fmt.Errorf("the fake doesn't do `docker compose %s`", cmd)
}

func sortedServices(f composeFile) []string {
	var names []string
	for n := range f.Services {
		names = append(names, n)
	}
	slices.Sort(names)
	return names
}

// build gives each of services (all when empty) that builds its image a
// new local image, as `up --build` does after a change: up recreates it.
func (e *Engine) build(project string, f composeFile, services []string) {
	if f.Name != "" {
		project = f.Name
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, name := range sortedServices(f) {
		s := f.Services[name]
		if s.Build == nil || (len(services) > 0 && !slices.Contains(services, name)) {
			continue
		}
		image := cmpOr(s.Image, project+"-"+name)
		e.images[image] = fmt.Sprintf("local:%s@%d", image, len(e.calls))
	}
}

// up (re)creates the project's containers for services (all when empty):
// a container whose image changed is recreated, others started.
func (e *Engine) up(project, dir, file string, b []byte, services []string) error {
	f, err := parse(b)
	if err != nil {
		return err
	}
	if f.Name != "" {
		project = f.Name
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, name := range sortedServices(f) {
		if len(services) > 0 && !slices.Contains(services, name) {
			continue
		}
		s := f.Services[name]
		image := s.Image
		if image == "" {
			image = project + "-" + name
		}
		id, ok := e.images[image]
		if !ok {
			if latest, inRegistry := e.registry[image]; inRegistry {
				id = latest
			} else {
				id = "local:" + image
			}
			e.images[image] = id
		}
		labels := map[string]string{
			agent.LabelProject: project, agent.LabelService: name,
			agent.LabelWorkingDir: dir, agent.LabelConfigFiles: file,
		}
		for k, v := range labelsOf(s.Labels) {
			labels[k] = v
		}
		c := agent.Container{
			Name: fmt.Sprintf("%s-%s-1", project, name), Image: image, ImageID: id, State: "running",
			Status: "Up Less than a second", Labels: labels, Ports: portsOf(s.Ports), Created: time.Now().UTC(),
			RepoDigests: e.digests(image),
		}
		i := slices.IndexFunc(e.containers, func(x agent.Container) bool { return x.Name == c.Name })
		switch {
		case i < 0:
			c.ID = e.id(c.Name)
			e.containers = append(e.containers, c)
		case e.containers[i].ImageID != id || e.containers[i].State != "running":
			c.ID = e.id(c.Name)
			e.containers[i] = c
		}
	}
	return nil
}

func labelsOf(v any) map[string]string {
	out := map[string]string{}
	switch l := v.(type) {
	case map[string]any:
		for k, v := range l {
			out[k] = fmt.Sprint(v)
		}
	case []any:
		for _, s := range l {
			k, v, _ := strings.Cut(fmt.Sprint(s), "=")
			out[k] = v
		}
	}
	return out
}

func portsOf(ports []any) []agent.Port {
	var out []agent.Port
	for _, p := range ports {
		s := fmt.Sprint(p)
		s, proto, _ := strings.Cut(s, "/")
		parts := strings.Split(s, ":")
		if len(parts) < 2 {
			continue
		}
		pub, _ := strconv.Atoi(parts[len(parts)-2])
		priv, _ := strconv.Atoi(parts[len(parts)-1])
		port := agent.Port{PublicPort: uint16(pub), PrivatePort: uint16(priv), Protocol: cmpOr(proto, "tcp")}
		if len(parts) == 3 {
			port.IP = parts[0]
		}
		out = append(out, port)
	}
	return out
}

func (e *Engine) record(call string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.calls = append(e.calls, strings.TrimSpace(call))
}

func (e *Engine) sleep(ctx context.Context) {
	select {
	case <-ctx.Done():
	case <-time.After(e.delay):
	}
}

// --- shared: the server, pairing with any code ---

// server is an Agent server that accepts any enrolment code: the fake
// stands in for the Operator copying ddash's code to the host. A pairing
// request while paired starts over (a new server over the same
// certificate), so a ddash whose DB was reset can pair again.
type server struct {
	o        Options
	kind     agent.Kind
	register func(*agent.Server)

	mu  sync.Mutex
	cur *agent.Server
}

func newServer(kind agent.Kind, o Options, register func(*agent.Server)) (*server, error) {
	if o.StateDir == "" {
		dir, err := os.MkdirTemp("", "ddash-agent-state-")
		if err != nil {
			return nil, err
		}
		o.StateDir = dir
	}
	if o.Release == "" {
		o.Release = agent.Release
	}
	s := &server{o: o, kind: kind, register: register}
	return s, s.start()
}

// fakeCode is the enrolment code the fake's server holds; a pairing
// request's code is rewritten to it.
const fakeCode = "FAKE-AGNT-CODE-0000"

func (s *server) start() error {
	srv, err := agent.NewServer(agent.ServerOptions{Kind: s.kind, Release: s.o.Release, StateDir: s.o.StateDir, EnrolmentCode: fakeCode})
	if err != nil {
		return err
	}
	s.register(srv)
	s.mu.Lock()
	s.cur = srv
	s.mu.Unlock()
	return nil
}

func (s *server) current() *agent.Server {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cur
}

func (s *server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if s.o.NoCancel && r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, agent.PathJobs+"/") && strings.HasSuffix(r.URL.Path, "/cancel") {
		http.NotFound(w, r)
		return
	}
	if r.Method == http.MethodPost && r.URL.Path == agent.PathPair {
		var req agent.PairRequest
		b, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		if json.Unmarshal(b, &req) == nil {
			req.Code = fakeCode
			b, _ = json.Marshal(req)
			if s.current().Paired() {
				if err := os.Remove(filepath.Join(s.o.StateDir, "state.json")); err != nil && !errors.Is(err, fs.ErrNotExist) {
					agent.WriteError(w, http.StatusInternalServerError, agent.CodeFailed, err.Error())
					return
				}
				if err := s.start(); err != nil {
					agent.WriteError(w, http.StatusInternalServerError, agent.CodeFailed, err.Error())
					return
				}
			}
		}
		r.Body = io.NopCloser(bytes.NewReader(b))
	}
	s.current().Handler().ServeHTTP(w, r)
}

// Unpair forgets the fake's pairing, keeping its certificate, as if its
// state had been reset: a ddash that never paired with it can now.
func (s *server) Unpair() error {
	if err := os.Remove(filepath.Join(s.o.StateDir, "state.json")); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return s.start()
}

// Fingerprint is the fake Agent's certificate's.
func (s *server) Fingerprint() string { return s.current().Fingerprint() }

// Paired reports whether a ddash paired with it.
func (s *server) Paired() bool { return s.current().Paired() }

// Serve serves the fake over TLS on addr ("127.0.0.1:0"), returning its
// URL and a stop func.
func (s *server) Serve(addr string) (string, func(), error) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return "", nil, err
	}
	srv := &http.Server{Handler: s, TLSConfig: s.current().TLSConfig(), ReadHeaderTimeout: 10 * time.Second}
	go srv.ServeTLS(ln, "", "") //nolint:errcheck // ends with stop
	return "https://" + ln.Addr().String(), func() { srv.Close() }, nil
}
