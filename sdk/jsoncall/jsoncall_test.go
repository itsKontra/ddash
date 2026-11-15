package jsoncall_test

import (
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/itsKontra/ddash/sdk"
	"github.com/itsKontra/ddash/sdk/jsoncall"
)

func serve(t *testing.T, h http.HandlerFunc) string {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv.URL
}

func TestDoSendsTheBodyAsJSONWithTheCallersHeadersAndReturnsStatusAndBody(t *testing.T) {
	var got *http.Request
	var sent []byte
	url := serve(t, func(w http.ResponseWriter, r *http.Request) {
		got = r
		sent, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusTeapot) // any status is a Reply: its meaning is the caller's
		w.Write([]byte(`{"why":"short and stout"}`))
	})

	reply, err := jsoncall.Do(t.Context(), http.DefaultClient, jsoncall.Request{
		Method: http.MethodPatch, URL: url + "/alias?id=3", Body: map[string]any{"id": 3},
		Header: http.Header{"x-api-key": {"k"}, "Accept": {"application/json"}},
	})

	if err != nil {
		t.Fatal(err)
	}
	if reply.Status != http.StatusTeapot || string(reply.Body) != `{"why":"short and stout"}` {
		t.Errorf("reply = %d %s", reply.Status, reply.Body)
	}
	if got.Method != http.MethodPatch || got.URL.String() != "/alias?id=3" || string(sent) != `{"id":3}` {
		t.Errorf("sent %s %s %s", got.Method, got.URL, sent)
	}
	if got.Header.Get("X-Api-Key") != "k" || got.Header.Get("Accept") != "application/json" || got.Header.Get("Content-Type") != "application/json" {
		t.Errorf("headers = %v, want the caller's and a JSON Content-Type", got.Header)
	}
}

func TestDoSendsNoBodyAndNoContentTypeWithoutOne(t *testing.T) {
	url := serve(t, func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		if len(b) != 0 || r.Header.Get("Content-Type") != "" {
			t.Errorf("a GET carried %q as %q", b, r.Header.Get("Content-Type"))
		}
	})
	if _, err := jsoncall.Do(t.Context(), http.DefaultClient, jsoncall.Request{Method: http.MethodGet, URL: url}); err != nil {
		t.Fatal(err)
	}
}

// stage is how far Do got, and the gRPC code sdk.Unanswered gives it.
func stage(t *testing.T, err error) (jsoncall.Stage, codes.Code, string) {
	t.Helper()
	var call *jsoncall.Error
	if !errors.As(err, &call) {
		t.Fatalf("err = %v, want a *jsoncall.Error", err)
	}
	st := status.Convert(sdk.Unanswered(err, "pfSense"))
	return call.Stage, st.Code(), st.Message()
}

func TestWhatCantBeSentIsTheCallersBugAndInternal(t *testing.T) {
	_, err := jsoncall.Do(t.Context(), http.DefaultClient, jsoncall.Request{Method: http.MethodPost, URL: "http://198.18.1.1", Body: make(chan int)})
	if s, code, _ := stage(t, err); s != jsoncall.Build || code != codes.Internal {
		t.Errorf("a body that isn't JSON: stage %v, code %v; want Build and Internal", s, code)
	}
	_, err = jsoncall.Do(t.Context(), http.DefaultClient, jsoncall.Request{Method: "NOT A METHOD", URL: "http://198.18.1.1"})
	if s, code, _ := stage(t, err); s != jsoncall.Build || code != codes.Internal {
		t.Errorf("a bad method: stage %v, code %v; want Build and Internal", s, code)
	}
}

func TestASystemThatDoesntAnswerIsUnavailableNamedWithoutItsURL(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	url := "http://" + l.Addr().String() + "/api?key=secret"
	l.Close()

	_, err = jsoncall.Do(t.Context(), http.DefaultClient, jsoncall.Request{Method: http.MethodGet, URL: url})

	s, code, msg := stage(t, err)
	if s != jsoncall.Reach || code != codes.Unavailable || !strings.HasPrefix(msg, "pfSense unreachable: ") {
		t.Errorf("stage %v, code %v, %q; want Reach, Unavailable, pfSense unreachable", s, code, msg)
	}
	// The URL may hold a credential (a bot token, a key in the query).
	if strings.Contains(msg, "secret") || strings.Contains(err.Error(), "secret") {
		t.Errorf("the error names the URL: %q / %q", msg, err)
	}
}

func TestAnAnswerOverTheCapOrBrokenOffIsUnavailable(t *testing.T) {
	big := serve(t, func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte(strings.Repeat("x", 2000))) })
	_, err := jsoncall.Do(t.Context(), http.DefaultClient, jsoncall.Request{Method: http.MethodGet, URL: big, MaxResponse: 1000})
	if s, code, msg := stage(t, err); s != jsoncall.Read || code != codes.Unavailable || !strings.Contains(msg, "larger than 1000 bytes") {
		t.Errorf("an answer over the cap: stage %v, code %v, %q", s, code, msg)
	}
	if reply, err := jsoncall.Do(t.Context(), http.DefaultClient, jsoncall.Request{Method: http.MethodGet, URL: big, MaxResponse: 2000}); err != nil || len(reply.Body) != 2000 {
		t.Errorf("an answer at the cap: %d bytes, %v; want all of it", len(reply.Body), err)
	}

	broken := serve(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", "500")
		w.Write([]byte("only the start"))
		w.(http.Flusher).Flush() // the status and the start arrive; the rest never does
		panic(http.ErrAbortHandler)
	})
	_, err = jsoncall.Do(t.Context(), http.DefaultClient, jsoncall.Request{Method: http.MethodGet, URL: broken})
	if s, code, msg := stage(t, err); s != jsoncall.Read || code != codes.Unavailable || !strings.HasPrefix(msg, "pfSense's answer broke off") {
		t.Errorf("an answer that breaks off: stage %v, code %v, %q", s, code, msg)
	}
}
