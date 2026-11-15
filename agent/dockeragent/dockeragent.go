// Package dockeragent is the Docker Agent's operations: it lists the
// host's containers and Compose Projects, reads, validates and writes
// Compose files under the mounted Compose roots, and runs Compose
// operations as jobs. It reaches Docker through the socket the Agent's
// container mounts, and runs `docker compose` (the CLI plugin in its image)
// with each root mounted at the same path as on the host, so relative bind
// mounts and .env files resolve exactly as they would there.
package dockeragent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"

	"go.yaml.in/yaml/v3"

	"github.com/itsKontra/ddash/agent"
)

// Docker is what the Agent reads from the Docker Engine.
type Docker interface {
	// Containers lists every container, with its image's repo digests.
	Containers(ctx context.Context) ([]agent.Container, error)
	// ImageDigests returns the local image ref's id and repo digests.
	ImageDigests(ctx context.Context, ref string) (id string, digests []string, err error)
	// Version is the Engine's.
	Version(ctx context.Context) (string, error)
}

// Compose runs `docker compose args...` in dir, passing each line of its
// output to out.
type Compose interface {
	Run(ctx context.Context, dir string, args []string, out func(line string)) error
}

// Options configure the Docker Agent.
type Options struct {
	// Roots are the Compose roots, absolute, mounted at the same path.
	Roots   []string
	Docker  Docker
	Compose Compose
	// Self is the Agent's own container name or id, for Update Agent.
	Self string
	// ReadFile reads a Compose file for a listing; os.ReadFile when nil.
	ReadFile func(name string) ([]byte, error)
}

// Agent is the Docker Agent's operations.
type Agent struct {
	o    Options
	jobs *agent.Jobs

	mu    sync.Mutex
	locks map[string]*sync.Mutex // a Compose file's path → its write lock

	// composeVersion is `docker compose version --short`, asked until it
	// answers once: the plugin doesn't change while the Agent's container
	// runs.
	composeMu      sync.Mutex
	composeVersion string
}

// composeVersionOnce returns the Compose plugin's version, "" while it
// can't be told.
func (a *Agent) composeVersionOnce(ctx context.Context) string {
	a.composeMu.Lock()
	defer a.composeMu.Unlock()
	if a.composeVersion == "" {
		var out []string
		if a.o.Compose.Run(ctx, "/", []string{"version", "--short"}, func(l string) { out = append(out, l) }) == nil && len(out) > 0 {
			a.composeVersion = out[0]
		}
	}
	return a.composeVersion
}

// New returns the operations, to Register on a Server.
func New(o Options) (*Agent, error) {
	for i, r := range o.Roots {
		if !filepath.IsAbs(r) {
			return nil, fmt.Errorf("Compose root %q isn't absolute", r)
		}
		o.Roots[i] = filepath.Clean(r)
	}
	if o.ReadFile == nil {
		o.ReadFile = os.ReadFile
	}
	return &Agent{o: o, locks: map[string]*sync.Mutex{}}, nil
}

// Register adds the Docker Agent's operations to s.
func (a *Agent) Register(s *agent.Server, info func(context.Context) agent.Info) {
	a.jobs = s.Jobs()
	s.Handle("GET "+agent.PathInfo, func(w http.ResponseWriter, r *http.Request) {
		i := info(r.Context())
		i.Roots = a.o.Roots
		if v, err := a.o.Docker.Version(r.Context()); err == nil {
			i.Docker = v
		}
		i.Compose = a.composeVersionOnce(r.Context())
		agent.WriteJSON(w, http.StatusOK, i)
	})
	s.Handle("GET "+agent.PathContainers, func(w http.ResponseWriter, r *http.Request) {
		cs, err := a.o.Docker.Containers(r.Context())
		if err != nil {
			agent.WriteError(w, http.StatusBadGateway, agent.CodeFailed, "Docker: "+err.Error())
			return
		}
		agent.WriteJSON(w, http.StatusOK, cs)
	})
	s.Handle("GET "+agent.PathProjects, a.projects)
	s.Handle("GET "+agent.PathFile, a.file)
	s.Handle("POST "+agent.PathValidate, a.validate)
	s.Handle("POST "+agent.PathWrite, a.write)
	s.Handle("POST "+agent.PathCreate, a.create)
	s.Handle("POST "+agent.PathCompose, a.compose)
	s.Handle("POST "+agent.PathUpdate, a.updateSelf)
}

// composeNames are the file names Compose looks for, in its order.
var composeNames = []string{"compose.yaml", "compose.yml", "docker-compose.yaml", "docker-compose.yml"}

// scanDepth is how deep under a root the scan looks for Compose files.
const scanDepth = 3

func (a *Agent) projects(w http.ResponseWriter, r *http.Request) {
	res, err := a.List(r.Context())
	if err != nil {
		agent.WriteError(w, http.StatusBadGateway, agent.CodeFailed, err.Error())
		return
	}
	agent.WriteJSON(w, http.StatusOK, res)
}

// List finds every Compose Project: from its containers' labels, and from
// the Compose files under the roots, which finds those with nothing
// running.
func (a *Agent) List(ctx context.Context) (agent.ProjectsResponse, error) {
	cs, err := a.o.Docker.Containers(ctx)
	if err != nil {
		return agent.ProjectsResponse{}, fmt.Errorf("Docker: %w", err)
	}
	byName := map[string]*agent.Project{}
	add := func(p agent.Project, how string) *agent.Project {
		if have, ok := byName[p.Name]; ok {
			if !slices.Contains(have.Found, how) {
				have.Found = append(have.Found, how)
			}
			if len(have.ConfigFiles) == 0 {
				have.ConfigFiles, have.WorkingDir = p.ConfigFiles, p.WorkingDir
			}
			return have
		}
		p.Found = []string{how}
		byName[p.Name] = &p
		return &p
	}
	for _, c := range cs {
		name := c.Labels[agent.LabelProject]
		if name == "" || c.Labels[agent.LabelOneOff] == "True" {
			continue
		}
		var files []string
		for f := range strings.SplitSeq(c.Labels[agent.LabelConfigFiles], ",") {
			if f = strings.TrimSpace(f); f != "" {
				files = append(files, f)
			}
		}
		add(agent.Project{Name: name, WorkingDir: c.Labels[agent.LabelWorkingDir], ConfigFiles: files}, "labels")
	}
	// Each Compose file is read once for the listing: for its project's
	// name here and for its services in describe.
	type content struct {
		b   []byte
		err error
	}
	files := map[string]content{}
	read := func(file string) ([]byte, error) {
		c, ok := files[file]
		if !ok {
			c.b, c.err = a.o.ReadFile(file)
			files[file] = c
		}
		return c.b, c.err
	}
	for _, root := range a.o.Roots {
		for _, file := range scan(root) {
			b, _ := read(file)
			add(agent.Project{Name: projectNameFromContent(string(b), filepath.Dir(file)), WorkingDir: filepath.Dir(file), ConfigFiles: []string{file}}, "scan")
		}
	}
	out := agent.ProjectsResponse{Containers: cs, Projects: []agent.Project{}}
	for _, p := range byName {
		a.describe(p, cs, read)
		out.Projects = append(out.Projects, *p)
	}
	slices.SortFunc(out.Projects, func(x, y agent.Project) int { return strings.Compare(x.Name, y.Name) })
	return out, nil
}

// scan returns the Compose files under root, at most scanDepth deep: the
// first of composeNames in each directory. Hidden directories and
// unreadable ones are skipped.
func scan(root string) []string {
	var out []string
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || !d.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		depth := 0
		if rel != "." {
			depth = strings.Count(rel, string(filepath.Separator)) + 1
			if strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
		}
		for _, n := range composeNames {
			if fi, err := os.Stat(filepath.Join(path, n)); err == nil && fi.Mode().IsRegular() {
				out = append(out, filepath.Join(path, n))
				break
			}
		}
		if depth >= scanDepth {
			return filepath.SkipDir
		}
		return nil
	})
	return out
}

var nonName = regexp.MustCompile(`[^a-z0-9_-]+`)

// describe fills p's Compose services from its file and its containers.
func (a *Agent) describe(p *agent.Project, cs []agent.Container, read func(file string) ([]byte, error)) {
	services := map[string]*agent.Service{}
	if len(p.ConfigFiles) > 0 {
		if !a.inRoots(p.ConfigFiles[0]) {
			p.Error = fmt.Sprintf("%s isn't under a Compose root the Docker Agent mounts", p.ConfigFiles[0])
		} else if b, err := read(p.ConfigFiles[0]); err != nil {
			p.Error = "can't read the Compose file: " + err.Error()
		} else {
			parsed, err := ParseServices(b)
			if err != nil {
				p.Error = err.Error()
			}
			for _, s := range parsed {
				services[s.Name] = &s
			}
		}
	}
	for _, c := range cs {
		if c.Labels[agent.LabelProject] != p.Name || c.Labels[agent.LabelOneOff] == "True" {
			continue
		}
		name := c.Labels[agent.LabelService]
		s, ok := services[name]
		if !ok {
			s = &agent.Service{Name: name, Image: c.Image}
			services[name] = s
		}
		s.Containers = append(s.Containers, c.ID)
	}
	p.Services = []agent.Service{}
	for _, s := range services {
		p.Services = append(p.Services, *s)
	}
	slices.SortFunc(p.Services, func(x, y agent.Service) int { return strings.Compare(x.Name, y.Name) })
}

// ParseServices reads the Compose services of a Compose file: each
// one's image and published ports as written (variables unresolved).
func ParseServices(b []byte) ([]agent.Service, error) {
	var doc struct {
		Services map[string]struct {
			Image string `yaml:"image"`
			Ports []any  `yaml:"ports"`
		} `yaml:"services"`
	}
	if err := yaml.Unmarshal(b, &doc); err != nil {
		return nil, fmt.Errorf("the Compose file isn't valid YAML: %w", err)
	}
	var out []agent.Service
	for name, s := range doc.Services {
		svc := agent.Service{Name: name, Image: s.Image}
		for _, p := range s.Ports {
			switch v := p.(type) {
			case string:
				svc.Ports = append(svc.Ports, v)
			case int:
				svc.Ports = append(svc.Ports, fmt.Sprint(v))
			case map[string]any:
				if pub, ok := v["published"]; ok {
					svc.Ports = append(svc.Ports, fmt.Sprintf("%v:%v", pub, v["target"]))
				}
			}
		}
		out = append(out, svc)
	}
	slices.SortFunc(out, func(x, y agent.Service) int { return strings.Compare(x.Name, y.Name) })
	return out, nil
}

// inRoots reports whether path lies under one of the roots.
func (a *Agent) inRoots(path string) bool {
	path = filepath.Clean(path)
	if !filepath.IsAbs(path) {
		return false
	}
	// As the reads and writes that follow will see it: a symlink inside a
	// root may lead out of it.
	path = resolve(path)
	for _, r := range a.o.Roots {
		r = resolve(r)
		if path == r || strings.HasPrefix(path, r+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

// resolve follows the symlinks in path, an absolute clean path. What
// doesn't exist yet (a new Compose Project's directory and file) is kept
// as written under the deepest directory that does.
func resolve(path string) string {
	rest := ""
	for dir := path; ; {
		if real, err := filepath.EvalSymlinks(dir); err == nil {
			return filepath.Join(real, rest)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return path
		}
		rest = filepath.Join(filepath.Base(dir), rest)
		dir = parent
	}
}

// composeFile checks that path is a Compose file under the roots.
func (a *Agent) composeFile(path string) error {
	if !a.inRoots(path) {
		return fmt.Errorf("%s isn't under a Compose root the Docker Agent mounts (%s)", path, strings.Join(a.o.Roots, ", "))
	}
	if !slices.Contains(composeNames, filepath.Base(path)) && !strings.HasSuffix(path, ".yaml") && !strings.HasSuffix(path, ".yml") {
		return fmt.Errorf("%s isn't a Compose file", path)
	}
	return nil
}

func sum(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

func (a *Agent) file(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")
	if err := a.composeFile(path); err != nil {
		agent.WriteError(w, http.StatusForbidden, agent.CodeOutOfRoots, err.Error())
		return
	}
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		agent.WriteError(w, http.StatusNotFound, agent.CodeNotFound, path+" doesn't exist")
		return
	} else if err != nil {
		agent.WriteError(w, http.StatusInternalServerError, agent.CodeFailed, err.Error())
		return
	}
	out := agent.File{Path: path, Content: string(b), SHA256: sum(b)}
	env := filepath.Join(filepath.Dir(path), ".env")
	if e, err := os.ReadFile(env); err == nil {
		out.Env = &agent.File{Path: env, Content: string(e), SHA256: sum(e)}
	}
	builds := a.dockerfiles(path, b)
	for _, f := range slices.Sorted(maps.Keys(builds)) {
		if d, err := os.ReadFile(f); err == nil {
			out.Dockerfiles = append(out.Dockerfiles, agent.Dockerfile{Path: f, Content: string(d), SHA256: sum(d), Services: builds[f]})
		}
	}
	agent.WriteJSON(w, http.StatusOK, out)
}

// dockerfiles maps each Dockerfile the Compose file at path (content b)
// builds from to the Compose services built from it: build's context,
// relative to the file's directory, joined with its dockerfile
// (Dockerfile by default). Remote contexts, inline Dockerfiles, paths
// with variables and paths outside the roots are left out.
func (a *Agent) dockerfiles(path string, b []byte) map[string][]string {
	var doc struct {
		Services map[string]struct {
			Build any `yaml:"build"`
		} `yaml:"services"`
	}
	if yaml.Unmarshal(b, &doc) != nil {
		return nil
	}
	out := map[string][]string{}
	for name, s := range doc.Services {
		var dir, file string
		switch v := s.Build.(type) {
		case string:
			dir = v
		case map[string]any:
			if _, inline := v["dockerfile_inline"]; inline {
				continue
			}
			dir, _ = v["context"].(string)
			file, _ = v["dockerfile"].(string)
		default:
			continue
		}
		if dir == "" {
			dir = "."
		}
		if file == "" {
			file = "Dockerfile"
		}
		if strings.Contains(dir+file, "$") || strings.Contains(dir, "://") || strings.HasPrefix(dir, "git@") || strings.HasPrefix(dir, "github.com/") {
			continue
		}
		if !filepath.IsAbs(dir) {
			dir = filepath.Join(filepath.Dir(path), dir)
		}
		if !filepath.IsAbs(file) {
			file = filepath.Join(dir, file)
		}
		if file = filepath.Clean(file); a.inRoots(file) {
			out[file] = append(out[file], name)
		}
	}
	for _, s := range out {
		slices.Sort(s)
	}
	return out
}

func (a *Agent) validate(w http.ResponseWriter, r *http.Request) {
	var req agent.ValidateRequest
	if !agent.ReadJSON(w, r, &req) {
		return
	}
	if err := a.composeFile(req.Path); err != nil {
		agent.WriteError(w, http.StatusForbidden, agent.CodeOutOfRoots, err.Error())
		return
	}
	agent.WriteJSON(w, http.StatusOK, a.Validate(r.Context(), req.Path, req.Project, req.Content, req.Env))
}

// Validate runs `docker compose config` on content as if it were path:
// from a temporary file beside it, so its .env and relative paths resolve.
// With env, it is a new Project's: both go in a temporary directory under
// its root, the .env as .env.
func (a *Agent) Validate(ctx context.Context, path, project, content, env string) agent.ValidateResponse {
	dir := filepath.Dir(path)
	projectDir := dir
	if env != "" {
		tmpDir, err := os.MkdirTemp(a.rootOf(path), ".ddash-validate-*")
		if err != nil {
			return agent.ValidateResponse{Error: "can't validate: " + err.Error()}
		}
		defer os.RemoveAll(tmpDir)
		if err := os.WriteFile(filepath.Join(tmpDir, ".env"), []byte(env), 0o600); err != nil {
			return agent.ValidateResponse{Error: "can't validate: " + err.Error()}
		}
		dir, projectDir = tmpDir, tmpDir
	} else if _, err := os.Stat(dir); err != nil {
		// A new Project's directory: validate from its root instead.
		dir = a.rootOf(path)
	}
	tmp, err := os.CreateTemp(dir, ".ddash-validate-*.yaml")
	if err != nil {
		return agent.ValidateResponse{Error: "can't validate: " + err.Error()}
	}
	defer os.Remove(tmp.Name())
	if _, err := io.WriteString(tmp, content); err != nil {
		tmp.Close()
		return agent.ValidateResponse{Error: "can't validate: " + err.Error()}
	}
	tmp.Close()
	if project == "" {
		project = projectNameFromContent(content, filepath.Dir(path))
	}
	var lines []string
	args := []string{"-p", project, "--project-directory", projectDir, "-f", tmp.Name(), "config", "--quiet"}
	if err := a.o.Compose.Run(ctx, dir, args, func(l string) { lines = append(lines, l) }); err != nil {
		msg := strings.Join(lines, "\n")
		if msg == "" {
			msg = err.Error()
		}
		// Compose names the temporary files; the Operator knows the real ones.
		msg = strings.ReplaceAll(msg, tmp.Name(), path)
		return agent.ValidateResponse{Error: strings.ReplaceAll(msg, projectDir, filepath.Dir(path))}
	}
	return agent.ValidateResponse{OK: true}
}

// projectNameFromContent is the name Compose gives the project of a
// Compose file with content in dir: its top-level name, else the
// directory's name, lower-cased with what Compose drops removed.
func projectNameFromContent(content, dir string) string {
	var doc struct {
		Name string `yaml:"name"`
	}
	if yaml.Unmarshal([]byte(content), &doc) == nil && doc.Name != "" && !strings.Contains(doc.Name, "$") {
		return doc.Name
	}
	return strings.TrimLeft(nonName.ReplaceAllString(strings.ToLower(filepath.Base(dir)), ""), "_-")
}

func (a *Agent) rootOf(path string) string {
	for _, r := range a.o.Roots {
		if strings.HasPrefix(filepath.Clean(path), r+string(filepath.Separator)) {
			return r
		}
	}
	return os.TempDir()
}

func (a *Agent) lock(path string) func() {
	a.mu.Lock()
	l, ok := a.locks[path]
	if !ok {
		l = &sync.Mutex{}
		a.locks[path] = l
	}
	a.mu.Unlock()
	l.Lock()
	return l.Unlock
}

func (a *Agent) write(w http.ResponseWriter, r *http.Request) {
	var req agent.WriteRequest
	if !agent.ReadJSON(w, r, &req) {
		return
	}
	isEnv := filepath.Base(req.Path) == ".env"
	// A Dockerfile is written only when its Compose file builds from it.
	isDockerfile := req.File != "" && req.File != req.Path && !isEnv
	check := req.Path
	switch {
	case isEnv:
		check = filepath.Join(filepath.Dir(req.Path), "compose.yaml")
	case isDockerfile:
		check = req.File
	}
	if err := a.composeFile(check); err != nil {
		agent.WriteError(w, http.StatusForbidden, agent.CodeOutOfRoots, err.Error())
		return
	}
	if isDockerfile {
		b, err := os.ReadFile(req.File)
		if err != nil {
			agent.WriteError(w, http.StatusNotFound, agent.CodeNotFound, "can't read "+req.File+": "+err.Error())
			return
		}
		if _, ok := a.dockerfiles(req.File, b)[filepath.Clean(req.Path)]; !ok {
			agent.WriteError(w, http.StatusForbidden, agent.CodeOutOfRoots, req.Path+" isn't a Dockerfile "+req.File+" builds from")
			return
		}
	}
	defer a.lock(req.Path)()
	old, err := os.ReadFile(req.Path)
	// A .env not there yet is created, with nothing to back up.
	create := isEnv && errors.Is(err, os.ErrNotExist) && req.ExpectSHA256 == ""
	switch {
	case create:
	case err != nil:
		agent.WriteError(w, http.StatusNotFound, agent.CodeNotFound, "can't read "+req.Path+": "+err.Error())
		return
	case sum(old) != req.ExpectSHA256:
		agent.WriteError(w, http.StatusConflict, agent.CodeConflict, req.Path+" changed on disk since it was read")
		return
	}
	if !isEnv && !isDockerfile {
		if v := a.Validate(r.Context(), req.Path, req.Project, req.Content, ""); !v.OK {
			agent.WriteError(w, http.StatusUnprocessableEntity, agent.CodeInvalid, v.Error)
			return
		}
	}
	// A .env holds secrets: readable by its owner only.
	perm := os.FileMode(0o600)
	if !create {
		// Gone since it was read: the backup below still gets the default.
		if fi, err := os.Stat(req.Path); err == nil {
			perm = fi.Mode().Perm()
		}
		if err := agent.WriteFileAtomic(req.Path+".bak", old, perm); err != nil {
			agent.WriteError(w, http.StatusInternalServerError, agent.CodeFailed, "can't keep the backup: "+err.Error())
			return
		}
	}
	if err := agent.WriteFileAtomic(req.Path, []byte(req.Content), perm); err != nil {
		agent.WriteError(w, http.StatusInternalServerError, agent.CodeFailed, err.Error())
		return
	}
	agent.WriteJSON(w, http.StatusOK, agent.File{Path: req.Path, Content: req.Content, SHA256: sum([]byte(req.Content))})
}

func (a *Agent) create(w http.ResponseWriter, r *http.Request) {
	var req agent.CreateRequest
	if !agent.ReadJSON(w, r, &req) {
		return
	}
	dir := filepath.Clean(req.Directory)
	path := filepath.Join(dir, "compose.yaml")
	if !a.inRoots(path) {
		agent.WriteError(w, http.StatusForbidden, agent.CodeOutOfRoots, fmt.Sprintf("%s isn't under a Compose root the Docker Agent mounts (%s)", dir, strings.Join(a.o.Roots, ", ")))
		return
	}
	if req.Name == "" || nonName.MatchString(req.Name) {
		agent.WriteError(w, http.StatusBadRequest, agent.CodeInvalid, "a Compose Project's name is lower-case letters, digits, - and _")
		return
	}
	defer a.lock(path)()
	names := composeNames
	if req.Env != "" {
		names = append(slices.Clip(names), ".env")
	}
	for _, n := range names {
		if _, err := os.Stat(filepath.Join(dir, n)); err == nil {
			agent.WriteError(w, http.StatusConflict, agent.CodeConflict, filepath.Join(dir, n)+" already exists")
			return
		}
	}
	if v := a.Validate(r.Context(), path, req.Name, req.Content, req.Env); !v.OK {
		agent.WriteError(w, http.StatusUnprocessableEntity, agent.CodeInvalid, v.Error)
		return
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		agent.WriteError(w, http.StatusInternalServerError, agent.CodeFailed, err.Error())
		return
	}
	var out agent.File
	if req.Env != "" {
		env := filepath.Join(dir, ".env")
		// A .env holds secrets: readable by its owner only.
		if err := agent.WriteFileAtomic(env, []byte(req.Env), 0o600); err != nil {
			agent.WriteError(w, http.StatusInternalServerError, agent.CodeFailed, err.Error())
			return
		}
		out.Env = &agent.File{Path: env, Content: req.Env, SHA256: sum([]byte(req.Env))}
	}
	if err := agent.WriteFileAtomic(path, []byte(req.Content), 0o644); err != nil {
		agent.WriteError(w, http.StatusInternalServerError, agent.CodeFailed, err.Error())
		return
	}
	out.Path, out.Content, out.SHA256 = path, req.Content, sum([]byte(req.Content))
	agent.WriteJSON(w, http.StatusOK, out)
}

var opTitles = map[agent.ComposeOp]string{
	agent.Up:      "Start",
	agent.Down:    "Stop",
	agent.Restart: "Restart",
	agent.Pull:    "Pull",
	agent.Update:  "Update",
}

func (a *Agent) compose(w http.ResponseWriter, r *http.Request) {
	var req agent.ComposeRequest
	if !agent.ReadJSON(w, r, &req) {
		return
	}
	title, ok := opTitles[req.Op]
	switch {
	case !ok:
		agent.WriteError(w, http.StatusBadRequest, agent.CodeInvalid, fmt.Sprintf("no Compose operation %q", req.Op))
		return
	case req.Project == "" || nonName.MatchString(req.Project):
		agent.WriteError(w, http.StatusBadRequest, agent.CodeInvalid, "project must be a Compose project name")
		return
	}
	if err := a.composeFile(req.File); err != nil {
		agent.WriteError(w, http.StatusForbidden, agent.CodeOutOfRoots, err.Error())
		return
	}
	// Compose runs in the working directory and reads its .env there.
	if !a.inRoots(req.WorkingDir) {
		agent.WriteError(w, http.StatusForbidden, agent.CodeOutOfRoots, fmt.Sprintf("%s is outside the Compose roots", req.WorkingDir))
		return
	}
	for _, s := range req.Services {
		if s == "" || strings.HasPrefix(s, "-") {
			agent.WriteError(w, http.StatusBadRequest, agent.CodeInvalid, fmt.Sprintf("bad service %q", s))
			return
		}
	}
	steps, run := a.op(req)
	what := req.Project
	if len(req.Services) > 0 {
		what += " (" + strings.Join(req.Services, ", ") + ")"
	}
	// A Compose command stopped part-way leaves nothing `up -d` doesn't
	// mend: ddash may cancel it.
	job, err := a.jobs.StartCancellable("compose."+string(req.Op), title+" "+what, "project:"+req.Project, steps, run)
	if errors.Is(err, agent.ErrBusy) {
		agent.WriteError(w, http.StatusConflict, agent.CodeBusy, err.Error())
		return
	}
	agent.WriteJSON(w, http.StatusAccepted, agent.JobRef{Job: job})
}

// op is a Compose operation's steps and body.
func (a *Agent) op(req agent.ComposeRequest) ([]string, agent.Run) {
	base := []string{"-p", req.Project, "--project-directory", req.WorkingDir, "-f", req.File}
	run := func(ctx context.Context, p *agent.Progress, i int, args ...string) error {
		p.Begin(i)
		var last []string
		err := a.o.Compose.Run(ctx, req.WorkingDir, append(slices.Clone(base), args...), func(l string) {
			p.Log(i, l)
			last = append(last, l)
			if len(last) > 3 {
				last = last[1:]
			}
		})
		if err != nil {
			if len(last) > 0 {
				return errors.New(strings.Join(last, " · "))
			}
			return err
		}
		return nil
	}
	svc := req.Services
	switch req.Op {
	case agent.Up:
		steps := []string{"Start containers"}
		args := []string{"up", "-d", "--remove-orphans"}
		if req.Pull {
			args = append(args, "--pull", "always")
		}
		if req.Wait {
			args = append(args, "--wait", "--wait-timeout", "60")
		}
		if req.Build {
			args = append(args, "--build")
		}
		return steps, func(ctx context.Context, p *agent.Progress) (map[string]string, error) {
			if err := run(ctx, p, 0, append(args, svc...)...); err != nil {
				return nil, err
			}
			p.Done(0, "")
			return nil, nil
		}
	case agent.Down:
		args := []string{"down"}
		if len(svc) > 0 {
			args = append([]string{"stop"}, svc...)
		}
		return []string{"Stop containers"}, func(ctx context.Context, p *agent.Progress) (map[string]string, error) {
			if err := run(ctx, p, 0, args...); err != nil {
				return nil, err
			}
			p.Done(0, "")
			return nil, nil
		}
	case agent.Restart:
		return []string{"Restart containers"}, func(ctx context.Context, p *agent.Progress) (map[string]string, error) {
			if err := run(ctx, p, 0, append([]string{"restart"}, svc...)...); err != nil {
				return nil, err
			}
			p.Done(0, "")
			return nil, nil
		}
	case agent.Pull:
		return []string{"Pull images"}, func(ctx context.Context, p *agent.Progress) (map[string]string, error) {
			if err := run(ctx, p, 0, append([]string{"pull", "--ignore-buildable"}, svc...)...); err != nil {
				return nil, err
			}
			p.Done(0, "")
			return nil, nil
		}
	}
	return []string{"Pull images", "Compare digests", "Recreate changed services"}, func(ctx context.Context, p *agent.Progress) (map[string]string, error) {
		return a.update(ctx, req, p, run)
	}
}

// update pulls, compares each Compose service's running image with the
// one pulled, and recreates only those that changed.
func (a *Agent) update(ctx context.Context, req agent.ComposeRequest, p *agent.Progress, run func(context.Context, *agent.Progress, int, ...string) error) (map[string]string, error) {
	b, err := os.ReadFile(req.File)
	if err != nil {
		return nil, fmt.Errorf("can't read the Compose file: %w", err)
	}
	services, err := ParseServices(b)
	if err != nil {
		return nil, err
	}
	cs, err := a.o.Docker.Containers(ctx)
	if err != nil {
		return nil, fmt.Errorf("Docker: %w", err)
	}
	running := map[string]agent.Container{}
	for _, c := range cs {
		if c.Labels[agent.LabelProject] == req.Project && c.Labels[agent.LabelOneOff] != "True" {
			running[c.Labels[agent.LabelService]] = c
		}
	}
	if err := run(ctx, p, 0, append([]string{"pull", "--ignore-buildable"}, req.Services...)...); err != nil {
		return nil, err
	}
	p.Done(0, "")
	p.Begin(1)
	result := map[string]string{}
	var changed []string
	for _, s := range services {
		if len(req.Services) > 0 && !slices.Contains(req.Services, s.Name) {
			continue
		}
		c, ok := running[s.Name]
		if !ok || s.Image == "" {
			continue
		}
		id, digests, err := a.o.Docker.ImageDigests(ctx, c.Image)
		if err != nil {
			p.Log(1, fmt.Sprintf("%s: can't inspect %s: %v", s.Name, c.Image, err))
			continue
		}
		if id != c.ImageID {
			changed = append(changed, s.Name)
			result[s.Name] = digestOf(c.RepoDigests, c.ImageID) + " " + digestOf(digests, id)
		}
	}
	if len(changed) == 0 {
		p.Done(1, "already up to date")
		p.Skip(2, "no image changed")
		return map[string]string{"unchanged": "true"}, nil
	}
	p.Done(1, fmt.Sprintf("%d changed: %s", len(changed), strings.Join(changed, ", ")))
	if err := run(ctx, p, 2, append([]string{"up", "-d", "--no-deps"}, changed...)...); err != nil {
		return result, err
	}
	p.Done(2, strings.Join(changed, ", "))
	return result, nil
}

// digestOf is an image's first repo digest's sha256 part, else its id.
func digestOf(repoDigests []string, id string) string {
	for _, d := range repoDigests {
		if _, digest, ok := strings.Cut(d, "@"); ok {
			return digest
		}
	}
	return id
}

// updateSelf recreates the Agent's own container on req.Image: a helper
// container from that image rewrites the image in the Agent's own Compose
// file and runs `docker compose up -d` from outside, since a container
// can't recreate itself.
func (a *Agent) updateSelf(w http.ResponseWriter, r *http.Request) {
	var req agent.UpdateRequest
	if !agent.ReadJSON(w, r, &req) {
		return
	}
	// The helper container below gets the Docker socket, so it runs the
	// Docker Agent's own image and no other.
	if !agentImageRef.MatchString(req.Image) {
		agent.WriteError(w, http.StatusBadRequest, agent.CodeInvalid, "image must be the Docker Agent's: "+imageRepo+":<release>")
		return
	}
	cs, err := a.o.Docker.Containers(r.Context())
	if err != nil {
		agent.WriteError(w, http.StatusBadGateway, agent.CodeFailed, "Docker: "+err.Error())
		return
	}
	var self *agent.Container
	for i, c := range cs {
		if a.o.Self != "" && (c.Name == a.o.Self || strings.HasPrefix(c.ID, a.o.Self)) {
			self = &cs[i]
		}
	}
	if self == nil || self.Labels[agent.LabelConfigFiles] == "" {
		agent.WriteError(w, http.StatusConflict, agent.CodeFailed, "the Docker Agent wasn't started by Compose, so it can't update itself: pull the new image and recreate it by hand")
		return
	}
	file := strings.Split(self.Labels[agent.LabelConfigFiles], ",")[0]
	dir := self.Labels[agent.LabelWorkingDir]
	project := self.Labels[agent.LabelProject]
	job, err := a.jobs.Start("agent.update", "Update the Docker Agent to "+req.Release, "agent", []string{"Pull the new image", "Recreate the Docker Agent"}, func(ctx context.Context, p *agent.Progress) (map[string]string, error) {
		p.Begin(0)
		if err := a.o.Compose.Run(ctx, "/", []string{"--docker", "pull", req.Image}, func(l string) { p.Log(0, l) }); err != nil {
			return nil, fmt.Errorf("can't pull %s: %w", req.Image, err)
		}
		p.Done(0, req.Image)
		p.Begin(1)
		args := []string{"--docker", "run", "-d", "--rm",
			"-v", "/var/run/docker.sock:/var/run/docker.sock",
			"-v", dir + ":" + dir,
			req.Image, "recreate", "--file", file, "--dir", dir, "--project", project, "--image", req.Image}
		if err := a.o.Compose.Run(ctx, "/", args, func(l string) { p.Log(1, l) }); err != nil {
			return nil, err
		}
		p.Done(1, "the new Docker Agent starts in a few seconds")
		return nil, nil
	})
	if err != nil {
		agent.WriteError(w, http.StatusConflict, agent.CodeBusy, err.Error())
		return
	}
	agent.WriteJSON(w, http.StatusAccepted, agent.JobRef{Job: job})
}

// agentImage matches the Docker Agent's image reference in its own
// Compose file.
var agentImage = regexp.MustCompile(regexp.QuoteMeta(imageRepo) + `[:@][^\s"']+`)

// imageRepo is the Docker Agent's image, without its tag or digest.
const imageRepo = "ghcr.io/itskontra/ddash-docker-agent"

// agentImageRef matches a whole string that is the Docker Agent's image
// reference, the only image updateSelf runs.
var agentImageRef = regexp.MustCompile(`^` + agentImage.String() + `$`)

// Recreate is the helper container's job: point the Agent's Compose file
// at image and recreate it. It runs outside the Agent's container.
func Recreate(ctx context.Context, file, dir, project, image string, c Compose) error {
	b, err := os.ReadFile(file)
	if err != nil {
		return err
	}
	if !agentImage.Match(b) {
		return fmt.Errorf("%s doesn't name the Docker Agent's image", file)
	}
	fi, err := os.Stat(file)
	if err != nil {
		return err
	}
	if err := agent.WriteFileAtomic(file, agentImage.ReplaceAll(b, []byte(image)), fi.Mode().Perm()); err != nil {
		return err
	}
	return c.Run(ctx, dir, []string{"-p", project, "--project-directory", dir, "-f", file, "up", "-d"}, func(l string) { fmt.Println(l) })
}

// CLI runs the real `docker compose` (and, with a leading "--docker",
// plain `docker`).
type CLI struct{}

func (CLI) Run(ctx context.Context, dir string, args []string, out func(string)) error {
	bin := []string{"docker", "compose"}
	if len(args) > 0 && args[0] == "--docker" {
		bin, args = []string{"docker"}, args[1:]
	}
	cmd := exec.CommandContext(ctx, bin[0], append(bin[1:], args...)...)
	cmd.Dir = dir
	return agent.RunLines(cmd, out)
}
