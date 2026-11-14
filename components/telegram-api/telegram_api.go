// Package telegramapi is the telegram-api Component: it sends
// Notifications to one Telegram chat through a bot, as the Bot API's
// sendMessage with HTML text. Its health is the last delivery's outcome.
// Without a bot token and a chat ID it is not set up: it starts, and Send
// refuses with FailedPrecondition.
package telegramapi

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode/utf16"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/itsKontra/ddash/components/telegram-api/telegramapipb"
	"github.com/itsKontra/ddash/sdk"
)

//go:embed component.yaml
var manifest []byte

// sendTimeout bounds one sendMessage.
const sendTimeout = 10 * time.Second

// maxText is the most characters Telegram shows of one message's text.
const maxText = 4096

// Definition is telegram-api, ready for sdk.Serve.
func Definition() sdk.Definition {
	return sdk.Define(sdk.Spec[*telegramapipb.TelegramApiConfig]{
		Manifest: manifest,
		Service:  &telegramapipb.TelegramApiService_ServiceDesc,
		New: func() sdk.Component[*telegramapipb.TelegramApiConfig] {
			return &component{client: &http.Client{Timeout: sendTimeout}}
		},
	})
}

type component struct {
	telegramapipb.UnimplementedTelegramApiServiceServer
	client *http.Client

	// root is the Bot API server's URL with a trailing slash.
	root, token, chat string

	mu sync.Mutex
	// lastErr is why the last delivery failed; nil once one succeeds.
	lastErr error
}

var (
	tokenPattern = regexp.MustCompile(`^[0-9]+:[-_A-Za-z0-9]{30,}$`)
	chatPattern  = regexp.MustCompile(`^(-?[0-9]+|@[A-Za-z][_A-Za-z0-9]{3,})$`)
)

func (c *component) Start(_ context.Context, _ *sdk.Env, cfg *telegramapipb.TelegramApiConfig) error {
	var errs sdk.FieldErrors
	server := cfg.GetServer()
	if cfg.Server == nil {
		// The host fills in the field's default; a test may not.
		server = "https://api.telegram.org"
	}
	u, err := url.Parse(server)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		errs.Add("server", "must be an http or https URL")
	}
	token, chat := strings.TrimSpace(cfg.GetToken()), strings.TrimSpace(cfg.GetChatId())
	switch {
	case token != "" && !tokenPattern.MatchString(token):
		errs.Add("token", "must look like 123456789:AAH…, as @BotFather gave it")
	case token == "" && chat != "":
		errs.Add("token", "is required with a chat ID")
	}
	switch {
	case chat != "" && !chatPattern.MatchString(chat):
		errs.Add("chat_id", "must be a number (a group's starts with -) or a channel's @username")
	case chat == "" && token != "":
		errs.Add("chat_id", "is required with a bot token")
	}
	if err := errs.Err(); err != nil {
		return err
	}
	c.root = strings.TrimSuffix(u.String(), "/") + "/"
	c.token, c.chat = token, chat
	return nil
}

func (c *component) setUp() bool { return c.token != "" }

// errNotSetUp is what Send answers while Telegram is not set up.
var errNotSetUp = status.Error(codes.FailedPrecondition, "Telegram is not set up: enter a bot token and a chat ID")

// Health is down while the last delivery failed.
func (c *component) Health(context.Context) sdk.Health {
	if !c.setUp() {
		return sdk.Health{State: sdk.OK, Message: "not set up"}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.lastErr != nil {
		return sdk.Health{State: sdk.Down, Message: "last delivery failed: " + c.lastErr.Error()}
	}
	return sdk.Health{State: sdk.OK}
}

func (c *component) GetChannel(context.Context, *telegramapipb.GetChannelRequest) (*telegramapipb.GetChannelResponse, error) {
	if !c.setUp() {
		return &telegramapipb.GetChannelResponse{}, nil
	}
	return &telegramapipb.GetChannelResponse{SetUp: true, Destination: "chat " + c.chat}, nil
}

func (c *component) Send(ctx context.Context, req *telegramapipb.SendRequest) (*telegramapipb.SendResponse, error) {
	id, err := c.deliver(ctx, req.GetNotification())
	if err != nil {
		return nil, err
	}
	return &telegramapipb.SendResponse{MessageId: id}, nil
}

func (c *component) SendTest(ctx context.Context, _ *telegramapipb.SendTestRequest) (*telegramapipb.SendTestResponse, error) {
	id, err := c.deliver(ctx, &telegramapipb.Notification{
		Title:   "ddash test",
		Message: "Notifications from ddash reach this chat.",
		Tags:    []string{"white_check_mark"},
	})
	if err != nil {
		return nil, err
	}
	return &telegramapipb.SendTestResponse{MessageId: id}, nil
}

// deliver sends n to the chat and returns Telegram's message id. Its
// outcome becomes the Component's health.
func (c *component) deliver(ctx context.Context, n *telegramapipb.Notification) (int64, error) {
	if !c.setUp() {
		return 0, errNotSetUp
	}
	m := message{
		ChatID:              c.chat,
		Text:                render(n),
		ParseMode:           "HTML",
		DisableNotification: n.GetPriority() == 1 || n.GetPriority() == 2,
		LinkPreview:         linkPreview{Disabled: true},
	}
	id, err := c.post(ctx, m)
	c.mu.Lock()
	c.lastErr = err
	c.mu.Unlock()
	if err != nil {
		return 0, status.Error(codes.Unavailable, err.Error())
	}
	return id, nil
}

// message is sendMessage's JSON body.
type message struct {
	ChatID              string      `json:"chat_id"`
	Text                string      `json:"text"`
	ParseMode           string      `json:"parse_mode"`
	DisableNotification bool        `json:"disable_notification,omitempty"`
	LinkPreview         linkPreview `json:"link_preview_options"`
}

type linkPreview struct {
	Disabled bool `json:"is_disabled"`
}

// emoji are the ntfy tags shown as emoji ahead of the title, as ntfy does.
var emoji = map[string]string{
	"red_circle":       "\U0001F534",
	"green_circle":     "\U0001F7E2",
	"yellow_circle":    "\U0001F7E1",
	"warning":          "⚠️",
	"white_check_mark": "✅",
	"x":                "❌",
	"rotating_light":   "\U0001F6A8",
	"package":          "\U0001F4E6",
}

// linkText is the click link's text.
const linkText = "Open in ddash"

// render is n as sendMessage's HTML text: the title in bold behind its
// tags' emoji, the message, and a link to n's click. A message too long
// for Telegram is cut, marked with "…", keeping the title and the link.
func render(n *telegramapipb.Notification) string {
	title := n.GetTitle()
	var lead []string
	for _, tag := range n.GetTags() {
		if e, ok := emoji[tag]; ok {
			lead = append(lead, e)
		}
	}
	shownTitle := strings.TrimSpace(strings.Join(lead, "") + " " + title)
	body := n.GetMessage()

	// Telegram counts UTF-16 code units of the text without the markup.
	room := maxText - units(shownTitle) - 1
	if n.GetClick() != "" {
		room -= units("\n\n" + linkText)
	}
	if units(body) > room {
		body = cut(body, room-1) + "…"
	}

	var parts []string
	switch {
	case title != "":
		head := "<b>" + escape(title) + "</b>"
		if len(lead) > 0 {
			head = strings.Join(lead, "") + " " + head
		}
		parts = append(parts, head)
	case len(lead) > 0:
		parts = append(parts, strings.Join(lead, ""))
	}
	if body != "" {
		parts = append(parts, escape(body))
	}
	text := strings.Join(parts, "\n")
	if click := n.GetClick(); click != "" {
		text += "\n\n" + `<a href="` + escape(click) + `">` + linkText + "</a>"
	}
	return text
}

// units is how many UTF-16 code units s is.
func units(s string) int {
	n := 0
	for _, r := range s {
		n += utf16.RuneLen(r)
	}
	return n
}

// cut is the longest start of s of at most room UTF-16 code units.
func cut(s string, room int) string {
	n := 0
	for i, r := range s {
		if n += utf16.RuneLen(r); n > room {
			return s[:i]
		}
	}
	return s
}

// escape makes s literal in Telegram's HTML text and attributes.
var escape = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;").Replace

func (c *component) post(ctx context.Context, m message) (int64, error) {
	body, err := json.Marshal(m)
	if err != nil {
		return 0, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.root+"bot"+c.token+"/sendMessage", bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.client.Do(req)
	if err != nil {
		// url.Error would name the URL, and with it the token.
		var uerr *url.Error
		if errors.As(err, &uerr) {
			err = uerr.Err
		}
		return 0, fmt.Errorf("%s unreachable: %s", req.URL.Host, strings.ReplaceAll(err.Error(), c.token, "<token>"))
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	var answer struct {
		OK          bool   `json:"ok"`
		Description string `json:"description"`
		Result      struct {
			MessageID int64 `json:"message_id"`
		} `json:"result"`
	}
	_ = json.Unmarshal(raw, &answer)
	if resp.StatusCode/100 != 2 || !answer.OK {
		why := answer.Description
		if why == "" {
			why = strings.ToLower(http.StatusText(resp.StatusCode))
		}
		return 0, fmt.Errorf("Telegram answered %d: %s", resp.StatusCode, why) //nolint:staticcheck // a name, shown to the Operator
	}
	return answer.Result.MessageID, nil
}
