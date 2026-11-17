package core_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	"github.com/itsKontra/ddash/components/checks"
	ntfyapi "github.com/itsKontra/ddash/components/ntfy-api"
	telegramapi "github.com/itsKontra/ddash/components/telegram-api"
	"github.com/itsKontra/ddash/components/telegram-api/telegramapipb"
	"github.com/itsKontra/ddash/components/telegram-api/telegramfake"
	"github.com/itsKontra/ddash/core/appstate"
	"github.com/itsKontra/ddash/core/coretest"
	"github.com/itsKontra/ddash/core/internal/sample"
	"github.com/itsKontra/ddash/core/internal/sample/samplepb"
	"github.com/itsKontra/ddash/core/notifications"
	"github.com/itsKontra/ddash/core/runtime"
	notificationpb "github.com/itsKontra/ddash/proto/ddash/notification"
	"github.com/itsKontra/ddash/sdk"
)

// sender is a Component that requires nothing and, from its Act, sends a
// Notification through the core: Act's note is "<CATEGORY>:<title>". It
// answers with the core's SendResponse as JSON.
func sender() sdk.Definition {
	return sample.Define("sender", nil, sample.Hooks{
		Act: func(ctx context.Context, env *sdk.Env, req *samplepb.ActRequest) (*samplepb.ActResponse, error) {
			category, title, _ := strings.Cut(req.GetNote(), ":")
			resp, err := sdk.Notifications(env).Send(ctx, &notificationpb.SendRequest{
				Notification: &notificationpb.Notification{Title: title},
				Category:     notificationpb.Category(notificationpb.Category_value[category]),
			})
			if err != nil {
				return nil, err
			}
			b, err := protojson.Marshal(resp)
			return &samplepb.ActResponse{Actor: string(b)}, err
		},
	})
}

var (
	sending        = coretest.Plugin{Name: "sending", Requires: []string{"sender"}}
	telegramPlugin = coretest.Plugin{Name: "telegram", Requires: []string{"telegram-api"}, Headless: true}
)

// notifyLab is a core running sender with both channel Plugins in its
// Release, against a fake ntfy server and a fake Telegram bot.
type notifyLab struct {
	*coretest.Harness
	ntfy *ntfyServer
	bot  *telegramfake.Bot
	// botURL is the fake bot's Bot API server.
	botURL string
}

func newNotifyLab(t *testing.T, opts coretest.Options) *notifyLab {
	t.Helper()
	l := &notifyLab{ntfy: newNtfy(t)}
	l.bot = telegramfake.New("../components/telegram-api/testdata/baseline", telegramfake.Options{Miss: func(request string, err error) { t.Errorf("telegram fixture: %v", err) }})
	srv := httptest.NewServer(l.bot)
	t.Cleanup(srv.Close)
	l.botURL = srv.URL
	opts.Components = append(opts.Components, sender(), ntfyapi.Definition(), telegramapi.Definition())
	opts.Plugins = append(opts.Plugins, sending, ntfyPlugin, telegramPlugin)
	l.Harness = coretest.New(t, opts)
	l.SeedConfig("sender", configured)
	l.Install("sending")
	l.WaitState("sender", runtime.OK)
	return l
}

// installNtfy installs the ntfy Plugin with its config form saved, as the
// Operator does from the Catalogue.
func (l *notifyLab) installNtfy(t *testing.T, extra map[string]any) {
	t.Helper()
	l.Install("ntfy")
	l.WaitState("ntfy-api", runtime.Unconfigured)
	values := map[string]any{"server": l.ntfy.URL, "topic": "ddash-alerts"}
	for k, v := range extra {
		values[k] = v
	}
	b, _ := json.Marshal(values)
	coreJSON(t, l.Harness, "SaveConfig", `{"owner":"ntfy-api","values":`+string(b)+`}`)
	l.WaitState("ntfy-api", runtime.OK)
}

// installTelegram installs the telegram Plugin with its config form saved.
func (l *notifyLab) installTelegram(t *testing.T, extra map[string]any) {
	t.Helper()
	l.Install("telegram")
	l.WaitState("telegram-api", runtime.Unconfigured)
	values := map[string]any{"server": l.botURL, "token": telegramfake.DefaultToken, "chat_id": telegramfake.DefaultChat}
	for k, v := range extra {
		values[k] = v
	}
	b, _ := json.Marshal(values)
	coreJSON(t, l.Harness, "SaveConfig", `{"owner":"telegram-api","values":`+string(b)+`}`)
	l.WaitState("telegram-api", runtime.OK)
}

// send has sender send a Notification of category titled title, and
// returns each channel's delivery as "<state>" or "<state>: <error>".
func (l *notifyLab) send(t *testing.T, category, title string) map[string]string {
	t.Helper()
	res := l.Call("sending", "sender", "Act", `{"note":"`+category+`:`+title+`"}`)
	if res.Code != http.StatusOK {
		t.Fatalf("send %s: status %d: %s", title, res.Code, res.Body)
	}
	var act struct{ Actor string }
	if err := json.Unmarshal(res.Body.Bytes(), &act); err != nil {
		t.Fatal(err)
	}
	var sent notificationpb.SendResponse
	if err := protojson.Unmarshal([]byte(act.Actor), &sent); err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, d := range sent.GetDeliveries() {
		state := strings.ToLower(strings.TrimPrefix(d.GetState().String(), "DELIVERY_STATE_"))
		if d.GetError() != "" {
			state += ": " + d.GetError()
		}
		out[d.GetChannel()] = state
	}
	return out
}

// chainOf returns the hops of the newest chain whose root is target's
// method, oldest first, as "<target> <method> <outcome> <actor>", the hops
// to channels sorted since they run at once.
func chainOf(h *coretest.Harness, target, method string) []string {
	entries := h.AuditLog()
	i := slices.IndexFunc(entries, func(e appstate.Entry) bool { return e.Target == target && e.Method == method })
	if i < 0 {
		return nil
	}
	var hops []string
	for _, e := range slices.Backward(entries) {
		if e.Chain == entries[i].Chain {
			hops = append(hops, e.Target+" "+e.Method+" "+e.Outcome+" "+e.ActorKind+":"+e.Username)
		}
	}
	if len(hops) > 2 {
		slices.Sort(hops[2:])
	}
	return hops
}

func TestAComponentSendsANotificationWithoutRequiringAnyAndNoChannelIsNoError(t *testing.T) {
	l := newNotifyLab(t, coretest.Options{})

	got := l.send(t, "CATEGORY_CHECKS", "NAS down")

	if len(got) != 0 {
		t.Errorf("deliveries %v, want none: no channel is installed", got)
	}
	want := []string{"sender Act ok operator:test", "core Send ok operator:test"}
	if diff := cmp.Diff(want, chainOf(l.Harness, "sender", "Act")); diff != "" {
		t.Errorf("the Act's chain (-want +got):\n%s", diff)
	}
	if e := l.AuditLog()[0]; e.Target != "core" || !strings.Contains(e.Request, `"title":"NAS down"`) || !strings.Contains(e.Request, "CATEGORY_CHECKS") {
		t.Errorf("the Send's entry %+v, want the Notification and its category recorded", e)
	}
}

func TestANotificationWithoutACategoryIsRefusedAndAudited(t *testing.T) {
	l := newNotifyLab(t, coretest.Options{})
	l.installNtfy(t, nil)

	res := l.Call("sending", "sender", "Act", `{"note":":NAS down"}`)

	if res.Code != http.StatusBadRequest || !strings.Contains(res.Body.String(), "a Notification needs a category") {
		t.Errorf("Send without a category = %d %s, want invalid_argument", res.Code, res.Body)
	}
	want := []string{"sender Act InvalidArgument operator:test", "core Send InvalidArgument operator:test"}
	if diff := cmp.Diff(want, chainOf(l.Harness, "sender", "Act")); diff != "" {
		t.Errorf("the Act's chain (-want +got):\n%s", diff)
	}
	if got := l.ntfy.published(); len(got) != 0 {
		t.Errorf("ntfy got %v, want nothing", got)
	}
}

func TestEveryInstalledChannelGetsTheNotificationInTheSendersChain(t *testing.T) {
	l := newNotifyLab(t, coretest.Options{})
	l.installNtfy(t, nil)
	l.installTelegram(t, nil)

	got := l.send(t, "CATEGORY_CHECKS", "NAS down")

	if diff := cmp.Diff(map[string]string{"ntfy-api": "sent", "telegram-api": "sent"}, got); diff != "" {
		t.Errorf("deliveries (-want +got):\n%s", diff)
	}
	if got := l.ntfy.published(); !slices.Equal(got, []string{"NAS down"}) {
		t.Errorf("ntfy got %v, want the Notification", got)
	}
	if msgs := l.bot.Messages(); len(msgs) != 1 || msgs[0].Plain != "NAS down" {
		t.Errorf("Telegram got %+v, want the Notification", msgs)
	}
	want := []string{
		"sender Act ok operator:test",
		"core Send ok operator:test",
		"ntfy-api Deliver ok operator:test",
		"telegram-api Deliver ok operator:test",
	}
	if diff := cmp.Diff(want, chainOf(l.Harness, "sender", "Act")); diff != "" {
		t.Errorf("the Act's chain (-want +got):\n%s", diff)
	}
}

func TestAChannelWhoseFilterDoesntTakeTheCategoryIsSkipped(t *testing.T) {
	l := newNotifyLab(t, coretest.Options{})
	l.installNtfy(t, nil)
	l.installTelegram(t, map[string]any{"image_updates": false})

	got := l.send(t, "CATEGORY_IMAGE_UPDATES", "1 image update")

	if diff := cmp.Diff(map[string]string{"ntfy-api": "sent", "telegram-api": "filtered"}, got); diff != "" {
		t.Errorf("deliveries (-want +got):\n%s", diff)
	}
	if msgs := l.bot.Messages(); len(msgs) != 0 {
		t.Errorf("Telegram got %+v, want nothing", msgs)
	}
	want := []string{"sender Act ok operator:test", "core Send ok operator:test", "ntfy-api Deliver ok operator:test"}
	if diff := cmp.Diff(want, chainOf(l.Harness, "sender", "Act")); diff != "" {
		t.Errorf("the Act's chain (-want +got):\n%s", diff)
	}
}

// A channel whose delivery fails fails neither the send nor the other
// channel, and shows down on its own Plugin only.
func TestAFailingChannelShowsDownOnItsOwnPluginOnly(t *testing.T) {
	l := newNotifyLab(t, coretest.Options{})
	l.installNtfy(t, nil)
	l.installTelegram(t, nil)
	l.ntfy.answer(http.StatusUnauthorized)

	got := l.send(t, "CATEGORY_CHECKS", "NAS down")

	want := map[string]string{"ntfy-api": "failed: ntfy answered 401: unauthorized", "telegram-api": "sent"}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("deliveries (-want +got):\n%s", diff)
	}
	states := map[string]string{}
	for _, p := range coreJSON(t, l.Harness, "ListCatalogue", `{}`)["plugins"].([]any) {
		p := p.(map[string]any)
		for _, c := range p["components"].([]any) {
			c := c.(map[string]any)
			states[p["name"].(string)+"/"+c["name"].(string)], _ = c["state"].(string)
		}
	}
	wantStates := map[string]string{"sending/sender": "ok", "ntfy/ntfy-api": "down", "telegram/telegram-api": "ok"}
	if diff := cmp.Diff(wantStates, states); diff != "" {
		t.Errorf("the Catalogue's Component states by Plugin (-want +got):\n%s", diff)
	}
}

func TestASendFailsOnlyWhenEveryChannelThatShouldHaveReceivedItFailed(t *testing.T) {
	l := newNotifyLab(t, coretest.Options{})
	l.installNtfy(t, nil)
	l.installTelegram(t, map[string]any{"checks": false})
	l.ntfy.answer(http.StatusUnauthorized)

	res := l.Call("sending", "sender", "Act", `{"note":"CATEGORY_CHECKS:NAS down"}`)

	if res.Code != http.StatusServiceUnavailable || !strings.Contains(res.Body.String(), "ntfy-api: ntfy answered 401: unauthorized") {
		t.Errorf("Send = %d %s, want unavailable naming ntfy's failure", res.Code, res.Body)
	}
	if hops := chainOf(l.Harness, "sender", "Act"); len(hops) < 2 || hops[1] != "core Send Unavailable operator:test" {
		t.Errorf("the Act's chain %v, want core Send Unavailable", hops)
	}
}

// One channel that hangs delays the Send by DeliveryDeadline at most and
// the other channel not at all.
func TestAChannelThatHangsIsFailedAtTheDeliveryDeadline(t *testing.T) {
	defer func(d time.Duration) { notifications.DeliveryDeadline = d }(notifications.DeliveryDeadline)
	notifications.DeliveryDeadline = 300 * time.Millisecond
	l := newNotifyLab(t, coretest.Options{})
	// Released before the core shuts down, so the Deliver still running
	// ends then.
	release := make(chan struct{})
	hanging := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		<-release
		_, _ = w.Write([]byte(`{"id":"late"}`))
	}))
	t.Cleanup(hanging.Close)
	t.Cleanup(func() { close(release) })
	l.installNtfy(t, map[string]any{"server": hanging.URL})
	l.installTelegram(t, nil)

	start := time.Now()
	got := l.send(t, "CATEGORY_CHECKS", "NAS down")

	if took := time.Since(start); took > 5*time.Second {
		t.Errorf("Send took %s, want about the delivery deadline", took)
	}
	want := map[string]string{"ntfy-api": "failed: no answer within 300ms", "telegram-api": "sent"}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("deliveries (-want +got):\n%s", diff)
	}
	if msgs := l.bot.Messages(); len(msgs) != 1 {
		t.Errorf("Telegram got %+v, want the Notification", msgs)
	}
}

// A channel's Deliver is the core's to call: a Component that calls it
// itself is refused, as any call outside its requires.
func TestAComponentCantCallAChannelItself(t *testing.T) {
	l := newNotifyLab(t, coretest.Options{Components: []sdk.Definition{sample.Define("rogue", nil, sample.Hooks{
		Act: func(ctx context.Context, env *sdk.Env, _ *samplepb.ActRequest) (*samplepb.ActResponse, error) {
			_, err := sdk.Require(env, notificationpb.NewChannelServiceClient).Deliver(ctx, &notificationpb.DeliverRequest{
				Notification: &notificationpb.Notification{Title: "spoofed"},
			})
			return &samplepb.ActResponse{}, err
		},
	})}, Plugins: []coretest.Plugin{{Name: "rogues", Requires: []string{"rogue"}}}})
	l.installNtfy(t, nil)
	l.installTelegram(t, nil)
	l.SeedConfig("rogue", configured)
	l.Install("rogues")
	l.WaitState("rogue", runtime.OK)

	res := l.Call("rogues", "rogue", "Act", `{}`)

	if res.Code != http.StatusForbidden || !strings.Contains(res.Body.String(), "doesn't require a Component serving ddash.notification.ChannelService") {
		t.Errorf("rogue's Deliver = %d %s, want permission_denied", res.Code, res.Body)
	}
	if got := l.ntfy.published(); len(got) != 0 {
		t.Errorf("ntfy got %v, want nothing", got)
	}
	if msgs := l.bot.Messages(); len(msgs) != 0 {
		t.Errorf("Telegram got %+v, want nothing", msgs)
	}
}

// The Checks Page's channel list comes from the core, through checks:
// each installed channel with its destination, categories and last
// problem.
func TestChecksListsTheInstalledChannelsFromTheCore(t *testing.T) {
	l := newNotifyLab(t, coretest.Options{
		Components: []sdk.Definition{checks.Definition()},
		Plugins:    []coretest.Plugin{{Name: "checks", Requires: []string{"checks"}}},
	})
	l.Install("checks")
	l.WaitState("checks", runtime.OK)
	list := func() string {
		t.Helper()
		res := l.Call("checks", "checks", "ListChannels", `{}`)
		if res.Code != http.StatusOK {
			t.Fatalf("ListChannels: %d %s", res.Code, res.Body)
		}
		return res.Body.String()
	}
	if got := list(); got != `{}` {
		t.Errorf("ListChannels with no channel installed = %s, want none", got)
	}
	l.installNtfy(t, map[string]any{"action_outcomes": false})
	l.Install("telegram")
	l.WaitState("telegram-api", runtime.Unconfigured)
	l.ntfy.answer(http.StatusUnauthorized)
	// Both fail: ntfy refuses, Telegram isn't configured yet.
	if res := l.Call("sending", "sender", "Act", `{"note":"CATEGORY_CHECKS:NAS down"}`); res.Code != http.StatusServiceUnavailable {
		t.Errorf("Send = %d %s, want unavailable", res.Code, res.Body)
	}

	var got struct{ Channels []map[string]any }
	if err := json.Unmarshal([]byte(list()), &got); err != nil {
		t.Fatal(err)
	}
	want := []map[string]any{
		{
			"component": "ntfy-api", "title": "ntfy", "destination": strings.TrimPrefix(l.ntfy.URL, "http://") + "/ddash-alerts",
			"categories": []any{"CATEGORY_CHECKS", "CATEGORY_IMAGE_UPDATES"},
			"problem":    "last delivery failed: ntfy answered 401: unauthorized",
		},
		{"component": "telegram-api", "title": "telegram-api", "problem": "not configured"},
	}
	if diff := cmp.Diff(want, got.Channels); diff != "" {
		t.Errorf("ListChannels (-want +got):\n%s", diff)
	}

	if _, err := l.Uninstall("telegram", false); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(list()), &got); err != nil || len(got.Channels) != 1 {
		t.Errorf("ListChannels after uninstalling telegram = %v, %v, want ntfy only", got.Channels, err)
	}
}

func TestUninstallingAChannelStopsItsNotificationsAndChangesNoOtherPlugin(t *testing.T) {
	l := newNotifyLab(t, coretest.Options{})
	l.installNtfy(t, nil)
	l.installTelegram(t, nil)

	if _, err := l.Uninstall("telegram", false); err != nil {
		t.Fatal(err)
	}
	got := l.send(t, "CATEGORY_CHECKS", "NAS down")

	if diff := cmp.Diff(map[string]string{"ntfy-api": "sent"}, got); diff != "" {
		t.Errorf("deliveries (-want +got):\n%s", diff)
	}
	if msgs := l.bot.Messages(); len(msgs) != 0 {
		t.Errorf("Telegram got %+v after its Plugin was uninstalled, want nothing", msgs)
	}
	if st := l.Status("sender"); st.State != runtime.OK {
		t.Errorf("sender = %v, want ok", st)
	}
	// Its config is kept, as any uninstalled Component's, for a reinstall.
	var cfg telegramapipb.TelegramApiConfig
	if raw, ok, err := l.Host.Config().Raw(t.Context(), "telegram-api"); err != nil || !ok {
		t.Fatalf("telegram-api's config: %v, %v", ok, err)
	} else if err := proto.Unmarshal(raw, &cfg); err != nil || cfg.GetChatId() != telegramfake.DefaultChat {
		t.Errorf("telegram-api's kept config = %v, %v", &cfg, err)
	}
}
