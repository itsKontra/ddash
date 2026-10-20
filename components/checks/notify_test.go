package checks_test

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/google/go-cmp/cmp"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/testing/protocmp"

	"github.com/itsKontra/ddash/components/checks"
	"github.com/itsKontra/ddash/components/checks/checkspb"
	"github.com/itsKontra/ddash/components/ntfy-api/ntfyapipb"
	"github.com/itsKontra/ddash/sdk/sdktest"
)

// fakeNtfy is ntfy-api as checks sees it: it records the Notifications
// handed to Send, and fails them while down.
type fakeNtfy struct {
	ntfyapipb.NtfyApiServiceClient

	mu       sync.Mutex
	sent     []*ntfyapipb.Notification
	attempts []time.Time
	down     bool
}

func (f *fakeNtfy) Send(_ context.Context, req *ntfyapipb.SendRequest, _ ...grpc.CallOption) (*ntfyapipb.SendResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.attempts = append(f.attempts, time.Now())
	if f.down {
		return nil, status.Error(codes.Unavailable, "ntfy answered 502: bad gateway")
	}
	f.sent = append(f.sent, req.GetNotification())
	return &ntfyapipb.SendResponse{Id: strconv.Itoa(len(f.sent))}, nil
}

func (f *fakeNtfy) setDown(down bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.down = down
}

// notifications returns what was delivered so far.
func (f *fakeNtfy) notifications() []*ntfyapipb.Notification {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]*ntfyapipb.Notification(nil), f.sent...)
}

func (f *fakeNtfy) tries() []time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]time.Time(nil), f.attempts...)
}

func titles(ns []*ntfyapipb.Notification) []string {
	var out []string
	for _, n := range ns {
		out = append(out, n.GetTitle())
	}
	return out
}

func named(name, target string) *checkspb.CheckSpec {
	return &checkspb.CheckSpec{Name: name, Type: checkspb.CheckType_CHECK_TYPE_TCP, Target: target}
}

// nextBeat lets time pass until Check id was probed once more.
func nextBeat(t *testing.T, c checkspb.ChecksServiceClient, id int64) {
	t.Helper()
	last := find(t, c, id).GetLastBeat().AsTime()
	for range Interval11() / time.Second {
		after(time.Second)
		if find(t, c, id).GetLastBeat().AsTime().After(last) {
			return
		}
	}
	t.Fatalf("Check %d wasn't probed within %v", id, Interval11())
}

// tick11 is the longest the delivery loop takes to come round.
func tick11() time.Duration { return checks.DeliveryTick + checks.DeliveryTick/10 }

// goDown lets addr refuse until the Check is confirmed down.
func goDown(l *lab, addr string) {
	l.set(addr, "refuse")
	after(checks.DownAfter * Interval11())
}

func TestOneNotificationOnConfirmedDownAndOneOnRecovery(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		l := newLab()
		l.set("nas:22", "up")
		_, c := startIn(t, l)
		id := create(t, c, named("NAS", "nas:22")).GetId()
		synctest.Wait()

		l.set("nas:22", "refuse")
		after(2 * Interval11())
		if got := l.ntfy.notifications(); len(got) != 0 {
			t.Fatalf("sent %v while pending, want nothing before down is confirmed", titles(got))
		}
		nextBeat(t, c, id)
		if got := find(t, c, id).GetState(); got != checkspb.State_STATE_DOWN {
			t.Fatalf("after 3 failures: %v, want down", got)
		}
		nextBeat(t, c, id)
		nextBeat(t, c, id)

		click := fmt.Sprintf("%s/checks/%d", sdktest.PublicURL, id)
		want := []*ntfyapipb.Notification{{Title: "NAS down", Message: "connection refused", Priority: 4, Tags: []string{"red_circle"}, Click: click}}
		if diff := cmp.Diff(want, l.ntfy.notifications(), protocmp.Transform()); diff != "" {
			t.Fatalf("after 2 more down beats (-want +got):\n%s", diff)
		}

		l.set("nas:22", "up")
		nextBeat(t, c, id)
		after(tick11())

		// Up 3 beats after it went down: 180 to 198 s.
		want = append(want, &ntfyapipb.Notification{Title: "NAS up again", Message: "Down for 3 min (connection refused)", Priority: 3, Tags: []string{"green_circle"}, Click: click})
		if diff := cmp.Diff(want, l.ntfy.notifications(), protocmp.Transform()); diff != "" {
			t.Errorf("after recovering (-want +got):\n%s", diff)
		}
		after(10 * Interval11())
		if n := len(l.ntfy.notifications()); n != 2 {
			t.Errorf("%d Notifications, want no more while up", n)
		}
	})
}

func TestACheckThatWasNeverDownSendsNothingWhenItComesUp(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		l := newLab()
		_, c := startIn(t, l)
		create(t, c, named("NAS", "nas:22"))
		after(Interval11()) // failed at once and once more: pending
		l.set("nas:22", "up")
		after(Interval11())

		if got := l.ntfy.notifications(); len(got) != 0 {
			t.Errorf("sent %v, want nothing: it was pending, never down", titles(got))
		}
	})
}

func TestAMutedCheckSendsNoNotification(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		l := newLab()
		l.set("nas:22", "up")
		_, c := startIn(t, l)
		id := create(t, c, named("NAS", "nas:22")).GetId()
		synctest.Wait()
		if _, err := c.SetMuted(t.Context(), &checkspb.SetMutedRequest{Id: id, Muted: true}); err != nil {
			t.Fatal(err)
		}

		goDown(l, "nas:22")
		l.set("nas:22", "up")
		after(Interval11())

		if got := l.ntfy.notifications(); len(got) != 0 {
			t.Errorf("sent %v for a muted Check, want nothing", titles(got))
		}
	})
}

func TestTheLastNotifiedStateSurvivesARestart(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		l := newLab()
		l.set("nas:22", "up")
		h, c := startIn(t, l)
		create(t, c, named("NAS", "nas:22"))
		synctest.Wait()
		goDown(l, "nas:22")
		synctest.Wait()

		h.Stop()
		after(5 * time.Minute)
		startIn(t, l, sdktest.WithDB(h.DBPath))
		after(3 * Interval11())
		if got := titles(l.ntfy.notifications()); !cmp.Equal(got, []string{"NAS down"}) {
			t.Fatalf("sent %v across the restart, want the one down", got)
		}

		l.set("nas:22", "up")
		after(Interval11())
		if got := titles(l.ntfy.notifications()); !cmp.Equal(got, []string{"NAS down", "NAS up again"}) {
			t.Errorf("sent %v, want the recovery after the restart", got)
		}
	})
}

func TestEachCheckIsNotifiedOnItsOwn(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		l := newLab()
		_, c := startIn(t, l)
		create(t, c, named("NAS", "nas:22"))
		create(t, c, named("NVR", "nvr:554"))
		l.set("nas:22", "refuse")
		l.set("nvr:554", "refuse")
		after(checks.DownAfter * Interval11())

		got := titles(l.ntfy.notifications())
		slices.Sort(got)
		if !cmp.Equal(got, []string{"NAS down", "NVR down"}) {
			t.Errorf("sent %v, want one down per Check", got)
		}
	})
}

func TestAnUnsentNotificationIsRetriedWithBackoff(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		l := newLab()
		l.set("nas:22", "up")
		_, c := startIn(t, l)
		create(t, c, named("NAS", "nas:22"))
		synctest.Wait()
		l.ntfy.setDown(true)

		goDown(l, "nas:22")
		after(20 * time.Minute)
		tries := l.ntfy.tries()
		if len(tries) < 3 {
			t.Fatalf("%d attempts in 20 min, want retries", len(tries))
		}
		for i := 2; i < len(tries); i++ {
			if gap, last := tries[i].Sub(tries[i-1]), tries[i-1].Sub(tries[i-2]); gap < last && gap < checks.RetryMax {
				t.Errorf("attempt %d came %v after the last, sooner than the gap before (%v): want backoff", i, gap, last)
			}
		}
		if gap := tries[len(tries)-1].Sub(tries[len(tries)-2]); gap > checks.RetryMax+tick11() {
			t.Errorf("retry gap %v, want at most %v", gap, checks.RetryMax)
		}

		l.ntfy.setDown(false)
		after(checks.RetryMax + tick11())
		if got := titles(l.ntfy.notifications()); !cmp.Equal(got, []string{"NAS down"}) {
			t.Errorf("sent %v once ntfy works again, want the down", got)
		}
	})
}

func TestTheQueueIsCollapsedToTheCurrentStateBeforeEachAttempt(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		l := newLab()
		l.set("nas:22", "up")
		l.set("nvr:554", "up")
		_, c := startIn(t, l)
		create(t, c, named("NAS", "nas:22"))
		create(t, c, named("NVR", "nvr:554"))
		synctest.Wait()
		l.ntfy.setDown(true)

		// NAS goes down and comes back; NVR goes down and stays down.
		l.set("nvr:554", "refuse")
		goDown(l, "nas:22")
		l.set("nas:22", "up")
		after(Interval11())

		l.ntfy.setDown(false)
		after(checks.RetryMax + tick11())
		if got := titles(l.ntfy.notifications()); !cmp.Equal(got, []string{"NVR down"}) {
			t.Errorf("sent %v, want only NVR's down: NAS is back where it was last notified", got)
		}
	})
}

func TestAnUnsentNotificationIsDroppedAfterAnHour(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		l := newLab()
		l.set("nas:22", "up")
		_, c := startIn(t, l)
		create(t, c, named("NAS", "nas:22"))
		synctest.Wait()
		l.ntfy.setDown(true)

		goDown(l, "nas:22")
		after(checks.GiveUpAfter)
		l.ntfy.setDown(false)
		before := len(l.ntfy.tries())
		after(time.Hour)

		if got := l.ntfy.notifications(); len(got) != 0 {
			t.Errorf("sent %v after an hour of failures, want it dropped", titles(got))
		}
		if n := len(l.ntfy.tries()); n != before {
			t.Errorf("%d more attempts after the hour, want none", n-before)
		}
		// Never notified down, so its recovery sends nothing either.
		l.set("nas:22", "up")
		after(Interval11())
		if got := l.ntfy.notifications(); len(got) != 0 {
			t.Errorf("sent %v on recovery, want nothing", titles(got))
		}
	})
}

func TestADeletedChecksQueuedNotificationIsDropped(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		l := newLab()
		l.set("nas:22", "up")
		_, c := startIn(t, l)
		id := create(t, c, named("NAS", "nas:22")).GetId()
		synctest.Wait()
		l.ntfy.setDown(true)
		goDown(l, "nas:22")

		if _, err := c.DeleteCheck(t.Context(), &checkspb.DeleteCheckRequest{Id: id}); err != nil {
			t.Fatal(err)
		}
		l.ntfy.setDown(false)
		after(checks.RetryMax + tick11())

		if got := l.ntfy.notifications(); len(got) != 0 {
			t.Errorf("sent %v for a deleted Check, want nothing", titles(got))
		}
	})
}
