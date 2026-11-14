// Package telegramfake is a stateful fake of the Telegram Bot API for one
// bot, for telegram-api's tests and fakelab. It keeps the bot's token and
// the chats it may write to, and records every message sent, in order;
// anything else (getMe) is served from a scenario's fixtures
// (fixture.Scenario), under the bot's token.
//
// Like Telegram it answers a wrong token with 401, an unknown chat with
// 400 "chat not found", and parses HTML text as Telegram does: only its
// tags, and every <, > and & that isn't part of one escaped, else 400
// "can't parse entities". A message whose text is longer than 4096
// characters once parsed is refused as too long.
package telegramfake

import (
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/itsKontra/ddash/sdk/fixture"
)

// DefaultToken and DefaultChat are the bot's token and the one chat it may
// write to when Options leave them empty.
const (
	DefaultToken = "7000000001:AAFakeLabTokenForTheTelegramFake0001"
	DefaultChat  = "123456789"
)

// MaxText is the most characters a message's text may have once parsed.
const MaxText = 4096

// Options shape a Bot.
type Options struct {
	// Miss is told about a request no fixture answers (fixture.Scenario).
	Miss func(request string, err error)
	// Token is the bot's token; empty for DefaultToken.
	Token string
	// Chats are the chats the bot may write to; none for DefaultChat.
	Chats []string
}

// Message is one message the bot sent.
type Message struct {
	ID     int64
	ChatID string
	// Text is the text as sent, ParseMode its parse_mode ("" for plain).
	Text, ParseMode string
	// Plain is the text as the chat shows it: tags dropped, entities
	// decoded. Links are the href of each <a>, in order.
	Plain string
	Links []string
	// Silent: disable_notification was set.
	Silent bool
	// NoPreview: link_preview_options.is_disabled was set.
	NoPreview bool
}

// Bot is the fake, an http.Handler.
type Bot struct {
	fixtures http.Handler
	token    string
	chats    []string

	mu   sync.Mutex
	sent []Message
	// fail answers the next sendMessages with these errors, in order.
	fail []failure
	log  []string
}

type failure struct {
	code        int
	description string
	retryAfter  int
}

// New serves the scenario in dir for anything but sendMessage.
func New(dir string, opts Options) *Bot {
	if opts.Token == "" {
		opts.Token = DefaultToken
	}
	if len(opts.Chats) == 0 {
		opts.Chats = []string{DefaultChat}
	}
	return &Bot{fixtures: fixture.Scenario(dir, opts.Miss), token: opts.Token, chats: opts.Chats}
}

// Messages returns every message sent so far, in order.
func (b *Bot) Messages() []Message {
	b.mu.Lock()
	defer b.mu.Unlock()
	return slices.Clone(b.sent)
}

// Log returns every request as "METHOD /bot<token>/<method>", in order,
// with the token replaced by "<token>" when it was the bot's.
func (b *Bot) Log() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return slices.Clone(b.log)
}

// FailNext makes the next sendMessage answer with Telegram's error code
// and description; retryAfter > 0 adds parameters.retry_after, as a 429
// does. Calls queue up.
func (b *Bot) FailNext(code int, description string, retryAfter int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.fail = append(b.fail, failure{code, description, retryAfter})
}

func (b *Bot) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rest, ok := strings.CutPrefix(r.URL.Path, "/bot")
	token, method, _ := strings.Cut(rest, "/")
	b.mu.Lock()
	logged := r.URL.Path
	if ok && token == b.token {
		logged = "/bot<token>/" + method
	}
	b.log = append(b.log, r.Method+" "+logged)
	b.mu.Unlock()
	if !ok || method == "" {
		fail(w, http.StatusNotFound, "Not Found", 0)
		return
	}
	if token != b.token {
		fail(w, http.StatusUnauthorized, "Unauthorized", 0)
		return
	}
	if method != "sendMessage" {
		// The Bot API takes GET and POST alike; the fixtures are GETs.
		r2 := r.Clone(r.Context())
		r2.Method, r2.URL.Path = http.MethodGet, "/"+method
		b.fixtures.ServeHTTP(w, r2)
		return
	}
	b.sendMessage(w, r)
}

// params reads a Bot API call's parameters, from a JSON body or a form.
func params(r *http.Request) (map[string]any, error) {
	if strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		var p map[string]any
		if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
			return nil, err
		}
		return p, nil
	}
	if err := r.ParseForm(); err != nil {
		return nil, err
	}
	p := map[string]any{}
	for k, v := range r.Form {
		p[k] = v[0]
	}
	return p, nil
}

func (b *Bot) sendMessage(w http.ResponseWriter, r *http.Request) {
	p, err := params(r)
	if err != nil {
		fail(w, http.StatusBadRequest, "Bad Request: invalid parameters: "+err.Error(), 0)
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.fail) > 0 {
		f := b.fail[0]
		b.fail = b.fail[1:]
		fail(w, f.code, f.description, f.retryAfter)
		return
	}
	m := Message{ChatID: text(p["chat_id"]), Text: text(p["text"]), ParseMode: text(p["parse_mode"])}
	if !slices.Contains(b.chats, m.ChatID) {
		fail(w, http.StatusBadRequest, "Bad Request: chat not found", 0)
		return
	}
	switch m.ParseMode {
	case "":
		m.Plain = m.Text
	case "HTML":
		if m.Plain, m.Links, err = parseHTML(m.Text); err != nil {
			fail(w, http.StatusBadRequest, "Bad Request: can't parse entities: "+err.Error(), 0)
			return
		}
	default:
		fail(w, http.StatusBadRequest, "Bad Request: unsupported parse_mode", 0)
		return
	}
	if strings.TrimSpace(m.Plain) == "" {
		fail(w, http.StatusBadRequest, "Bad Request: message text is empty", 0)
		return
	}
	if utf8.RuneCountInString(m.Plain) > MaxText {
		fail(w, http.StatusBadRequest, "Bad Request: message is too long", 0)
		return
	}
	m.Silent = truthy(p["disable_notification"])
	if lp, ok := p["link_preview_options"].(map[string]any); ok {
		m.NoPreview = truthy(lp["is_disabled"])
	}
	m.ID = int64(len(b.sent) + 1)
	b.sent = append(b.sent, m)
	reply(w, http.StatusOK, map[string]any{"ok": true, "result": map[string]any{
		"message_id": m.ID,
		"chat":       map[string]any{"id": m.ChatID},
		"text":       m.Plain,
	}})
}

// tags are the tags Telegram's HTML parse mode knows.
var tags = []string{"b", "strong", "i", "em", "u", "ins", "s", "strike", "del", "span", "tg-spoiler", "a", "tg-emoji", "code", "pre", "blockquote"}

// entities are the named entities Telegram decodes.
var entities = []string{"lt", "gt", "amp", "quot"}

// parseHTML parses s as Telegram's HTML parse mode: the text it shows and
// each link's href.
func parseHTML(s string) (plain string, links []string, err error) {
	var out strings.Builder
	var open []string
	for i := 0; i < len(s); {
		switch s[i] {
		case '<':
			end := strings.IndexByte(s[i:], '>')
			if end < 0 {
				return "", nil, fmt.Errorf("unclosed start tag at byte offset %d", i)
			}
			tag := s[i+1 : i+end]
			name, attrs, _ := strings.Cut(tag, " ")
			if closing, ok := strings.CutPrefix(name, "/"); ok {
				if len(open) == 0 || open[len(open)-1] != closing {
					return "", nil, fmt.Errorf("unmatched end tag at byte offset %d, expected \"</%s>\", found \"</%s>\"", i, last(open), closing)
				}
				open = open[:len(open)-1]
			} else {
				if !slices.Contains(tags, name) {
					return "", nil, fmt.Errorf("unsupported start tag %q at byte offset %d", name, i)
				}
				if name == "a" {
					href, ok := attr(attrs, "href")
					if !ok {
						return "", nil, fmt.Errorf("tag \"a\" without href at byte offset %d", i)
					}
					links = append(links, href)
				}
				open = append(open, name)
			}
			i += end + 1
		case '>':
			return "", nil, fmt.Errorf("unexpected \">\" at byte offset %d", i)
		case '&':
			end := strings.IndexByte(s[i:], ';')
			if end < 0 || !entity(s[i+1:i+end]) {
				return "", nil, fmt.Errorf("character entity expected at byte offset %d", i)
			}
			out.WriteString(html.UnescapeString(s[i : i+end+1]))
			i += end + 1
		default:
			out.WriteByte(s[i])
			i++
		}
	}
	if len(open) > 0 {
		return "", nil, fmt.Errorf("can't find end tag corresponding to start tag %q", last(open))
	}
	return out.String(), links, nil
}

func last(open []string) string {
	if len(open) == 0 {
		return ""
	}
	return open[len(open)-1]
}

func entity(name string) bool {
	if slices.Contains(entities, name) {
		return true
	}
	if n, ok := strings.CutPrefix(name, "#"); ok {
		base := 10
		if x, ok := strings.CutPrefix(n, "x"); ok {
			n, base = x, 16
		}
		_, err := strconv.ParseUint(n, base, 32)
		return err == nil
	}
	return false
}

// attr is the value of attribute name in attrs (`href="…"`), decoded.
func attr(attrs, name string) (string, bool) {
	rest, ok := strings.CutPrefix(strings.TrimSpace(attrs), name+`="`)
	if !ok {
		return "", false
	}
	v, _, ok := strings.Cut(rest, `"`)
	return html.UnescapeString(v), ok
}

func text(v any) string {
	switch v := v.(type) {
	case string:
		return v
	case float64:
		return strconv.FormatInt(int64(v), 10)
	}
	return ""
}

func truthy(v any) bool {
	switch v := v.(type) {
	case bool:
		return v
	case string:
		return v == "true"
	}
	return false
}

func reply(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

// fail writes the Bot API's error envelope.
func fail(w http.ResponseWriter, code int, description string, retryAfter int) {
	body := map[string]any{"ok": false, "error_code": code, "description": description}
	if retryAfter > 0 {
		body["parameters"] = map[string]any{"retry_after": retryAfter}
	}
	reply(w, code, body)
}
