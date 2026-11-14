package telegramapi_test

import (
	"net"
	"net/http/httptest"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/google/go-cmp/cmp"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	telegramapi "github.com/itsKontra/ddash/components/telegram-api"
	"github.com/itsKontra/ddash/components/telegram-api/telegramapipb"
	"github.com/itsKontra/ddash/components/telegram-api/telegramfake"
	"github.com/itsKontra/ddash/sdk"
	"github.com/itsKontra/ddash/sdk/sdktest"
)

func ptr[T any](v T) *T { return &v }

// bot serves a telegramfake.Bot and returns it with its URL.
func bot(t *testing.T) (*telegramfake.Bot, string) {
	t.Helper()
	b := telegramfake.New("testdata/baseline", telegramfake.Options{Miss: func(request string, err error) { t.Errorf("fixture: %v", err) }})
	srv := httptest.NewServer(b)
	t.Cleanup(srv.Close)
	return b, srv.URL
}

func setUp(url string) *telegramapipb.TelegramApiConfig {
	return &telegramapipb.TelegramApiConfig{Server: ptr(url), Token: telegramfake.DefaultToken, ChatId: telegramfake.DefaultChat}
}

func start(t *testing.T, cfg *telegramapipb.TelegramApiConfig) (*sdktest.Harness, telegramapipb.TelegramApiServiceClient) {
	t.Helper()
	h := sdktest.New(t, telegramapi.Definition())
	h.MustStart(cfg)
	return h, telegramapipb.NewTelegramApiServiceClient(h.Conn)
}

func send(t *testing.T, c telegramapipb.TelegramApiServiceClient, n *telegramapipb.Notification) (*telegramapipb.SendResponse, error) {
	t.Helper()
	return c.Send(t.Context(), &telegramapipb.SendRequest{Notification: n})
}

var down = &telegramapipb.Notification{
	Title:    "Jellyfin down",
	Message:  "timeout",
	Priority: 4,
	Tags:     []string{"red_circle"},
	Click:    "https://ddash.home.example/checks/7",
}

func TestSendWritesTheNotificationToTheChatWithALinkToDdash(t *testing.T) {
	b, url := bot(t)
	h, c := start(t, setUp(url))

	resp, err := send(t, c, down)
	if err != nil {
		t.Fatalf("Send: %v", err)
	}

	if resp.GetMessageId() != 1 {
		t.Errorf("message id = %d, want Telegram's", resp.GetMessageId())
	}
	got := b.Messages()
	if len(got) != 1 {
		t.Fatalf("sent %v, want one message", got)
	}
	want := telegramfake.Message{
		ID: 1, ChatID: telegramfake.DefaultChat, ParseMode: "HTML",
		Plain:     "\U0001F534 Jellyfin down\ntimeout\n\nOpen in ddash",
		Links:     []string{"https://ddash.home.example/checks/7"},
		NoPreview: true,
	}
	if diff := cmp.Diff(want, got[0], cmpIgnoreText); diff != "" {
		t.Errorf("sent (-want +got):\n%s", diff)
	}
	if !strings.Contains(got[0].Text, "<b>Jellyfin down</b>") {
		t.Errorf("text %q, want the title in bold", got[0].Text)
	}
	if got := h.Health(); got.State != sdk.OK {
		t.Errorf("health after a delivery = %v, want ok", got)
	}
}

var cmpIgnoreText = cmp.FilterPath(func(p cmp.Path) bool { return p.Last().String() == ".Text" }, cmp.Ignore())

func TestMarkupInTheNotificationIsEscaped(t *testing.T) {
	b, url := bot(t)
	_, c := start(t, setUp(url))

	n := &telegramapipb.Notification{
		Title:   "<Grafana> & co",
		Message: `nginx: [emerg] "upstream" <none> & 1 > 0`,
		Click:   `https://ddash.home.example/checks?a=1&b="2"`,
	}
	if _, err := send(t, c, n); err != nil {
		t.Fatalf("Send: %v (Telegram couldn't parse it)", err)
	}

	got := b.Messages()[0]
	if want := "<Grafana> & co\nnginx: [emerg] \"upstream\" <none> & 1 > 0\n\nOpen in ddash"; got.Plain != want {
		t.Errorf("shown %q, want %q", got.Plain, want)
	}
	if diff := cmp.Diff([]string{n.Click}, got.Links); diff != "" {
		t.Errorf("links (-want +got):\n%s", diff)
	}
}

func TestALowPriorityArrivesSilentlyAndNoClickMeansNoLink(t *testing.T) {
	b, url := bot(t)
	_, c := start(t, setUp(url))

	if _, err := send(t, c, &telegramapipb.Notification{Title: "Backup done", Priority: 2, Tags: []string{"unknown_tag"}}); err != nil {
		t.Fatal(err)
	}

	got := b.Messages()[0]
	if !got.Silent || got.Plain != "Backup done" || len(got.Links) != 0 {
		t.Errorf("sent %+v, want a silent message with only the title", got)
	}
}

func TestALongMessageIsCutToTelegramsLimit(t *testing.T) {
	b, url := bot(t)
	_, c := start(t, setUp(url))

	n := &telegramapipb.Notification{Title: "12 down", Message: strings.Repeat("NAS down: connection <refused>\n", 400), Click: "https://ddash.home.example/checks"}
	if _, err := send(t, c, n); err != nil {
		t.Fatalf("Send: %v", err)
	}

	got := b.Messages()[0]
	if n := utf8.RuneCountInString(got.Plain); n > telegramfake.MaxText {
		t.Errorf("%d characters sent, want at most %d", n, telegramfake.MaxText)
	}
	if !strings.HasSuffix(got.Plain, "…\n\nOpen in ddash") {
		t.Errorf("shown ends %q, want the cut marked and the link kept", got.Plain[len(got.Plain)-40:])
	}
}

func TestAFailedDeliveryMakesItDownWithTelegramsErrorUntilOneSucceeds(t *testing.T) {
	b, url := bot(t)
	cfg := setUp(url)
	cfg.Token = "7000000001:AARevokedTokenRevokedTokenRevoked01"
	h, c := start(t, cfg)

	_, err := send(t, c, down)

	if status.Code(err) != codes.Unavailable || status.Convert(err).Message() != "Telegram answered 401: Unauthorized" {
		t.Errorf("Send = %v, want Unavailable: Telegram answered 401: Unauthorized", err)
	}
	if got := h.Health(); got.State != sdk.Down || got.Message != "last delivery failed: Telegram answered 401: Unauthorized" {
		t.Errorf("health = %v, want down with the last error", got)
	}

	_, c = start(t, setUp(url))
	b.FailNext(429, "Too Many Requests: retry after 7", 7)
	_, err = send(t, c, down)
	if msg := status.Convert(err).Message(); msg != "Telegram answered 429: Too Many Requests: retry after 7" {
		t.Errorf("Send = %v, want Telegram's 429", err)
	}
	if _, err := send(t, c, down); err != nil {
		t.Fatalf("Send after the limit passed: %v", err)
	}
}

func TestAnUnknownChatIsTelegramsError(t *testing.T) {
	_, url := bot(t)
	cfg := setUp(url)
	cfg.ChatId = "-1001234567890"
	_, c := start(t, cfg)

	_, err := send(t, c, down)

	if status.Code(err) != codes.Unavailable || status.Convert(err).Message() != "Telegram answered 400: Bad Request: chat not found" {
		t.Errorf("Send = %v, want Telegram's chat not found", err)
	}
}

func TestAnUnreachableServerIsADeliveryFailureThatNeverShowsTheToken(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	url := "http://" + l.Addr().String()
	l.Close()
	h, c := start(t, setUp(url))

	_, err = send(t, c, down)

	msg := status.Convert(err).Message()
	if status.Code(err) != codes.Unavailable || !strings.Contains(msg, "unreachable") {
		t.Errorf("Send = %v, want Unavailable naming the server unreachable", err)
	}
	if strings.Contains(msg, telegramfake.DefaultToken) || strings.Contains(h.Health().Message, telegramfake.DefaultToken) {
		t.Errorf("error %q shows the bot token", msg)
	}
}

func TestSendTestWritesATestMessage(t *testing.T) {
	b, url := bot(t)
	_, c := start(t, setUp(url))

	resp, err := c.SendTest(t.Context(), &telegramapipb.SendTestRequest{})
	if err != nil || resp.GetMessageId() != 1 {
		t.Fatalf("SendTest = %v, %v", resp, err)
	}

	got := b.Messages()
	if len(got) != 1 || !strings.Contains(got[0].Plain, "ddash test") || got[0].ChatID != telegramfake.DefaultChat {
		t.Errorf("sent %+v, want one test message to the chat", got)
	}
}

func TestWithoutTokenAndChatTelegramIsNotSetUp(t *testing.T) {
	b, _ := bot(t)
	h, c := start(t, &telegramapipb.TelegramApiConfig{})

	if got := h.Health(); got.State != sdk.OK || got.Message != "not set up" {
		t.Errorf("health = %v, want ok: not set up", got)
	}
	ch, err := c.GetChannel(t.Context(), &telegramapipb.GetChannelRequest{})
	if err != nil || ch.GetSetUp() || ch.GetDestination() != "" {
		t.Errorf("GetChannel = %v, %v, want not set up", ch, err)
	}
	for name, call := range map[string]func() error{
		"Send":     func() error { _, err := send(t, c, down); return err },
		"SendTest": func() error { _, err := c.SendTest(t.Context(), &telegramapipb.SendTestRequest{}); return err },
	} {
		if err := call(); status.Code(err) != codes.FailedPrecondition || !strings.Contains(status.Convert(err).Message(), "not set up") {
			t.Errorf("%s = %v, want FailedPrecondition: not set up", name, err)
		}
	}
	if got := b.Log(); len(got) != 0 {
		t.Errorf("requests %v, want none", got)
	}
}

func TestGetChannelNamesTheChat(t *testing.T) {
	_, url := bot(t)
	_, c := start(t, setUp(url))

	ch, err := c.GetChannel(t.Context(), &telegramapipb.GetChannelRequest{})
	if err != nil || !ch.GetSetUp() || ch.GetDestination() != "chat 123456789" {
		t.Errorf("GetChannel = %v, %v, want set up to chat 123456789", ch, err)
	}
}

func TestABadConfigIsRefusedPerField(t *testing.T) {
	const token = "123456789:AAHabcdefghijklmnopqrstuvwxyz012345"
	for _, tc := range []struct {
		cfg        *telegramapipb.TelegramApiConfig
		path, want string
	}{
		{&telegramapipb.TelegramApiConfig{Server: ptr("ftp://api.telegram.org"), Token: token, ChatId: "1"}, "server", "must be an http or https URL"},
		{&telegramapipb.TelegramApiConfig{Token: "not-a-token", ChatId: "1"}, "token", "must look like 123456789:AAH…, as @BotFather gave it"},
		{&telegramapipb.TelegramApiConfig{Token: token, ChatId: "me"}, "chat_id", "must be a number (a group's starts with -) or a channel's @username"},
		{&telegramapipb.TelegramApiConfig{Token: token}, "chat_id", "is required with a bot token"},
		{&telegramapipb.TelegramApiConfig{ChatId: "-1001234567890"}, "token", "is required with a chat ID"},
	} {
		h := sdktest.New(t, telegramapi.Definition())
		errs := sdk.FieldErrorsOf(h.Start(tc.cfg))
		if len(errs) != 1 || errs[0].Path != tc.path || errs[0].Message != tc.want {
			t.Errorf("Start(%v) field errors = %v, want %s: %s", tc.cfg, errs, tc.path, tc.want)
		}
	}
	for _, chat := range []string{"123456789", "-1001234567890", "@ddash_alerts"} {
		h := sdktest.New(t, telegramapi.Definition())
		if err := h.Start(&telegramapipb.TelegramApiConfig{Token: token, ChatId: chat}); err != nil {
			t.Errorf("Start with chat %q: %v", chat, err)
		}
	}
}
