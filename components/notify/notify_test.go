package notify_test

import (
	"context"
	"sync"
	"testing"

	notificationpb "github.com/itsKontra/ddash/proto/ddash/notification"

	"github.com/google/go-cmp/cmp"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/testing/protocmp"

	"github.com/itsKontra/ddash/components/notify"
	"github.com/itsKontra/ddash/components/notify/notifypb"
	"github.com/itsKontra/ddash/components/ntfy-api/ntfyapipb"
	"github.com/itsKontra/ddash/components/telegram-api/telegramapipb"
	"github.com/itsKontra/ddash/sdk/sdktest"
)

// A channel's state as notify sees it through the host.
const (
	working = "working"
	// unconfigured: the host answers FailedPrecondition "not configured"
	// (a stored config the Component refuses).
	unconfigured = "unconfigured"
	// notSetUp: ntfy-api started without a topic, telegram-api without a
	// token and chat.
	notSetUp = "not set up"
	// failing: the channel's system refuses, or its Component is down.
	failing = "failing"
)

// channel records what a fake channel Component was asked to send.
type channel struct {
	mu    sync.Mutex
	state string
	sent  []string // titles
	dest  string
}

func (c *channel) answer() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	switch c.state {
	case unconfigured:
		return status.Error(codes.FailedPrecondition, "not configured")
	case notSetUp:
		return status.Error(codes.FailedPrecondition, c.dest+" is not set up")
	case failing:
		return status.Error(codes.Unavailable, c.dest+" answered 502: bad gateway")
	}
	return nil
}

func (c *channel) record(title string) error {
	if err := c.answer(); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sent = append(c.sent, title)
	return nil
}

func (c *channel) titles() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.sent...)
}

func (c *channel) set(state string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.state = state
}

type fakeNtfy struct {
	ntfyapipb.NtfyApiServiceClient
	*channel
	got []*notificationpb.Notification
}

func (f *fakeNtfy) Send(_ context.Context, req *ntfyapipb.SendRequest, _ ...grpc.CallOption) (*ntfyapipb.SendResponse, error) {
	if err := f.record(req.GetNotification().GetTitle()); err != nil {
		return nil, err
	}
	f.mu.Lock()
	f.got = append(f.got, req.GetNotification())
	f.mu.Unlock()
	return &ntfyapipb.SendResponse{Id: "x"}, nil
}

func (f *fakeNtfy) GetChannel(context.Context, *ntfyapipb.GetChannelRequest, ...grpc.CallOption) (*ntfyapipb.GetChannelResponse, error) {
	switch f.state {
	case notSetUp:
		return &ntfyapipb.GetChannelResponse{}, nil
	case working:
		return &ntfyapipb.GetChannelResponse{SetUp: true, Destination: f.dest}, nil
	}
	return nil, f.answer()
}

type fakeTelegram struct {
	telegramapipb.TelegramApiServiceClient
	*channel
	got []*notificationpb.Notification
}

func (f *fakeTelegram) Send(_ context.Context, req *telegramapipb.SendRequest, _ ...grpc.CallOption) (*telegramapipb.SendResponse, error) {
	if err := f.record(req.GetNotification().GetTitle()); err != nil {
		return nil, err
	}
	f.mu.Lock()
	f.got = append(f.got, req.GetNotification())
	f.mu.Unlock()
	return &telegramapipb.SendResponse{MessageId: 1}, nil
}

func (f *fakeTelegram) GetChannel(context.Context, *telegramapipb.GetChannelRequest, ...grpc.CallOption) (*telegramapipb.GetChannelResponse, error) {
	switch f.state {
	case notSetUp:
		return &telegramapipb.GetChannelResponse{}, nil
	case working:
		return &telegramapipb.GetChannelResponse{SetUp: true, Destination: f.dest}, nil
	}
	return nil, f.answer()
}

type lab struct {
	ntfy     *fakeNtfy
	telegram *fakeTelegram
}

func newLab(ntfy, telegram string) *lab {
	return &lab{
		ntfy:     &fakeNtfy{channel: &channel{state: ntfy, dest: "ntfy.sh/ddash-alerts"}},
		telegram: &fakeTelegram{channel: &channel{state: telegram, dest: "chat 123456789"}},
	}
}

func start(t *testing.T, l *lab, cfg *notifypb.NotifyConfig) notifypb.NotifyServiceClient {
	t.Helper()
	h := sdktest.New(t, notify.Definition(),
		sdktest.WithClient[ntfyapipb.NtfyApiServiceClient](l.ntfy),
		sdktest.WithClient[telegramapipb.TelegramApiServiceClient](l.telegram))
	h.MustStart(cfg)
	return notifypb.NewNotifyServiceClient(h.Conn)
}

func ptr[T any](v T) *T { return &v }

var nasDown = &notificationpb.Notification{
	Title: "NAS down", Message: "connection refused", Priority: notificationpb.Priority_PRIORITY_HIGH, Tags: []string{"red_circle"},
	Click: "https://ddash.home.example/checks/7",
}

// send sends n as a Notification about Checks.
func send(t *testing.T, c notifypb.NotifyServiceClient, n *notificationpb.Notification) (*notifypb.SendResponse, error) {
	t.Helper()
	return sendAs(t, c, n, notifypb.Category_CATEGORY_CHECKS)
}

func sendAs(t *testing.T, c notifypb.NotifyServiceClient, n *notificationpb.Notification, category notifypb.Category) (*notifypb.SendResponse, error) {
	t.Helper()
	return c.Send(t.Context(), &notifypb.SendRequest{Notification: n, Category: category})
}

func TestANotificationGoesToEveryChannelByDefault(t *testing.T) {
	l := newLab(working, working)
	c := start(t, l, &notifypb.NotifyConfig{})

	resp, err := send(t, c, nasDown)
	if err != nil {
		t.Fatalf("Send: %v", err)
	}

	want := []*notifypb.Delivery{
		{Channel: "ntfy", State: notifypb.DeliveryState_DELIVERY_STATE_SENT},
		{Channel: "telegram", State: notifypb.DeliveryState_DELIVERY_STATE_SENT},
	}
	if diff := cmp.Diff(want, resp.GetDeliveries(), protocmp.Transform()); diff != "" {
		t.Errorf("deliveries (-want +got):\n%s", diff)
	}
	wantNtfy := &notificationpb.Notification{Title: "NAS down", Message: "connection refused", Priority: notificationpb.Priority_PRIORITY_HIGH, Tags: []string{"red_circle"}, Click: "https://ddash.home.example/checks/7"}
	if diff := cmp.Diff([]*notificationpb.Notification{wantNtfy}, l.ntfy.got, protocmp.Transform()); diff != "" {
		t.Errorf("to ntfy (-want +got):\n%s", diff)
	}
	wantTelegram := &notificationpb.Notification{Title: "NAS down", Message: "connection refused", Priority: notificationpb.Priority_PRIORITY_HIGH, Tags: []string{"red_circle"}, Click: "https://ddash.home.example/checks/7"}
	if diff := cmp.Diff([]*notificationpb.Notification{wantTelegram}, l.telegram.got, protocmp.Transform()); diff != "" {
		t.Errorf("to Telegram (-want +got):\n%s", diff)
	}
}

func TestEachChannelGetsOnlyTheCategoriesItsFilterTakes(t *testing.T) {
	l := newLab(working, working)
	c := start(t, l, &notifypb.NotifyConfig{NtfyImageUpdates: ptr(false), TelegramChecks: ptr(false), TelegramActionOutcomes: ptr(true)})

	for _, n := range []struct {
		title    string
		category notifypb.Category
	}{
		{"NAS down", notifypb.Category_CATEGORY_CHECKS},
		{"2 images stale", notifypb.Category_CATEGORY_IMAGE_UPDATES},
		{"Backup of pve done", notifypb.Category_CATEGORY_ACTION_OUTCOMES},
	} {
		if _, err := sendAs(t, c, &notificationpb.Notification{Title: n.title}, n.category); err != nil {
			t.Fatalf("Send %q: %v", n.title, err)
		}
	}

	if diff := cmp.Diff([]string{"NAS down", "Backup of pve done"}, l.ntfy.titles()); diff != "" {
		t.Errorf("ntfy got (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]string{"2 images stale", "Backup of pve done"}, l.telegram.titles()); diff != "" {
		t.Errorf("Telegram got (-want +got):\n%s", diff)
	}
}

func TestAFilteredOutChannelIsReportedSkipped(t *testing.T) {
	l := newLab(working, working)
	c := start(t, l, &notifypb.NotifyConfig{TelegramChecks: ptr(false)})

	resp, err := send(t, c, nasDown)

	if err != nil || resp.GetDeliveries()[1].GetState() != notifypb.DeliveryState_DELIVERY_STATE_FILTERED {
		t.Errorf("Send = %v, %v, want Telegram filtered out", resp, err)
	}
}

func TestAChannelNotSetUpIsSkippedNotAnError(t *testing.T) {
	for _, tc := range []struct{ ntfy, telegram string }{
		{unconfigured, working},
		{notSetUp, working},
		{working, notSetUp},
		{unconfigured, notSetUp},
		{notSetUp, notSetUp},
	} {
		l := newLab(tc.ntfy, tc.telegram)
		c := start(t, l, &notifypb.NotifyConfig{})

		resp, err := send(t, c, nasDown)

		if err != nil {
			t.Errorf("ntfy %s, Telegram %s: Send = %v, want ok", tc.ntfy, tc.telegram, err)
		}
		for _, d := range resp.GetDeliveries() {
			ch := map[string]string{"ntfy": tc.ntfy, "telegram": tc.telegram}[d.GetChannel()]
			want := notifypb.DeliveryState_DELIVERY_STATE_SENT
			if ch != working {
				want = notifypb.DeliveryState_DELIVERY_STATE_NOT_SET_UP
			}
			if d.GetState() != want || d.GetError() != "" {
				t.Errorf("ntfy %s, Telegram %s: %v, want %v", tc.ntfy, tc.telegram, d, want)
			}
		}
	}
}

func TestSendFailsOnlyWhenEveryChannelThatShouldHaveGotItFailed(t *testing.T) {
	l := newLab(failing, working)
	c := start(t, l, &notifypb.NotifyConfig{})

	resp, err := send(t, c, nasDown)
	if err != nil {
		t.Fatalf("one of two failed: Send = %v, want ok", err)
	}
	if d := resp.GetDeliveries()[0]; d.GetState() != notifypb.DeliveryState_DELIVERY_STATE_FAILED || d.GetError() != "ntfy.sh/ddash-alerts answered 502: bad gateway" {
		t.Errorf("ntfy's delivery = %v, want failed with its error", d)
	}

	l.telegram.set(failing)
	_, err = send(t, c, nasDown)
	want := "ntfy: ntfy.sh/ddash-alerts answered 502: bad gateway; Telegram: chat 123456789 answered 502: bad gateway"
	if status.Code(err) != codes.Unavailable || status.Convert(err).Message() != want {
		t.Errorf("both failed: Send = %v, want Unavailable: %s", err, want)
	}

	l.telegram.set(notSetUp)
	if _, err = send(t, c, nasDown); status.Code(err) != codes.Unavailable {
		t.Errorf("the only set-up channel failed: Send = %v, want Unavailable", err)
	}
	c = start(t, l, &notifypb.NotifyConfig{NtfyChecks: ptr(false)})
	if _, err = send(t, c, nasDown); err != nil {
		t.Errorf("the failing channel filtered out: Send = %v, want ok", err)
	}
}

func TestANotificationWithoutACategoryIsRefused(t *testing.T) {
	l := newLab(working, working)
	c := start(t, l, &notifypb.NotifyConfig{})

	_, err := sendAs(t, c, &notificationpb.Notification{Title: "NAS down"}, notifypb.Category_CATEGORY_UNSPECIFIED)

	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("Send = %v, want InvalidArgument", err)
	}
	if len(l.ntfy.titles())+len(l.telegram.titles()) != 0 {
		t.Error("sent it anyway")
	}
}

func TestListChannelsReportsEachChannelsSetupAndCategories(t *testing.T) {
	l := newLab(working, notSetUp)
	c := start(t, l, &notifypb.NotifyConfig{NtfyImageUpdates: ptr(false)})

	resp, err := c.ListChannels(t.Context(), &notifypb.ListChannelsRequest{})
	if err != nil {
		t.Fatal(err)
	}

	all := []notifypb.Category{notifypb.Category_CATEGORY_CHECKS, notifypb.Category_CATEGORY_IMAGE_UPDATES, notifypb.Category_CATEGORY_ACTION_OUTCOMES}
	want := []*notifypb.Channel{
		{Name: "ntfy", Title: "ntfy", Component: "ntfy-api", SetUp: true, Destination: "ntfy.sh/ddash-alerts", Categories: []notifypb.Category{all[0], all[2]}},
		{Name: "telegram", Title: "Telegram", Component: "telegram-api", Categories: all},
	}
	if diff := cmp.Diff(want, resp.GetChannels(), protocmp.Transform()); diff != "" {
		t.Errorf("channels (-want +got):\n%s", diff)
	}

	l.telegram.set(failing)
	for _, state := range []string{notSetUp, unconfigured} {
		l.ntfy.set(state)
		resp, err = c.ListChannels(t.Context(), &notifypb.ListChannelsRequest{})
		if err != nil {
			t.Fatal(err)
		}
		if ch := resp.GetChannels()[0]; ch.GetSetUp() || ch.GetDestination() != "" || ch.GetProblem() != "" {
			t.Errorf("%s ntfy = %v, want not set up, no problem", state, ch)
		}
	}
	if ch := resp.GetChannels()[1]; ch.GetSetUp() || ch.GetProblem() != "telegram-api: chat 123456789 answered 502: bad gateway" {
		t.Errorf("failing Telegram = %v, want its problem", ch)
	}
}
