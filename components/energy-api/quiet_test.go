package energyapi_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"

	energyapi "github.com/itsKontra/ddash/components/energy-api"
	pb "github.com/itsKontra/ddash/components/energy-api/energyapipb"
	"github.com/itsKontra/ddash/components/energy-api/energyfake"
	notificationpb "github.com/itsKontra/ddash/proto/ddash/notification"
	"github.com/itsKontra/ddash/sdk"
	"github.com/itsKontra/ddash/sdk/sdktest"
)

// clock is a test's clock, moved by hand.
type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// fakeNotify is the core's notification service: it records what is sent.
type fakeNotify struct {
	notificationpb.NotificationServiceClient

	mu   sync.Mutex
	sent []*notificationpb.SendRequest
}

func (f *fakeNotify) Send(_ context.Context, req *notificationpb.SendRequest, _ ...grpc.CallOption) (*notificationpb.SendResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, proto.CloneOf(req))
	return &notificationpb.SendResponse{}, nil
}

// said is each Notification sent so far as "title: message".
func (f *fakeNotify) said() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, r := range f.sent {
		out = append(out, r.GetNotification().GetTitle()+": "+r.GetNotification().GetMessage())
	}
	return out
}

func (f *fakeNotify) first() *notificationpb.SendRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.sent[0]
}

// watched is energy-api reading a fake with opts on a clock that starts
// at now, sending its Notifications to the returned fake.
func watched(t *testing.T, opts energyfake.Options) (*clock, *sdktest.Harness, *fakeNotify) {
	t.Helper()
	clk := &clock{t: now}
	opts.Now = clk.now
	_, addr := serve(t, opts)
	notify := &fakeNotify{}
	h := sdktest.New(t, energyapi.DefineOn(clk.now), sdktest.WithClient[notificationpb.NotificationServiceClient](notify))
	h.MustStart(&pb.EnergyApiConfig{Url: addr, Token: "fake-token"})
	return clk, h, notify
}

// poll moves the clock on by d a minute at a time, asking for health each
// minute as the host does (more often), and answers the last health.
func poll(h *sdktest.Harness, clk *clock, d time.Duration) sdk.Health {
	for range d/time.Minute - 1 {
		clk.advance(time.Minute)
		h.Health()
	}
	clk.advance(time.Minute)
	return h.Health()
}

func TestAVictronThatStopsReportingWhileTheInverterDoesIsDegradedAndNotifiedOnce(t *testing.T) {
	// The Victron's last reading is at now+5m.
	clk, h, notify := watched(t, energyfake.Options{VictronGap: [2]time.Time{now.Add(10 * time.Minute)}})

	if got := poll(h, clk, 19*time.Minute); got.State != sdk.OK {
		t.Fatalf("health 14 min after the Victron's last reading = %v, want ok", got)
	}
	if got := poll(h, clk, time.Minute); got.State != sdk.Degraded || got.Message != "The Victron hasn't reported for 15 min" {
		t.Errorf("health 15 min after = %v, want degraded: The Victron hasn't reported for 15 min", got)
	}
	if got := poll(h, clk, 3*time.Hour); got.Message != "The Victron hasn't reported for 3 h 15 min" {
		t.Errorf("health 3 h later = %v, want The Victron hasn't reported for 3 h 15 min", got)
	}

	want := []string{"Victron not reporting: The Victron hasn't reported for 15 min, while the inverter still does."}
	if diff := cmp.Diff(want, notify.said()); diff != "" {
		t.Fatalf("Notifications (-want +got):\n%s", diff)
	}
	if got := notify.first(); got.GetCategory() != notificationpb.Category_CATEGORY_CHECKS || got.GetNotification().GetClick() != sdktest.PublicURL+"/energy" {
		t.Errorf("Notification = %v, want category Checks, opening the Energy Page", got)
	}
}

func TestAVictronReportingAgainIsOkAndNotifiedOnce(t *testing.T) {
	clk, h, notify := watched(t, energyfake.Options{VictronGap: [2]time.Time{now.Add(10 * time.Minute), now.Add(3 * time.Hour)}})

	poll(h, clk, time.Hour)
	if got := poll(h, clk, 2*time.Hour+time.Minute); got.State != sdk.OK {
		t.Errorf("health once it reports again = %v, want ok", got)
	}
	poll(h, clk, time.Hour)

	want := []string{
		"Victron not reporting: The Victron hasn't reported for 15 min, while the inverter still does.",
		"Victron reporting again: The Victron reports again after 2 h 55 min.",
	}
	if diff := cmp.Diff(want, notify.said()); diff != "" {
		t.Errorf("Notifications (-want +got):\n%s", diff)
	}
}

func TestAGapThatBeganBeforeStartIsNotifiedOnceSayingSinceWhen(t *testing.T) {
	clk, h, notify := watched(t, energyfake.Options{VictronGap: [2]time.Time{now.Add(-2 * time.Hour)}})

	if got := h.Health(); got.State != sdk.Degraded || got.Message != "The Victron hasn't reported for 2 h 5 min" {
		t.Errorf("health at Start = %v, want degraded: The Victron hasn't reported for 2 h 5 min", got)
	}
	poll(h, clk, time.Hour)

	want := []string{"Victron not reporting: The Victron hasn't reported for 2 h 5 min, while the inverter still does."}
	if diff := cmp.Diff(want, notify.said()); diff != "" {
		t.Errorf("Notifications (-want +got):\n%s", diff)
	}
}

func TestWhenEverythingIsQuietNoVictronNotificationIsSent(t *testing.T) {
	gap := [2]time.Time{now.Add(10 * time.Minute)}
	clk, h, notify := watched(t, energyfake.Options{VictronGap: gap, InverterGap: gap})

	// The flow is cached for seconds: its first read is already quiet.
	clk.advance(time.Hour)
	if got := poll(h, clk, time.Hour); got.State != sdk.Degraded || got.Message != "no reading in the last 5 minutes" {
		t.Errorf("health = %v, want degraded: no reading in the last 5 minutes", got)
	}
	if got := notify.said(); len(got) != 0 {
		t.Errorf("Notifications = %q, want none", got)
	}
}

func TestAnInverterThatStopsReportingWhileTheVictronDoesIsDegradedAndNotified(t *testing.T) {
	clk, h, notify := watched(t, energyfake.Options{InverterGap: [2]time.Time{now.Add(10 * time.Minute)}})

	if got := poll(h, clk, time.Hour); got.State != sdk.Degraded || got.Message != "The inverter hasn't reported for 55 min" {
		t.Errorf("health = %v, want degraded: The inverter hasn't reported for 55 min", got)
	}
	want := []string{"Inverter not reporting: The inverter hasn't reported for 15 min, while the Victron still does."}
	if diff := cmp.Diff(want, notify.said()); diff != "" {
		t.Errorf("Notifications (-want +got):\n%s", diff)
	}
}
