package agentcall_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/itsKontra/ddash/agent"
	"github.com/itsKontra/ddash/components/internal/agentcall"
)

// serve runs a real Agent server of kind, enrolling with CODE.
func serve(t *testing.T, kind agent.Kind) (*agent.Server, string) {
	t.Helper()
	s, err := agent.NewServer(agent.ServerOptions{Kind: kind, Release: "v1.2.3", StateDir: t.TempDir(), EnrolmentCode: "CODE"})
	if err != nil {
		t.Fatal(err)
	}
	s.Handle("GET /v1/info", func(w http.ResponseWriter, _ *http.Request) {
		agent.WriteJSON(w, http.StatusOK, agent.Info{Kind: kind})
	})
	srv := httptest.NewUnstartedServer(s.Handler())
	srv.TLS = s.TLSConfig()
	srv.StartTLS()
	t.Cleanup(srv.Close)
	return s, srv.URL
}

func host(url, code string) agentcall.Pairing {
	return agentcall.Pairing{URL: url, Code: code, Kind: agent.KindHost, KindName: "Host Agent", IfPaired: "install it again"}
}

func TestPairReturnsTheTokenAndTheFingerprintToPin(t *testing.T) {
	ctx := context.Background()
	s, url := serve(t, agent.KindHost)

	token, fp, err := host(url, "CODE").Pair(ctx)

	if err != nil || token == "" || fp != s.Fingerprint() {
		t.Fatalf("Pair = token %q, fingerprint %q (the Agent's is %q), %v", token, fp, s.Fingerprint(), err)
	}
	var info agent.Info
	if err := agent.Dial(url, token, fp).Get(ctx, agent.PathInfo, nil, &info); err != nil {
		t.Errorf("a call with the token and the pinned fingerprint: %v", err)
	}
}

func TestPairRefusesTheWrongKindOfAgentAWrongCodeAndAnAgentPairedAlready(t *testing.T) {
	ctx := context.Background()
	_, dockerURL := serve(t, agent.KindDocker)
	if _, _, err := host(dockerURL, "CODE").Pair(ctx); err == nil || !strings.Contains(err.Error(), "answers as a docker Agent, not a Host Agent") {
		t.Errorf("a Docker Agent where a Host Agent is expected: %v", err)
	}

	s, url := serve(t, agent.KindHost)
	if _, _, err := host(url, "WRONG").Pair(ctx); err == nil || !strings.Contains(err.Error(), "pairing refused") {
		t.Errorf("a wrong enrolment code: %v", err)
	}
	if s.Paired() {
		t.Error("a wrong code paired the Agent")
	}

	if _, _, err := host(url, "CODE").Pair(ctx); err != nil {
		t.Fatal(err)
	}
	if _, _, err := host(url, "CODE").Pair(ctx); err == nil || !strings.Contains(err.Error(), "already paired") || !strings.Contains(err.Error(), "install it again") {
		t.Errorf("an Agent paired already: %v", err)
	}

	if _, _, err := host("https://127.0.0.1:1", "CODE").Pair(ctx); err == nil || !strings.Contains(err.Error(), "waiting for the Agent") {
		t.Errorf("no Agent there: %v", err)
	}
}

func TestStateOfTellsAChangedCertificateARefusedTokenAndSilenceApart(t *testing.T) {
	ctx := context.Background()
	s, url := serve(t, agent.KindHost)
	token, fp, err := host(url, "CODE").Pair(ctx)
	if err != nil {
		t.Fatal(err)
	}
	call := func(c *agent.Client) error {
		var info agent.Info
		return c.Get(ctx, agent.PathInfo, nil, &info)
	}

	other, _ := serve(t, agent.KindHost)
	if st, msg := agentcall.StateOf(call(agent.Dial(url, token, other.Fingerprint()))); st != agentcall.FingerprintChanged || !strings.Contains(msg, "pair it again") {
		t.Errorf("another certificate than the pinned one: %v, %q", st, msg)
	}
	if st, _ := agentcall.StateOf(call(agent.Dial(url, "not-the-token", fp))); st != agentcall.Refused {
		t.Errorf("a token the Agent doesn't know: %v, want Refused", st)
	}
	if st, msg := agentcall.StateOf(call(agent.Dial("https://127.0.0.1:1", token, s.Fingerprint()))); st != agentcall.Unreachable || !strings.HasPrefix(msg, "unreachable: ") {
		t.Errorf("no Agent there: %v, %q", st, msg)
	}
}

func TestStatusMapsAnAgentsAnswerToAGRPCCode(t *testing.T) {
	answer := func(code string) error { return &agent.APIError{Status: 400, Code: code, Message: "why"} }
	for _, tc := range []struct {
		err  error
		want codes.Code
	}{
		{answer(agent.CodeConflict), codes.Aborted},
		{answer(agent.CodeInvalid), codes.InvalidArgument},
		{answer(agent.CodeNotFound), codes.NotFound},
		{answer(agent.CodeOutOfRoots), codes.PermissionDenied},
		{answer(agent.CodeBusy), codes.FailedPrecondition},
		{answer(agent.CodeBadToken), codes.Unauthenticated},
		{answer(agent.CodeUnpaired), codes.Unauthenticated},
		{answer(agent.CodeFailed), codes.Internal},
		{agent.ErrFingerprint, codes.FailedPrecondition},
		{errors.New("connection refused"), codes.Unavailable},
	} {
		if got := status.Code(agentcall.Status(tc.err, "the Docker Agent", codes.Aborted)); got != tc.want {
			t.Errorf("%v: %s, want %s", tc.err, got, tc.want)
		}
	}
	// Each kind of Agent says what its own 409 means.
	if got := status.Code(agentcall.Status(answer(agent.CodeConflict), "the Host Agent", codes.FailedPrecondition)); got != codes.FailedPrecondition {
		t.Errorf("a Host Agent's conflict: %s", got)
	}
	if msg := status.Convert(agentcall.Status(errors.New("connection refused"), "the Host Agent", codes.Aborted)).Message(); msg != "the Host Agent: connection refused" {
		t.Errorf("an Agent that didn't answer reads %q", msg)
	}
}

func TestAJobIsReadThenCollectedAndAFailedOnesCollectSaysSo(t *testing.T) {
	ctx := context.Background()
	s, url := serve(t, agent.KindHost)
	token, fp, err := host(url, "CODE").Pair(ctx)
	if err != nil {
		t.Fatal(err)
	}
	c := agent.Dial(url, token, fp)
	run := func(fail error) string {
		t.Helper()
		j, err := s.Jobs().Start("packages.upgrade", "Update packages", "host", []string{"Upgrade"}, func(context.Context, *agent.Progress) (map[string]string, error) {
			return map[string]string{"updated": "3"}, fail
		})
		if err != nil {
			t.Fatal(err)
		}
		for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
			if got, err := agentcall.Job(ctx, c, j.ID); err == nil && got.State != agent.Running {
				return j.ID
			}
		}
		t.Fatal("the job never ended")
		return ""
	}

	id := run(nil)
	got, err := agentcall.Job(ctx, c, id)
	if err != nil || got.State != agent.Done || got.Result["updated"] != "3" {
		t.Fatalf("Job = %+v, %v", got, err)
	}
	if j, failed, err := agentcall.Collect(ctx, c, id); err != nil || failed != nil || j.ID != id {
		t.Fatalf("Collect = %+v, %v, %v", j, failed, err)
	}
	if _, err := agentcall.Job(ctx, c, id); agent.CodeOf(err) != agent.CodeNotFound {
		t.Errorf("a collected job: %v, want not found", err)
	}

	id = run(errors.New("dpkg was interrupted"))
	if _, failed, err := agentcall.Collect(ctx, c, id); err != nil || status.Code(failed) != codes.Aborted || !strings.Contains(status.Convert(failed).Message(), "dpkg was interrupted") {
		t.Errorf("collecting a failed job: failed = %v, err = %v", failed, err)
	}
}
