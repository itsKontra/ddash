package telegramfake_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/itsKontra/ddash/components/telegram-api/telegramfake"
)

type answer struct {
	OK          bool   `json:"ok"`
	ErrorCode   int    `json:"error_code"`
	Description string `json:"description"`
	Result      struct {
		MessageID int64  `json:"message_id"`
		Username  string `json:"username"`
	} `json:"result"`
}

func call(t *testing.T, b *telegramfake.Bot, token, method, body string) answer {
	t.Helper()
	srv := httptest.NewServer(b)
	defer srv.Close()
	resp, err := http.Post(srv.URL+"/bot"+token+"/"+method, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var a answer
	if err := json.NewDecoder(resp.Body).Decode(&a); err != nil {
		t.Fatal(err)
	}
	if a.ErrorCode != 0 && a.ErrorCode != resp.StatusCode {
		t.Errorf("error_code %d on a %d", a.ErrorCode, resp.StatusCode)
	}
	return a
}

func newBot(t *testing.T) *telegramfake.Bot {
	return telegramfake.New("../testdata/baseline", telegramfake.Options{Miss: func(request string, err error) { t.Errorf("fixture: %v", err) }})
}

func TestHTMLIsParsedAsTelegramDoes(t *testing.T) {
	b := newBot(t)
	for _, tc := range []struct{ text, want string }{
		{"<b>NAS</b> &lt;down&gt; &amp; &quot;out&quot; &#33;", ""},
		{"NAS <down>", `Bad Request: can't parse entities: unsupported start tag "down" at byte offset 4`},
		{"A & B", "Bad Request: can't parse entities: character entity expected at byte offset 2"},
		{"1 > 0", `Bad Request: can't parse entities: unexpected ">" at byte offset 2`},
		{"<b>NAS", `Bad Request: can't parse entities: can't find end tag corresponding to start tag "b"`},
	} {
		body, _ := json.Marshal(map[string]any{"chat_id": 123456789, "text": tc.text, "parse_mode": "HTML"})
		a := call(t, b, telegramfake.DefaultToken, "sendMessage", string(body))
		if a.Description != tc.want {
			t.Errorf("%q: %q, want %q", tc.text, a.Description, tc.want)
		}
	}
	got := b.Messages()
	if len(got) != 1 || got[0].Plain != `NAS <down> & "out" !` || got[0].ChatID != telegramfake.DefaultChat {
		t.Errorf("sent %+v, want the one valid message, decoded", got)
	}
}

func TestAWrongTokenIsUnauthorizedAndGetMeComesFromTheFixtures(t *testing.T) {
	b := newBot(t)

	if a := call(t, b, "1:wrong", "getMe", ""); a.ErrorCode != 401 || a.Description != "Unauthorized" {
		t.Errorf("wrong token: %+v, want 401 Unauthorized", a)
	}
	if a := call(t, b, telegramfake.DefaultToken, "getMe", ""); !a.OK || a.Result.Username != "ddash_home_bot" {
		t.Errorf("getMe: %+v, want the fixture's bot", a)
	}
	want := []string{"POST /bot1:wrong/getMe", "POST /bot<token>/getMe"}
	if diff := cmp.Diff(want, b.Log()); diff != "" {
		t.Errorf("log (-want +got):\n%s", diff)
	}
}
