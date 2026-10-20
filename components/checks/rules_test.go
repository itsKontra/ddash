package checks_test

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"syscall"
	"testing"
	"testing/synctest"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/itsKontra/ddash/components/checks"
	"github.com/itsKontra/ddash/components/checks/checkspb"
	"github.com/itsKontra/ddash/components/ntfy-api/ntfyapipb"
	"github.com/itsKontra/ddash/sdk/sdktest"
)

// lab fakes the network the probes dial, so time rules run in a synctest
// bubble: each address accepts, refuses or never answers. "up 20ms"
// accepts after 20 ms. "http 502" answers one HTTP request with that
// status. Notifications go to its fake ntfy-api.
type lab struct {
	mu    sync.Mutex
	hosts map[string]string // addr → "up", "up <duration>", "http <status>", "refuse" or "hang"
	ntfy  *fakeNtfy
}

func newLab() *lab { return &lab{hosts: map[string]string{}, ntfy: &fakeNtfy{}} }

func (l *lab) set(addr, how string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.hosts[addr] = how
}

func (l *lab) dial(ctx context.Context, _, addr string) (net.Conn, error) {
	l.mu.Lock()
	how := l.hosts[addr]
	l.mu.Unlock()
	how, arg, _ := strings.Cut(how, " ")
	if d, err := time.ParseDuration(arg); err == nil {
		time.Sleep(d)
	}
	switch how {
	case "http":
		client, server := net.Pipe()
		go answerHTTP(server, arg)
		return client, nil
	case "up":
		client, server := net.Pipe()
		server.Close()
		return client, nil
	case "hang":
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return nil, &net.OpError{Op: "dial", Net: "tcp", Err: syscall.ECONNREFUSED}
}

// answerHTTP answers one HTTP request on conn with status, then hangs up.
func answerHTTP(conn net.Conn, status string) {
	defer conn.Close()
	if _, err := http.ReadRequest(bufio.NewReader(conn)); err != nil {
		return
	}
	fmt.Fprintf(conn, "HTTP/1.1 %s Status\r\nContent-Length: 0\r\nConnection: close\r\n\r\n", status)
}

// startIn runs checks dialing through l. Call inside a synctest bubble.
func startIn(t *testing.T, l *lab, opts ...sdktest.Option) (*sdktest.Harness, checkspb.ChecksServiceClient) {
	t.Helper()
	opts = append([]sdktest.Option{sdktest.WithClient[ntfyapipb.NtfyApiServiceClient](l.ntfy)}, opts...)
	h := sdktest.New(t, checks.DefinitionDialing(l.dial), opts...)
	h.MustStart(&checkspb.ChecksConfig{})
	return h, checkspb.NewChecksServiceClient(h.Conn)
}

func tcp(target string) *checkspb.CheckSpec {
	return &checkspb.CheckSpec{Type: checkspb.CheckType_CHECK_TYPE_TCP, Target: target}
}

// after lets d pass in the bubble and waits for the Component to settle.
func after(d time.Duration) {
	time.Sleep(d)
	synctest.Wait()
}

// beat is one recorded beat.
type beat struct {
	at    time.Time
	up    bool
	state string
}

// beats reads Check id's kept beats through ListBeats, oldest first. A
// deleted Check has none.
func beats(t *testing.T, c checkspb.ChecksServiceClient, id int64) []beat {
	t.Helper()
	resp, err := c.ListBeats(t.Context(), &checkspb.ListBeatsRequest{Id: id})
	if status.Code(err) == codes.NotFound {
		return nil
	}
	if err != nil {
		t.Fatalf("ListBeats: %v", err)
	}
	var out []beat
	for _, b := range resp.GetBeats() {
		state := strings.ToLower(strings.TrimPrefix(b.GetState().String(), "STATE_"))
		out = append(out, beat{at: b.GetAt().AsTime(), up: b.GetUp(), state: state})
	}
	return out
}

func states(bs []beat) []string {
	var out []string
	for _, b := range bs {
		out = append(out, b.state)
	}
	return out
}

func TestACheckIsPendingThenDownAfterThreeFailuresAndUpAgainOnOneSuccess(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		l := newLab()
		l.set("nas:22", "up")
		_, c := startIn(t, l)
		id := create(t, c, tcp("nas:22")).GetId()
		synctest.Wait()
		if got := find(t, c, id).GetState(); got != checkspb.State_STATE_UP {
			t.Fatalf("after its first beat: %v, want up", got)
		}

		l.set("nas:22", "refuse")
		for range 2 {
			after(Interval11())
			if ch := find(t, c, id); ch.GetState() != checkspb.State_STATE_PENDING {
				t.Fatalf("after %d failures: %v, want pending", ch.GetFailures(), ch.GetState())
			}
		}
		after(Interval11())
		down := find(t, c, id)
		if down.GetState() != checkspb.State_STATE_DOWN || down.GetFailures() != 3 || down.GetReason() != "connection refused" {
			t.Fatalf("after 3 failures: %v, %d failures (%q), want down: connection refused", down.GetState(), down.GetFailures(), down.GetReason())
		}
		if !down.GetSince().AsTime().Equal(down.GetLastBeat().AsTime()) {
			t.Errorf("down since %v, want the third failure's beat %v", down.GetSince().AsTime(), down.GetLastBeat().AsTime())
		}

		after(Interval11())
		if got := find(t, c, id).GetState(); got != checkspb.State_STATE_DOWN {
			t.Errorf("a 4th failure: %v, want still down", got)
		}

		l.set("nas:22", "up")
		after(Interval11())
		if got := find(t, c, id).GetState(); got != checkspb.State_STATE_UP {
			t.Errorf("one success: %v, want up", got)
		}

		want := []string{"up", "pending", "pending", "down", "down", "up"}
		if got := states(beats(t, c, id)); !equal(got, want) {
			t.Errorf("check_event states %v, want %v", got, want)
		}
	})
}

// Interval11 is the longest a beat can take to come: the interval plus
// sdk.Every's jitter of up to 10%.
func Interval11() time.Duration { return checks.Interval + checks.Interval/10 }

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestANewCheckIsPendingUntilItsFirstSuccess(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		l := newLab()
		_, c := startIn(t, l)

		ch := create(t, c, tcp("nas:22"))
		if ch.GetState() != checkspb.State_STATE_PENDING || ch.GetLastBeat() != nil {
			t.Errorf("created: %v, last beat %v; want pending, never probed", ch.GetState(), ch.GetLastBeat())
		}
		synctest.Wait()
		if got := find(t, c, ch.GetId()); got.GetState() != checkspb.State_STATE_PENDING || got.GetFailures() != 1 {
			t.Errorf("after a failure: %v with %d failures, want pending with 1", got.GetState(), got.GetFailures())
		}
	})
}

func TestBeatsComeEveryMinute(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		l := newLab()
		l.set("nas:22", "up")
		_, c := startIn(t, l)
		id := create(t, c, tcp("nas:22")).GetId()

		after(10*time.Minute + time.Second)

		bs := beats(t, c, id)
		if len(bs) < 10 || len(bs) > 11 {
			t.Fatalf("%d beats in 10 min, want one a minute (plus jitter)", len(bs))
		}
		for i := 1; i < len(bs); i++ {
			if gap := bs[i].at.Sub(bs[i-1].at); gap < checks.Interval || gap > Interval11() {
				t.Errorf("beat %d came %v after the last, want 60 s plus up to 10%%", i, gap)
			}
		}
	})
}

func TestAProbeTimesOutAfterTenSeconds(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		l := newLab()
		l.set("nas:22", "hang")
		_, c := startIn(t, l)
		created := time.Now()
		id := create(t, c, tcp("nas:22")).GetId()

		after(checks.Timeout - time.Millisecond)
		if got := find(t, c, id).GetLastBeat(); got != nil {
			t.Fatalf("a beat was recorded before the timeout, at %v", got.AsTime())
		}
		after(time.Millisecond)
		ch := find(t, c, id)
		if ch.GetReason() != "timeout" || ch.GetFailures() != 1 {
			t.Errorf("%v, %d failures (%q), want a failure: timeout", ch.GetState(), ch.GetFailures(), ch.GetReason())
		}
		if took := ch.GetLastBeat().AsTime().Sub(created); took != checks.Timeout {
			t.Errorf("the beat failed after %v, want %v", took, checks.Timeout)
		}
	})
}

func TestARestartResumesFromTheLastStateLeavingAGap(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		l := newLab()
		l.set("nas:22", "refuse")
		l.set("nvr:554", "refuse")
		h, c := startIn(t, l)
		nas := create(t, c, tcp("nas:22")).GetId()
		after(3 * Interval11())
		nvr := create(t, c, tcp("nvr:554")).GetId()
		after(Interval11())
		if a, b := find(t, c, nas).GetState(), find(t, c, nvr).GetState(); a != checkspb.State_STATE_DOWN || b != checkspb.State_STATE_PENDING {
			t.Fatalf("before the restart: nas %v, nvr %v; want down and pending", a, b)
		}
		lastBefore := beats(t, c, nas)
		downSince := find(t, c, nas).GetSince().AsTime()

		h.Stop()
		after(30 * time.Minute)
		restarted := time.Now()
		_, c2 := startIn(t, l, sdktest.WithDB(h.DBPath))

		if ch := find(t, c2, nas); ch.GetState() != checkspb.State_STATE_DOWN || !ch.GetSince().AsTime().Equal(downSince) {
			t.Errorf("nas after the restart: %v since %v, want still down since %v", ch.GetState(), ch.GetSince().AsTime(), downSince)
		}
		synctest.Wait()
		// nvr had failed twice: its next failure makes it down.
		if ch := find(t, c2, nvr); ch.GetState() != checkspb.State_STATE_DOWN || ch.GetFailures() != 3 {
			t.Errorf("nvr after a 3rd failure across the restart: %v with %d failures, want down with 3", ch.GetState(), ch.GetFailures())
		}

		all := beats(t, c2, nas)
		next := all[len(lastBefore)]
		if !next.at.Equal(restarted.Truncate(time.Millisecond)) {
			t.Errorf("first beat after the restart at %v, want at once (%v)", next.at, restarted)
		}
		if gap := next.at.Sub(lastBefore[len(lastBefore)-1].at); gap < 30*time.Minute {
			t.Errorf("gap %v between beats across the restart, want the downtime left empty", gap)
		}
	})
}

func TestARestartWithinTheIntervalWaitsForTheNextDueBeat(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		l := newLab()
		l.set("nas:22", "up")
		h, c := startIn(t, l)
		id := create(t, c, tcp("nas:22")).GetId()
		synctest.Wait()
		first := find(t, c, id).GetLastBeat().AsTime()

		h.Stop()
		after(20 * time.Second)
		_, c2 := startIn(t, l, sdktest.WithDB(h.DBPath))
		after(time.Minute)

		bs := beats(t, c2, id)
		if len(bs) != 2 || !bs[1].at.Equal(first.Add(checks.Interval)) {
			t.Errorf("beats at %v, want the second 60 s after the first (%v)", bs, first.Add(checks.Interval))
		}
	})
}

func TestADisabledCheckIsNotProbedAndResumesWhenEnabled(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		l := newLab()
		l.set("nas:22", "up")
		_, c := startIn(t, l)
		id := create(t, c, tcp("nas:22")).GetId()
		synctest.Wait()

		res, err := c.SetDisabled(t.Context(), &checkspb.SetDisabledRequest{Id: id, Disabled: true})
		if err != nil || !res.GetCheck().GetDisabled() {
			t.Fatalf("SetDisabled = %v, %v", res, err)
		}
		after(10 * time.Minute)
		if n := len(beats(t, c, id)); n != 1 {
			t.Errorf("%d beats, want none while disabled", n-1)
		}

		if _, err := c.SetDisabled(t.Context(), &checkspb.SetDisabledRequest{Id: id}); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		if ch := find(t, c, id); ch.GetDisabled() || ch.GetState() != checkspb.State_STATE_UP || len(beats(t, c, id)) != 2 {
			t.Errorf("enabled again: disabled %v, %v, %d beats; want probed at once and up", ch.GetDisabled(), ch.GetState(), len(beats(t, c, id)))
		}
	})
}

func TestChangingATargetStartsTheCheckOver(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		l := newLab()
		l.set("nas:22", "refuse")
		l.set("nas:2222", "up")
		_, c := startIn(t, l)
		id := create(t, c, tcp("nas:22")).GetId()
		after(3 * Interval11())

		renamed, err := c.UpdateCheck(t.Context(), &checkspb.UpdateCheckRequest{Id: id, Spec: &checkspb.CheckSpec{Name: "NAS ssh", Type: checkspb.CheckType_CHECK_TYPE_TCP, Target: "nas:22"}})
		if err != nil || renamed.GetCheck().GetState() != checkspb.State_STATE_DOWN || renamed.GetCheck().GetSpec().GetName() != "NAS ssh" {
			t.Fatalf("renamed: %v, %v; want still down, named NAS ssh", renamed, err)
		}

		moved, err := c.UpdateCheck(t.Context(), &checkspb.UpdateCheckRequest{Id: id, Spec: &checkspb.CheckSpec{Name: "NAS ssh", Type: checkspb.CheckType_CHECK_TYPE_TCP, Target: "nas:2222"}})
		if err != nil || moved.GetCheck().GetState() != checkspb.State_STATE_PENDING || moved.GetCheck().GetFailures() != 0 {
			t.Fatalf("moved: %v, %v; want pending with no failures", moved, err)
		}
		synctest.Wait()
		if got := find(t, c, id).GetState(); got != checkspb.State_STATE_UP {
			t.Errorf("after moving: %v, want up, probed at once", got)
		}
	})
}

func TestDeletingACheckStopsItAndDropsItsHistory(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		l := newLab()
		l.set("nas:22", "up")
		_, c := startIn(t, l)
		id := create(t, c, tcp("nas:22")).GetId()
		synctest.Wait()

		if _, err := c.DeleteCheck(t.Context(), &checkspb.DeleteCheckRequest{Id: id}); err != nil {
			t.Fatal(err)
		}
		after(5 * time.Minute)

		if n := len(list(t, c)); n != 0 {
			t.Errorf("%d Checks listed, want none", n)
		}
		if n := len(beats(t, c, id)); n != 0 {
			t.Errorf("%d beats kept, want none", n)
		}
		_, err := c.DeleteCheck(t.Context(), &checkspb.DeleteCheckRequest{Id: id})
		if status.Code(err) != codes.NotFound {
			t.Errorf("deleting it again: %v, want NotFound", err)
		}
	})
}

func TestMutingIsKeptAcrossARestart(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		l := newLab()
		h, c := startIn(t, l)
		id := create(t, c, tcp("nas:22")).GetId()

		res, err := c.SetMuted(t.Context(), &checkspb.SetMutedRequest{Id: id, Muted: true})
		if err != nil || !res.GetCheck().GetMuted() {
			t.Fatalf("SetMuted = %v, %v", res, err)
		}
		h.Stop()
		_, c2 := startIn(t, l, sdktest.WithDB(h.DBPath))

		if !find(t, c2, id).GetMuted() {
			t.Error("muted was lost in the restart")
		}
	})
}
