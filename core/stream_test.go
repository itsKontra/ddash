package core_test

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"testing/synctest"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/itsKontra/ddash/core/appstate"
	"github.com/itsKontra/ddash/core/coretest"
	"github.com/itsKontra/ddash/core/internal/sample"
	"github.com/itsKontra/ddash/core/internal/sample/samplepb"
	"github.com/itsKontra/ddash/core/runtime"
	"github.com/itsKontra/ddash/sdk"
)

// wantStreamError fails the test unless s is a 200 whose end-of-stream is
// the error code with message ("" for any).
func wantStreamError(t *testing.T, s coretest.Streamed, code, message string) {
	t.Helper()
	if s.Status != http.StatusOK || s.Error == nil || s.Error.Code != code || (message != "" && s.Error.Message != message) {
		t.Errorf("status %d, end-of-stream error %+v; want 200 and %s %q", s.Status, s.Error, code, message)
	}
}

func TestAStreamingActionsMessagesStreamInOrderUnderOneEntry(t *testing.T) {
	h := newHello(t, configured)
	h.WaitState("greeter", runtime.OK)

	s := h.Stream("hello", "greeter", "Progress", `{"count":3}`)

	if s.Status != http.StatusOK || s.Header.Get("Content-Type") != "application/connect+json" {
		t.Errorf("status %d, Content-Type %q; want 200 application/connect+json", s.Status, s.Header.Get("Content-Type"))
	}
	if want := []string{`{"step":1}`, `{"step":2}`, `{"step":3}`}; !slices.Equal(s.Messages, want) || s.Error != nil {
		t.Errorf("streamed %v, error %+v; want %v and a clean end", s.Messages, s.Error, want)
	}
	entries := componentEntries(h)
	if len(entries) != 1 || entries[0].Method != "Progress" || entries[0].Outcome != appstate.OK || requestJSON(t, entries[0])["count"] != 3.0 {
		t.Errorf("entries = %+v, want one Progress ok with its request", entries)
	}
}

func TestAStreamingActionsErrorEndsTheStreamWithItsDetails(t *testing.T) {
	h := newHello(t, configured)
	h.WaitState("greeter", runtime.OK)

	s := h.Stream("hello", "greeter", "Progress", `{"count":3,"failAfter":2}`)

	if want := []string{`{"step":1}`, `{"step":2}`}; !slices.Equal(s.Messages, want) {
		t.Errorf("streamed %v, want %v before the error", s.Messages, want)
	}
	wantStreamError(t, s, "aborted", "stopped at 2")
	if s.Error == nil || len(s.Error.Details) != 1 || s.Error.Details[0].Type != "ddash.sample.ProgressResponse" {
		t.Fatalf("end-of-stream error %+v, want one ddash.sample.ProgressResponse detail", s.Error)
	}
	b, err := base64.RawStdEncoding.DecodeString(s.Error.Details[0].Value)
	if err != nil {
		t.Fatal(err)
	}
	var got samplepb.ProgressResponse
	if err := proto.Unmarshal(b, &got); err != nil || got.GetStep() != 2 {
		t.Errorf("detail = %v (%v), want step 2", &got, err)
	}
	if entries := componentEntries(h); len(entries) != 1 || entries[0].Outcome != "Aborted" || entries[0].Message != "stopped at 2" {
		t.Errorf("entries = %+v, want one Progress Aborted", entries)
	}
}

// Every refusal of a stream is a 200 with an end-of-stream error, or
// connect-web loses its message.
func TestAStreamsRefusalsEndTheStream(t *testing.T) {
	h := newRelay(t, sample.Hooks{}, sample.Hooks{})

	wantStreamError(t, h.Stream("relayer", "greeter", "Progress", `{"count":1}`), "permission_denied", "plugin relayer doesn't require greeter")
	wantStreamError(t, h.Stream("relayer", "relay", "Shout", `{}`), "unimplemented", "relay has no method Shout")
	wantStreamError(t, h.Stream("relayer", "relay", "Progress", `{"count":`), "invalid_argument", "")
	wantStreamError(t, h.Stream("relayer", "nonexistent", "Progress", `{}`), "permission_denied", "plugin relayer doesn't require nonexistent")

	var got []string
	for _, e := range componentEntries(h) {
		got = append(got, e.Target+" "+e.Method+" "+e.Outcome)
	}
	if want := []string{"greeter Progress PermissionDenied", "relay Progress InvalidArgument"}; !slices.Equal(got, want) {
		t.Errorf("Audit Log = %v, want %v", got, want)
	}
}

func TestAStreamIsRefusedWhenTheAuditLogCantRecordIt(t *testing.T) {
	h := newHello(t, configured)
	h.WaitState("greeter", runtime.OK)
	breakAuditLog(t, h)

	s := h.Stream("hello", "greeter", "Progress", `{"count":1}`)

	wantStreamError(t, s, "unavailable", "audit log unavailable")
	if len(s.Messages) > 0 {
		t.Errorf("streamed %v, want nothing: the Component didn't run", s.Messages)
	}
}

// A method is called in its own kind's form only, as connect-go does.
func TestAMethodCalledInTheOtherKindsFormIsUnsupported(t *testing.T) {
	h := newHello(t, configured)
	h.WaitState("greeter", runtime.OK)

	unary := coretest.StreamRequest("greeter", "Greet", `{"name":"Ada"}`)
	unary.Header.Set(coretest.PluginHeader, "hello")
	if res := h.Do(unary); res.Code != http.StatusUnsupportedMediaType {
		t.Errorf("unary Greet as a stream: status %d, want 415: %s", res.Code, res.Body)
	}
	if res := h.Call("hello", "greeter", "Progress", `{"count":1}`); res.Code != http.StatusUnsupportedMediaType {
		t.Errorf("streaming Progress as unary: status %d, want 415: %s", res.Code, res.Body)
	}
	if entries := componentEntries(h); len(entries) != 0 {
		t.Errorf("entries = %+v, want none", entries)
	}
}

// Over a real connection each message reaches the browser as it is sent,
// past anything that would buffer it.
func TestAStreamsMessagesAreFlushedAsTheyAreSent(t *testing.T) {
	release := make(chan struct{})
	h := coretest.New(t, coretest.Options{
		Components: []sdk.Definition{greeter(sample.Hooks{Release: release})},
		Plugins:    []coretest.Plugin{hello},
	})
	h.SeedConfig("greeter", configured)
	h.Install("hello")
	h.WaitState("greeter", runtime.OK)
	srv := httptest.NewServer(h.Host.Handler())
	defer srv.Close()
	call := coretest.StreamRequest("greeter", "Progress", `{"count":2,"waitAfter":1}`)
	// Gives up on the read long before the Action's own deadline.
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	r, err := http.NewRequestWithContext(ctx, http.MethodPost, srv.URL+call.URL.Path, call.Body)
	if err != nil {
		t.Fatal(err)
	}
	r.Header = call.Header
	r.Header.Set(coretest.PluginHeader, "hello")

	res, err := http.DefaultClient.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if got := res.Header.Get("X-Accel-Buffering"); got != "no" {
		t.Errorf("X-Accel-Buffering %q, want no", got)
	}
	flags, first, err := coretest.ReadEnvelope(res.Body)
	if err != nil || flags != 0 || string(first) != `{"step":1}` {
		t.Fatalf("first envelope %#x %s (%v), want step 1 while Progress waits", flags, first, err)
	}
	close(release)
	rest, err := coretest.ReadStream(res.Body)
	if err != nil || !slices.Equal(rest.Messages, []string{`{"step":2}`}) || rest.Error != nil {
		t.Errorf("then %+v (%v), want step 2 and a clean end", rest, err)
	}
}

func TestAStreamWhoseBrowserLeftRunsToItsEnd(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		release := make(chan struct{})
		h := coretest.New(t, coretest.Options{
			Components: []sdk.Definition{greeter(sample.Hooks{Release: release})},
			Plugins:    []coretest.Plugin{hello},
		})
		h.SeedConfig("greeter", configured)
		h.Install("hello")
		h.WaitState("greeter", runtime.OK)
		r := coretest.StreamRequest("greeter", "Progress", `{"count":2,"waitAfter":1}`)
		r.Header.Set(coretest.PluginHeader, "hello")

		leave, ended := leaving(h, r)
		time.Sleep(time.Second)
		leave()
		time.Sleep(time.Second)
		close(release)
		<-ended

		if entries := componentEntries(h); len(entries) != 1 || entries[0].Outcome != appstate.OK {
			t.Errorf("entries = %+v, want Progress ok", entries)
		}
	})
}

// Components call each other unary only: a streaming method is refused
// before it reaches its Component, and audited like any refused Action.
func TestAStreamingMethodCalledFromAComponentIsUnimplemented(t *testing.T) {
	stream := sample.Hooks{Act: func(ctx context.Context, env *sdk.Env, _ *samplepb.ActRequest) (*samplepb.ActResponse, error) {
		s, err := sdk.Require(env, samplepb.NewSampleServiceClient).Progress(ctx, &samplepb.ProgressRequest{Count: 1})
		if err == nil {
			_, err = s.Recv()
		}
		return nil, err
	}}
	h := newRelay(t, stream, sample.Hooks{})

	res := h.Call("relayer", "relay", "Act", `{}`)

	wantError(t, res, http.StatusNotImplemented, "unimplemented", "ddash.sample.SampleService.Progress streams; Components call each other unary only")
	var got []string
	for _, e := range componentEntries(h) {
		got = append(got, e.Target+" "+e.Method+" "+e.Outcome)
	}
	if want := []string{"relay Act Unimplemented", "greeter Progress Unimplemented"}; !slices.Equal(got, want) {
		t.Errorf("Audit Log = %v, want %v", got, want)
	}
}

// failing is a browser's connection that breaks after ok writes: every
// write from then on fails.
type failing struct {
	*httptest.ResponseRecorder
	ok, writes int
}

func (f *failing) Write(b []byte) (int, error) {
	f.writes++
	if f.writes > f.ok {
		return 0, errors.New("connection reset by peer")
	}
	return f.ResponseRecorder.Write(b)
}

func TestAStreamWhoseWriteFailedRunsToItsEndWithoutWritingMore(t *testing.T) {
	h := newHello(t, configured)
	h.WaitState("greeter", runtime.OK)
	r := coretest.StreamRequest("greeter", "Progress", `{"count":3}`)
	r.Header.Set(coretest.PluginHeader, "hello")
	w := &failing{ResponseRecorder: httptest.NewRecorder(), ok: 1}

	h.Host.Handler().ServeHTTP(w, r)

	if w.writes != 2 {
		t.Errorf("%d writes, want 2: step 1, then the failed step 2 and nothing after", w.writes)
	}
	if entries := componentEntries(h); len(entries) != 1 || entries[0].Outcome != appstate.OK {
		t.Errorf("entries = %+v, want Progress ok", entries)
	}
}
