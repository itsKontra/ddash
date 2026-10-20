package ntfyapi_test

import (
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/google/go-cmp/cmp"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	ntfyapi "github.com/itsKontra/ddash/components/ntfy-api"
	"github.com/itsKontra/ddash/components/ntfy-api/ntfyapipb"
	"github.com/itsKontra/ddash/sdk"
	"github.com/itsKontra/ddash/sdk/fixture"
	"github.com/itsKontra/ddash/sdk/sdktest"
)

// published is one request the fake ntfy server got.
type published struct {
	Method, Path, Auth string
	Body               map[string]any
}

// server fakes an ntfy server answering from a scenario and records every
// publish.
type server struct {
	*httptest.Server
	mu       sync.Mutex
	scenario string
	got      []published
}

func ntfy(t *testing.T, scenario string) *server {
	t.Helper()
	s := &server{scenario: scenario}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		p := published{Method: r.Method, Path: r.URL.Path, Auth: r.Header.Get("Authorization")}
		if err := json.Unmarshal(raw, &p.Body); err != nil {
			t.Errorf("publish body %q isn't JSON: %v", raw, err)
		}
		s.mu.Lock()
		s.got = append(s.got, p)
		scenario := s.scenario
		s.mu.Unlock()
		fixture.Scenario("testdata/"+scenario, func(request string, err error) { t.Errorf("fixture: %v", err) }).ServeHTTP(w, r)
	}))
	t.Cleanup(s.Close)
	return s
}

func (s *server) use(scenario string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.scenario = scenario
}

func (s *server) published() []published {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]published(nil), s.got...)
}

func start(t *testing.T, cfg *ntfyapipb.NtfyApiConfig) (*sdktest.Harness, ntfyapipb.NtfyApiServiceClient) {
	t.Helper()
	h := sdktest.New(t, ntfyapi.Definition())
	h.MustStart(cfg)
	return h, ntfyapipb.NewNtfyApiServiceClient(h.Conn)
}

func ptr[T any](v T) *T { return &v }

var down = &ntfyapipb.Notification{
	Title:    "Jellyfin down",
	Message:  "timeout",
	Priority: 4,
	Tags:     []string{"red_circle"},
	Click:    "https://ddash.home.example/checks/7",
}

func send(t *testing.T, c ntfyapipb.NtfyApiServiceClient, n *ntfyapipb.Notification) (*ntfyapipb.SendResponse, error) {
	t.Helper()
	return c.Send(t.Context(), &ntfyapipb.SendRequest{Notification: n})
}

func TestSendPostsTheNotificationAsJSONWithTheBearerToken(t *testing.T) {
	srv := ntfy(t, "baseline")
	h, c := start(t, &ntfyapipb.NtfyApiConfig{Server: ptr(srv.URL), Topic: "ddash-alerts", Token: "tk_0123456789abcdef"})

	resp, err := send(t, c, down)
	if err != nil {
		t.Fatalf("Send: %v", err)
	}

	if resp.GetId() != "sPs71M8A2T" {
		t.Errorf("id = %q, want ntfy's", resp.GetId())
	}
	want := []published{{
		Method: "POST", Path: "/", Auth: "Bearer tk_0123456789abcdef",
		Body: map[string]any{
			"topic": "ddash-alerts", "title": "Jellyfin down", "message": "timeout",
			"priority": 4.0, "tags": []any{"red_circle"}, "click": "https://ddash.home.example/checks/7",
		},
	}}
	if diff := cmp.Diff(want, srv.published()); diff != "" {
		t.Errorf("published (-want +got):\n%s", diff)
	}
	if got := h.Health(); got.State != sdk.OK {
		t.Errorf("health after a delivery = %v, want ok", got)
	}
}

func TestAnOpenTopicIsPublishedWithoutAuthorization(t *testing.T) {
	srv := ntfy(t, "baseline")
	_, c := start(t, &ntfyapipb.NtfyApiConfig{Server: ptr(srv.URL), Topic: "ddash-alerts"})

	if _, err := send(t, c, &ntfyapipb.Notification{Title: "Jellyfin up again", Message: "Down for 4 min"}); err != nil {
		t.Fatalf("Send: %v", err)
	}

	got := srv.published()
	if len(got) != 1 || got[0].Auth != "" {
		t.Fatalf("published %v, want one without Authorization", got)
	}
	for _, field := range []string{"priority", "tags", "click"} {
		if v, ok := got[0].Body[field]; ok {
			t.Errorf("%s = %v sent, want it left out when unset", field, v)
		}
	}
}

func TestAFailedDeliveryMakesItDownWithTheErrorUntilOneSucceeds(t *testing.T) {
	srv := ntfy(t, "unauthorized")
	h, c := start(t, &ntfyapipb.NtfyApiConfig{Server: ptr(srv.URL), Topic: "ddash-alerts", Token: "tk_revoked"})
	if got := h.Health(); got.State != sdk.OK {
		t.Errorf("health before any delivery = %v, want ok", got)
	}

	_, err := send(t, c, down)

	if status.Code(err) != codes.Unavailable || status.Convert(err).Message() != "ntfy answered 401: unauthorized" {
		t.Errorf("Send = %v, want Unavailable: ntfy answered 401: unauthorized", err)
	}
	if got := h.Health(); got.State != sdk.Down || got.Message != "last delivery failed: ntfy answered 401: unauthorized" {
		t.Errorf("health = %v, want down with the last error", got)
	}

	srv.use("rate-limited")
	_, _ = send(t, c, down)
	if got := h.Health(); got.Message != "last delivery failed: ntfy answered 429: limit reached: too many requests" {
		t.Errorf("health = %v, want the latest error", got)
	}

	srv.use("baseline")
	if _, err := send(t, c, down); err != nil {
		t.Fatal(err)
	}
	if got := h.Health(); got.State != sdk.OK {
		t.Errorf("health after a delivery worked again = %v, want ok", got)
	}
}

func TestAnUnreachableServerIsADeliveryFailure(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	url := "http://" + l.Addr().String()
	l.Close()
	h, c := start(t, &ntfyapipb.NtfyApiConfig{Server: ptr(url), Topic: "ddash-alerts"})

	_, err = send(t, c, down)

	if status.Code(err) != codes.Unavailable || !strings.Contains(status.Convert(err).Message(), "unreachable") {
		t.Errorf("Send = %v, want Unavailable naming the server unreachable", err)
	}
	if got := h.Health(); got.State != sdk.Down || !strings.Contains(got.Message, "unreachable") {
		t.Errorf("health = %v, want down: unreachable", got)
	}
}

func TestSendTestPublishesATestNotification(t *testing.T) {
	srv := ntfy(t, "baseline")
	_, c := start(t, &ntfyapipb.NtfyApiConfig{Server: ptr(srv.URL), Topic: "ddash-alerts", Token: "tk_0123456789abcdef"})

	resp, err := c.SendTest(t.Context(), &ntfyapipb.SendTestRequest{})
	if err != nil || resp.GetId() != "sPs71M8A2T" {
		t.Fatalf("SendTest = %v, %v", resp, err)
	}

	got := srv.published()
	if len(got) != 1 || got[0].Body["title"] != "ddash test" || got[0].Body["topic"] != "ddash-alerts" || got[0].Auth != "Bearer tk_0123456789abcdef" {
		t.Errorf("published %v, want one test Notification to the topic", got)
	}
}

func TestABadServerOrTopicIsRefusedPerField(t *testing.T) {
	for _, tc := range []struct {
		cfg        *ntfyapipb.NtfyApiConfig
		path, want string
	}{
		{&ntfyapipb.NtfyApiConfig{Server: ptr("ftp://ntfy.home.example"), Topic: "ddash"}, "server", "must be an http or https URL"},
		{&ntfyapipb.NtfyApiConfig{Server: ptr("https://ntfy.sh"), Topic: "ddash alerts"}, "topic", "must be 1 to 64 letters, digits, - or _"},
		{&ntfyapipb.NtfyApiConfig{Server: ptr("https://ntfy.sh"), Topic: strings.Repeat("a", 65)}, "topic", "must be 1 to 64 letters, digits, - or _"},
	} {
		h := sdktest.New(t, ntfyapi.Definition())
		errs := sdk.FieldErrorsOf(h.Start(tc.cfg))
		if len(errs) != 1 || errs[0].Path != tc.path || errs[0].Message != tc.want {
			t.Errorf("Start(%v) field errors = %v, want %s: %s", tc.cfg, errs, tc.path, tc.want)
		}
	}
}
