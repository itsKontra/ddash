package agent_test

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/itsKontra/ddash/agent"
)

// serve runs an Agent server over real TLS, as ddash reaches one.
func serve(t *testing.T, dir, code string) (*agent.Server, string) {
	t.Helper()
	s, err := agent.NewServer(agent.ServerOptions{Kind: agent.KindHost, Release: "v1.2.3", StateDir: dir, EnrolmentCode: code})
	if err != nil {
		t.Fatal(err)
	}
	s.Handle("GET /v1/secret", func(w http.ResponseWriter, _ *http.Request) {
		agent.WriteJSON(w, http.StatusOK, map[string]string{"ok": "yes"})
	})
	srv := httptest.NewUnstartedServer(s.Handler())
	srv.TLS = s.TLSConfig()
	srv.StartTLS()
	t.Cleanup(srv.Close)
	return s, srv.URL
}

func TestPairing(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s, url := serve(t, dir, "K7QX-M2PA-9RTD-HF4C")

	hello, fp, err := agent.Probe(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	if hello.Paired || hello.Kind != agent.KindHost || hello.Release != "v1.2.3" {
		t.Fatalf("hello = %+v", hello)
	}
	if fp != s.Fingerprint() {
		t.Fatalf("probed fingerprint %s, the Agent's is %s", fp, s.Fingerprint())
	}

	// Before pairing, operations are refused.
	var out map[string]string
	if err := agent.Dial(url, "x", fp).Get(ctx, "/v1/secret", nil, &out); agent.CodeOf(err) != agent.CodeUnpaired {
		t.Fatalf("unpaired call: %v", err)
	}
	if _, _, err := agent.Pair(ctx, url, fp, "WRONG-CODE"); agent.CodeOf(err) != agent.CodeBadCode {
		t.Fatalf("wrong code: %v", err)
	}
	// Dashes, spaces and case don't matter.
	token, _, err := agent.Pair(ctx, url, fp, " k7qxm2pa-9rtd hf4c ")
	if err != nil {
		t.Fatal(err)
	}
	if err := agent.Dial(url, token, fp).Get(ctx, "/v1/secret", nil, &out); err != nil || out["ok"] != "yes" {
		t.Fatalf("paired call: %v %v", out, err)
	}
	if err := agent.Dial(url, "other", fp).Get(ctx, "/v1/secret", nil, &out); agent.CodeOf(err) != agent.CodeBadToken {
		t.Fatalf("wrong token: %v", err)
	}
	// The code is one-time: a second pairing is refused.
	if _, _, err := agent.Pair(ctx, url, fp, "K7QX-M2PA-9RTD-HF4C"); agent.CodeOf(err) != agent.CodePaired {
		t.Fatalf("second pairing: %v", err)
	}
	// A pinned fingerprint that isn't the Agent's is refused.
	if err := agent.Dial(url, token, "00"+fp[2:]).Get(ctx, "/v1/secret", nil, &out); !errors.Is(err, agent.ErrFingerprint) {
		t.Fatalf("other fingerprint: %v", err)
	}

	// A restarted Agent keeps its certificate and pairing.
	s2, url2 := serve(t, dir, "")
	if s2.Fingerprint() != fp || !s2.Paired() {
		t.Fatalf("after restart: fingerprint %s paired %v", s2.Fingerprint(), s2.Paired())
	}
	if err := agent.Dial(url2, token, fp).Get(ctx, "/v1/secret", nil, &out); err != nil {
		t.Fatal(err)
	}
}

func TestPairingLimitsWrongCodes(t *testing.T) {
	ctx := context.Background()
	_, url := serve(t, t.TempDir(), "RIGHT")
	_, fp, _ := agent.Probe(ctx, url)
	for range 10 {
		if _, _, err := agent.Pair(ctx, url, fp, "WRONG"); agent.CodeOf(err) != agent.CodeBadCode {
			t.Fatal(err)
		}
	}
	if _, _, err := agent.Pair(ctx, url, fp, "RIGHT"); agent.CodeOf(err) != agent.CodeTooMany {
		t.Fatalf("after 10 wrong codes: %v", err)
	}
}

func TestJobs(t *testing.T) {
	jobs := agent.NewJobs(nil)
	release := make(chan struct{})
	job, err := jobs.Start("compose.update", "Update media", "media", []string{"Pull", "Recreate"}, func(_ context.Context, p *agent.Progress) (map[string]string, error) {
		p.Begin(0)
		p.Log(0, "pulling jellyfin\npulled jellyfin\n")
		p.Done(0, "1 image changed")
		p.Begin(1)
		<-release
		return nil, errors.New("port 8096 is taken")
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := jobs.Start("compose.restart", "Restart media", "media", nil, nil); !errors.Is(err, agent.ErrBusy) {
		t.Fatalf("second job on the target: %v", err)
	}
	if _, err := jobs.Collect(job.ID); err == nil {
		t.Fatal("collected a running job")
	}
	close(release)
	var got agent.Job
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		if got, _ = jobs.Get(job.ID); got.State.Ended() {
			break
		}
	}
	if got.State != agent.Failed || got.Error != "port 8096 is taken" {
		t.Fatalf("job = %+v", got)
	}
	if got.Steps[0].State != agent.Done || len(got.Steps[0].Log) != 2 || got.Steps[1].State != agent.Failed {
		t.Fatalf("steps = %+v", got.Steps)
	}
	if _, err := jobs.Collect(job.ID); err != nil {
		t.Fatal(err)
	}
	if len(jobs.List()) != 0 {
		t.Fatal("a collected job is still listed")
	}
}

// A Component dials an Agent anew for every call: the calls share the
// Agent's connection, so each one isn't a TLS handshake of its own.
func TestCallsToOneAgentShareItsConnection(t *testing.T) {
	ctx := context.Background()
	s, err := agent.NewServer(agent.ServerOptions{Kind: agent.KindHost, Release: "v1.2.3", StateDir: t.TempDir(), EnrolmentCode: "CODE"})
	if err != nil {
		t.Fatal(err)
	}
	s.Handle("GET /v1/secret", func(w http.ResponseWriter, _ *http.Request) {
		agent.WriteJSON(w, http.StatusOK, map[string]string{"ok": "yes"})
	})
	var connections atomic.Int32
	srv := httptest.NewUnstartedServer(s.Handler())
	srv.TLS = s.TLSConfig()
	srv.Config.ConnState = func(_ net.Conn, st http.ConnState) {
		if st == http.StateNew {
			connections.Add(1)
		}
	}
	srv.StartTLS()
	t.Cleanup(srv.Close)
	token, _, err := agent.Pair(ctx, srv.URL, s.Fingerprint(), "CODE")
	if err != nil {
		t.Fatal(err)
	}
	before := connections.Load()

	for range 5 {
		var out map[string]string
		if err := agent.Dial(srv.URL, token, s.Fingerprint()).Get(ctx, "/v1/secret", nil, &out); err != nil {
			t.Fatal(err)
		}
	}

	if got := connections.Load() - before; got > 1 {
		t.Errorf("5 calls opened %d connections, want at most 1", got)
	}
}

// A command that hangs (a pull from a registry that stopped answering)
// must not hold its target busy until the Agent restarts.
func TestAJobThatNeverEndsIsStoppedByItsTimeoutAndFails(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		jobs := agent.NewJobs(nil)
		heedsCtx, err := jobs.Start("compose.pull", "Pull media", "media", []string{"Pull"}, func(ctx context.Context, p *agent.Progress) (map[string]string, error) {
			p.Begin(0)
			<-ctx.Done() // as a command run with ctx does when it is killed
			return nil, ctx.Err()
		})
		if err != nil {
			t.Fatal(err)
		}
		release := make(chan struct{})
		defer close(release)
		ignoresCtx, err := jobs.Start("packages.upgrade", "Update packages", "host", []string{"Install"}, func(_ context.Context, p *agent.Progress) (map[string]string, error) {
			p.Begin(0)
			<-release
			p.Done(0, "too late") // after the job was given up on
			return nil, nil
		})
		if err != nil {
			t.Fatal(err)
		}

		time.Sleep(agent.JobTimeout - time.Minute)
		synctest.Wait()
		if j, _ := jobs.Get(heedsCtx.ID); j.State != agent.Running {
			t.Fatalf("a minute before the timeout the job is %s, want still running", j.State)
		}
		time.Sleep(2 * time.Minute)
		synctest.Wait()

		for _, id := range []string{heedsCtx.ID, ignoresCtx.ID} {
			j, _ := jobs.Get(id)
			if j.State != agent.Failed || !strings.Contains(j.Error, "didn't end within 2h") || j.Steps[0].State != agent.Failed {
				t.Errorf("%s after the timeout: %s, error %q, step %s; want failed, saying it was stopped", j.Title, j.State, j.Error, j.Steps[0].State)
			}
		}
		if _, err := jobs.Start("compose.up", "Start media", "media", nil, func(context.Context, *agent.Progress) (map[string]string, error) { return nil, nil }); err != nil {
			t.Errorf("a new job on the target after the timeout: %v", err)
		}
	})
}
