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

// compose records every call; config fails when the file says "bad:".
type compose struct {
	mu    sync.Mutex
	calls [][]string
}

func (c *compose) Run(_ context.Context, _ string, args []string, out func(string)) error {
	c.mu.Lock()
	c.calls = append(c.calls, args)
	c.mu.Unlock()
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
