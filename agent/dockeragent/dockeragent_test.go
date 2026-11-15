package dockeragent_test

import (
	"context"
	"errors"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/itsKontra/ddash/agent"
	"github.com/itsKontra/ddash/agent/dockeragent"
)

type docker struct {
	mu         sync.Mutex
	containers []agent.Container
	// pulled maps an image ref to the id a pull leaves it at.
	pulled map[string]string
}

func (d *docker) Containers(context.Context) ([]agent.Container, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return slices.Clone(d.containers), nil
}

func (d *docker) ImageDigests(_ context.Context, ref string) (string, []string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, c := range d.containers {
		if c.Image == ref {
			if id, ok := d.pulled[ref]; ok {
				return id, []string{strings.Split(ref, ":")[0] + "@sha256:new"}, nil
			}
			return c.ImageID, c.RepoDigests, nil
		}
	}
	return "", nil, errors.New("no such image")
}

func (d *docker) Version(context.Context) (string, error) { return "27.3.1", nil }

// compose records every call, and the .env each config saw in its
// project directory; config fails when the file says "bad:".
type compose struct {
	mu    sync.Mutex
	calls [][]string
	envs  []string
}

func (c *compose) Run(_ context.Context, _ string, args []string, out func(string)) error {
	c.mu.Lock()
	c.calls = append(c.calls, args)
	if slices.Contains(args, "config") {
		env, _ := os.ReadFile(filepath.Join(args[slices.Index(args, "--project-directory")+1], ".env"))
		c.envs = append(c.envs, string(env))
	}
	c.mu.Unlock()
	if slices.Equal(args, []string{"version", "--short"}) {
		out("2.29.7")
	}
	if i := slices.Index(args, "config"); i >= 0 {
		f := args[slices.Index(args, "-f")+1]
		b, _ := os.ReadFile(f)
		if strings.Contains(string(b), "bad:") {
			out("validating " + f + ": services.bad additional property not allowed")
			return errors.New("exit status 15")
		}
	}
	return nil
}

func setup(t *testing.T) (*agent.Client, string, *docker, *compose) {
	t.Helper()
	root := t.TempDir()
	mk := func(rel, content string) {
		p := filepath.Join(root, rel)
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte(content), 0o644)
	}
	mk("media/compose.yaml", "services:\n  jellyfin:\n    image: jellyfin/jellyfin:10.9\n    ports: [\"8096:8096\"]\n")
	mk("media/.env", "TZ=Europe/Berlin\n")
	mk("tools/stopped/docker-compose.yml", "name: idle\nservices:\n  web:\n    image: nginx:1.27\n")
	mk(".hidden/compose.yaml", "services: {}\n")
	d := &docker{pulled: map[string]string{}, containers: []agent.Container{{
		ID: "c1", Name: "media-jellyfin-1", Image: "jellyfin/jellyfin:10.9", State: "running", ImageID: "sha256:old",
		RepoDigests: []string{"jellyfin/jellyfin@sha256:old"},
		Labels: map[string]string{
			agent.LabelProject: "media", agent.LabelService: "jellyfin",
			agent.LabelWorkingDir: filepath.Join(root, "media"), agent.LabelConfigFiles: filepath.Join(root, "media/compose.yaml"),
		},
	}}}
	c := &compose{}
	a, err := dockeragent.New(dockeragent.Options{Roots: []string{root}, Docker: d, Compose: c})
	if err != nil {
		t.Fatal(err)
	}
	s, err := agent.NewServer(agent.ServerOptions{Kind: agent.KindDocker, Release: "dev", StateDir: t.TempDir(), EnrolmentCode: "CODE"})
	if err != nil {
		t.Fatal(err)
	}
	a.Register(s, func(context.Context) agent.Info { return agent.Info{Kind: agent.KindDocker} })
	srv := httptest.NewUnstartedServer(s.Handler())
	srv.TLS = s.TLSConfig()
	srv.StartTLS()
	t.Cleanup(srv.Close)
	token, _, err := agent.Pair(context.Background(), srv.URL, s.Fingerprint(), "CODE")
	if err != nil {
		t.Fatal(err)
	}
	return agent.Dial(srv.URL, token, s.Fingerprint()), root, d, c
}

func TestProjects(t *testing.T) {
	cl, root, _, _ := setup(t)
	var res agent.ProjectsResponse
	if err := cl.Get(context.Background(), agent.PathProjects, nil, &res); err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, p := range res.Projects {
		names = append(names, p.Name)
	}
	// media from its labels and the scan, idle by its name: from the scan
	// only; the hidden directory is skipped.
	if !slices.Equal(names, []string{"idle", "media"}) {
		t.Fatalf("projects = %v", names)
	}
	media := res.Projects[1]
	if !slices.Equal(media.Found, []string{"labels", "scan"}) || len(media.Services) != 1 || media.Services[0].Containers[0] != "c1" || media.Services[0].Ports[0] != "8096:8096" {
		t.Fatalf("media = %+v", media)
	}
	if res.Projects[0].ConfigFiles[0] != filepath.Join(root, "tools/stopped/docker-compose.yml") {
		t.Fatalf("idle = %+v", res.Projects[0])
	}
}

func TestWrite(t *testing.T) {
	ctx := context.Background()
	cl, root, _, _ := setup(t)
	path := filepath.Join(root, "media/compose.yaml")
	var f agent.File
	if err := cl.Get(ctx, agent.PathFile, map[string][]string{"path": {path}}, &f); err != nil {
		t.Fatal(err)
	}
	if f.Env == nil || f.Env.Content != "TZ=Europe/Berlin\n" {
		t.Fatalf("env = %+v", f.Env)
	}
	// Outside the roots.
	if err := cl.Get(ctx, agent.PathFile, map[string][]string{"path": {"/etc/passwd"}}, &f); agent.CodeOf(err) != agent.CodeOutOfRoots {
		t.Fatalf("outside: %v", err)
	}
	// Invalid: refused, nothing written.
	err := cl.Post(ctx, agent.PathWrite, agent.WriteRequest{Path: path, Content: "bad: yes\n", ExpectSHA256: f.SHA256}, nil)
	if agent.CodeOf(err) != agent.CodeInvalid || !strings.Contains(err.Error(), path) || strings.Contains(err.Error(), "ddash-validate") {
		t.Fatalf("invalid: %v", err)
	}
	newContent := strings.Replace(f.Content, "10.9", "10.10", 1)
	if err := cl.Post(ctx, agent.PathWrite, agent.WriteRequest{Path: path, Content: newContent, ExpectSHA256: f.SHA256}, nil); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(path + ".bak"); string(b) != f.Content {
		t.Fatalf("backup = %q", b)
	}
	// The hash read before no longer holds.
	if err := cl.Post(ctx, agent.PathWrite, agent.WriteRequest{Path: path, Content: "x", ExpectSHA256: f.SHA256}, nil); agent.CodeOf(err) != agent.CodeConflict {
		t.Fatalf("stale: %v", err)
	}
	if b, _ := os.ReadFile(path); string(b) != newContent {
		t.Fatalf("file = %q", b)
	}
}

func TestCreate(t *testing.T) {
	ctx := context.Background()
	cl, root, _, _ := setup(t)
	dir := filepath.Join(root, "paperless")
	if err := cl.Post(ctx, agent.PathCreate, agent.CreateRequest{Name: "paperless", Directory: dir, Content: "services:\n  web:\n    image: paperless\n"}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "compose.yaml")); err != nil {
		t.Fatal(err)
	}
	if err := cl.Post(ctx, agent.PathCreate, agent.CreateRequest{Name: "paperless", Directory: dir, Content: "services: {}\n"}, nil); agent.CodeOf(err) != agent.CodeConflict {
		t.Fatalf("again: %v", err)
	}
	if err := cl.Post(ctx, agent.PathCreate, agent.CreateRequest{Name: "x", Directory: "/opt/elsewhere", Content: "services: {}\n"}, nil); agent.CodeOf(err) != agent.CodeOutOfRoots {
		t.Fatalf("outside: %v", err)
	}
}

func TestCreateWithEnv(t *testing.T) {
	ctx := context.Background()
	cl, root, _, c := setup(t)
	dir := filepath.Join(root, "paperless")
	var f agent.File
	if err := cl.Post(ctx, agent.PathCreate, agent.CreateRequest{Name: "paperless", Directory: dir, Content: "services:\n  web:\n    image: paperless:${TAG}\n", Env: "TAG=2.13\n"}, &f); err != nil {
		t.Fatal(err)
	}
	env := filepath.Join(dir, ".env")
	if f.Env == nil || f.Env.Path != env || f.Env.Content != "TAG=2.13\n" {
		t.Fatalf("env = %+v", f.Env)
	}
	if fi, err := os.Stat(env); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("env file: %v, %v", fi, err)
	}
	// Validated with the .env beside the file, in a directory gone since.
	if c.envs[len(c.envs)-1] != "TAG=2.13\n" {
		t.Fatalf("validated with .env %q", c.envs[len(c.envs)-1])
	}
	if left, _ := filepath.Glob(filepath.Join(root, ".ddash-validate-*")); len(left) > 0 {
		t.Fatalf("left behind: %v", left)
	}

	// Refused: nothing written, and Compose's error names the real paths.
	bad := filepath.Join(root, "broken")
	err := cl.Post(ctx, agent.PathCreate, agent.CreateRequest{Name: "broken", Directory: bad, Content: "bad: yes\n", Env: "A=1\n"}, nil)
	if agent.CodeOf(err) != agent.CodeInvalid || !strings.Contains(err.Error(), filepath.Join(bad, "compose.yaml")) {
		t.Fatalf("invalid: %v", err)
	}
	if _, err := os.Stat(bad); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("broken left behind: %v", err)
	}
	// A .env already there isn't overwritten.
	stray := filepath.Join(root, "stray")
	os.MkdirAll(stray, 0o755)
	os.WriteFile(filepath.Join(stray, ".env"), []byte("KEEP=1\n"), 0o600)
	if err := cl.Post(ctx, agent.PathCreate, agent.CreateRequest{Name: "stray", Directory: stray, Content: "services: {}\n", Env: "A=1\n"}, nil); agent.CodeOf(err) != agent.CodeConflict {
		t.Fatalf("stray: %v", err)
	}
}

func TestWriteNewEnv(t *testing.T) {
	ctx := context.Background()
	cl, root, _, _ := setup(t)
	env := filepath.Join(root, "tools/stopped/.env")
	if err := cl.Post(ctx, agent.PathWrite, agent.WriteRequest{Path: env, Content: "A=1\n"}, nil); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Stat(env); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("env file: %v, %v", fi, err)
	}
	if _, err := os.Stat(env + ".bak"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a new .env has no backup: %v", err)
	}
	// There now: a write without its hash is stale.
	if err := cl.Post(ctx, agent.PathWrite, agent.WriteRequest{Path: env, Content: "A=2\n"}, nil); agent.CodeOf(err) != agent.CodeConflict {
		t.Fatalf("again: %v", err)
	}
	// Only a .env is created: a missing Compose file isn't.
	if err := cl.Post(ctx, agent.PathWrite, agent.WriteRequest{Path: filepath.Join(root, "tools/new/compose.yaml"), Content: "services: {}\n"}, nil); agent.CodeOf(err) != agent.CodeNotFound {
		t.Fatalf("compose file: %v", err)
	}
}

func TestDockerfiles(t *testing.T) {
	ctx := context.Background()
	cl, root, _, c := setup(t)
	dir := filepath.Join(root, "shop")
	os.MkdirAll(filepath.Join(dir, "api"), 0o755)
	file := filepath.Join(dir, "compose.yaml")
	os.WriteFile(file, []byte(`services:
  web:
    build: .
  worker:
    build: .
  api:
    build:
      context: api
      dockerfile: Containerfile
  inline:
    build:
      dockerfile_inline: "FROM alpine"
  remote:
    build: https://github.com/example/app.git
  outside:
    build: ../../..
  db:
    image: postgres:17
`), 0o644)
	os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte("FROM nginx:1.27\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "api/Containerfile"), []byte("FROM golang:1.25\n"), 0o640)

	// The Dockerfiles under the roots its Compose services build from;
	// inline, remote and outside ones aren't.
	var f agent.File
	if err := cl.Get(ctx, agent.PathFile, map[string][]string{"path": {file}}, &f); err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, d := range f.Dockerfiles {
		got = append(got, strings.TrimPrefix(d.Path, dir+"/")+":"+strings.Join(d.Services, ","))
	}
	if !slices.Equal(got, []string{"Dockerfile:web,worker", "api/Containerfile:api"}) {
		t.Fatalf("dockerfiles = %v", got)
	}
	api := f.Dockerfiles[1]

	// Written as its Compose file names it, its previous content kept and
	// its mode too; never validated with docker compose config.
	calls := len(c.calls)
	if err := cl.Post(ctx, agent.PathWrite, agent.WriteRequest{Path: api.Path, Content: "FROM golang:1.26\n", ExpectSHA256: api.SHA256, File: file}, nil); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(api.Path); string(b) != "FROM golang:1.26\n" {
		t.Fatalf("Containerfile = %q", b)
	}
	if b, _ := os.ReadFile(api.Path + ".bak"); string(b) != "FROM golang:1.25\n" {
		t.Fatalf("backup = %q", b)
	}
	if fi, _ := os.Stat(api.Path); fi.Mode().Perm() != 0o640 {
		t.Fatalf("mode = %v", fi.Mode())
	}
	if len(c.calls) != calls {
		t.Fatalf("compose ran: %v", c.calls[calls:])
	}

	// Nothing else is written as a Dockerfile: not a file the Compose file
	// doesn't build from, and not without the Compose file.
	other := filepath.Join(root, "media/compose.yaml")
	if err := cl.Post(ctx, agent.PathWrite, agent.WriteRequest{Path: filepath.Join(dir, "api/main.go"), Content: "x", File: file}, nil); agent.CodeOf(err) != agent.CodeOutOfRoots {
		t.Fatalf("main.go: %v", err)
	}
	if err := cl.Post(ctx, agent.PathWrite, agent.WriteRequest{Path: filepath.Join(dir, "Dockerfile"), Content: "x", File: other}, nil); agent.CodeOf(err) != agent.CodeOutOfRoots {
		t.Fatalf("another Compose file's: %v", err)
	}
	if err := cl.Post(ctx, agent.PathWrite, agent.WriteRequest{Path: filepath.Join(dir, "Dockerfile"), Content: "x"}, nil); agent.CodeOf(err) != agent.CodeOutOfRoots {
		t.Fatalf("without its Compose file: %v", err)
	}
}

func wait(t *testing.T, cl *agent.Client, id string) agent.Job {
	t.Helper()
	var j agent.Job
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		if err := cl.Get(context.Background(), agent.PathJobs+"/"+id, nil, &j); err != nil {
			t.Fatal(err)
		}
		if j.State.Ended() {
			return j
		}
	}
	t.Fatalf("job %s didn't end: %+v", id, j)
	return j
}

func TestUpdate(t *testing.T) {
	ctx := context.Background()
	cl, root, d, c := setup(t)
	req := agent.ComposeRequest{Op: agent.Update, Project: "media", WorkingDir: filepath.Join(root, "media"), File: filepath.Join(root, "media/compose.yaml")}

	var ref agent.JobRef
	if err := cl.Post(ctx, agent.PathCompose, req, &ref); err != nil {
		t.Fatal(err)
	}
	j := wait(t, cl, ref.Job.ID)
	if j.State != agent.Done || j.Result["unchanged"] != "true" || j.Steps[2].State != agent.Skipped {
		t.Fatalf("unchanged update = %+v", j)
	}

	d.pulled["jellyfin/jellyfin:10.9"] = "sha256:new"
	if err := cl.Post(ctx, agent.PathCompose, req, &ref); err != nil {
		t.Fatal(err)
	}
	j = wait(t, cl, ref.Job.ID)
	if j.State != agent.Done || j.Result["jellyfin"] != "sha256:old sha256:new" {
		t.Fatalf("update = %+v", j)
	}
	last := c.calls[len(c.calls)-1]
	if !slices.Equal(last[len(last)-4:], []string{"up", "-d", "--no-deps", "jellyfin"}) {
		t.Fatalf("recreated with %v", last)
	}
}

// The Agent offers named operations only (ADR-0005): its own update runs
// the Docker Agent's image and no other, since the helper container gets
// the Docker socket.
func TestUpdateSelfRunsOnlyTheDockerAgentsImage(t *testing.T) {
	ctx := context.Background()
	cl, _, _, c := setup(t)

	for _, image := range []string{
		"alpine:3",
		"evil.example/itskontra/ddash-docker-agent:v1.2.3",
		"--privileged",
		"ghcr.io/itskontra/ddash-docker-agent:v1.2.3 alpine",
	} {
		var ref agent.JobRef
		err := cl.Post(ctx, agent.PathUpdate, agent.UpdateRequest{Release: "v1.2.3", Image: image}, &ref)
		var refused *agent.APIError
		if !errors.As(err, &refused) || refused.Code != agent.CodeInvalid {
			t.Errorf("image %q: %v, want %s", image, err, agent.CodeInvalid)
		}
	}
	if len(c.calls) != 0 {
		t.Errorf("ran %v", c.calls)
	}
}

func TestAComposeOperationStaysInTheRoots(t *testing.T) {
	ctx := context.Background()
	cl, root, _, c := setup(t)
	req := agent.ComposeRequest{Op: agent.Up, Project: "media", WorkingDir: "/etc", File: filepath.Join(root, "media/compose.yaml")}

	var ref agent.JobRef
	err := cl.Post(ctx, agent.PathCompose, req, &ref)

	var refused *agent.APIError
	if !errors.As(err, &refused) || refused.Code != agent.CodeOutOfRoots {
		t.Errorf("working dir /etc: %v, want %s", err, agent.CodeOutOfRoots)
	}
	if len(c.calls) != 0 {
		t.Errorf("ran %v", c.calls)
	}
}

// ddash reads every Agent's info every 30 s: the Compose plugin's version
// can't change while the Agent runs, so it is asked for once.
func TestInfoAsksForTheComposeVersionOnce(t *testing.T) {
	cl, _, _, c := setup(t)
	for range 3 {
		var info agent.Info
		if err := cl.Get(context.Background(), agent.PathInfo, nil, &info); err != nil {
			t.Fatal(err)
		}
		if info.Compose != "2.29.7" || info.Docker != "27.3.1" {
			t.Fatalf("info = %+v, want Compose 2.29.7 and Docker 27.3.1", info)
		}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.calls) != 1 {
		t.Errorf("docker compose ran %d times for three reads of the info (%v), want once", len(c.calls), c.calls)
	}
}

func TestAListingReadsEachComposeFileOnce(t *testing.T) {
	root := t.TempDir()
	for rel, content := range map[string]string{
		"media/compose.yaml":       "services:\n  jellyfin:\n    image: jellyfin/jellyfin:10.9\n",
		"tools/docker-compose.yml": "name: idle\nservices:\n  web:\n    image: nginx:1.27\n",
	} {
		p := filepath.Join(root, rel)
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte(content), 0o644)
	}
	reads := map[string]int{}
	a, err := dockeragent.New(dockeragent.Options{
		Roots: []string{root}, Docker: &docker{}, Compose: &compose{},
		ReadFile: func(name string) ([]byte, error) {
			reads[name]++
			return os.ReadFile(name)
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	res, err := a.List(context.Background())

	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, p := range res.Projects {
		names = append(names, p.Name)
		if len(p.Services) != 1 || p.Error != "" {
			t.Errorf("%s: services %v, error %q; want its one service", p.Name, p.Services, p.Error)
		}
	}
	if !slices.Equal(names, []string{"idle", "media"}) {
		t.Errorf("projects = %v, want idle and media", names)
	}
	if len(reads) != 2 {
		t.Errorf("read %v, want the two Compose files", reads)
	}
	for file, n := range reads {
		if n != 1 {
			t.Errorf("%s was read %d times in one listing, want once", file, n)
		}
	}
}
