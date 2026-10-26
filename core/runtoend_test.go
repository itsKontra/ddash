package core_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	ntfyapi "github.com/itsKontra/ddash/components/ntfy-api"
	"github.com/itsKontra/ddash/core/appstate"
	"github.com/itsKontra/ddash/core/coretest"
	"github.com/itsKontra/ddash/core/internal/sample"
	"github.com/itsKontra/ddash/core/internal/sample/samplepb"
	"github.com/itsKontra/ddash/core/runtime"
	"github.com/itsKontra/ddash/sdk"
)

// leaving serves r in the background under a context the test cancels, as
// a browser that leaves mid-call would. ended closes once the host answered.
func leaving(h *coretest.Harness, r *http.Request) (leave context.CancelFunc, ended <-chan struct{}) {
	ctx, cancel := context.WithCancel(r.Context())
	done := make(chan struct{})
	go func() {
		defer close(done)
		h.Do(r.WithContext(ctx))
	}()
	return cancel, done
}

func TestAnActionWhoseBrowserLeftRunsToItsEnd(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		release := make(chan struct{})
		h := coretest.New(t, coretest.Options{
			Components: []sdk.Definition{greeter(sample.Hooks{Release: release})},
			Plugins:    []coretest.Plugin{hello},
		})
		h.SeedConfig("greeter", configured)
		h.Install("hello")
		h.WaitState("greeter", runtime.OK)
		r := coretest.ConnectRequest("greeter", "WaitAction", `{}`)
		r.Header.Set(coretest.PluginHeader, "hello")

		leave, ended := leaving(h, r)
		time.Sleep(time.Second)
		leave()
		time.Sleep(time.Second)
		close(release)
		<-ended

		if entries := componentEntries(h); len(entries) != 1 || entries[0].Outcome != appstate.OK {
			t.Errorf("entries = %+v, want WaitAction ok", entries)
		}
	})
}

func TestAnActionWhoseCallingComponentLeftRunsToItsEnd(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		release := make(chan struct{})
		// relay gives up on greeter's WaitAction after a second.
		leave := sample.Hooks{Act: func(ctx context.Context, env *sdk.Env, _ *samplepb.ActRequest) (*samplepb.ActResponse, error) {
			ctx, cancel := context.WithTimeout(ctx, time.Second)
			defer cancel()
			_, _ = sdk.Require(env, samplepb.NewSampleServiceClient).WaitAction(ctx, &samplepb.WaitActionRequest{})
			return &samplepb.ActResponse{}, nil
		}}
		h := newRelay(t, leave, sample.Hooks{Release: release})

		if res := h.Call("relayer", "relay", "Act", `{}`); res.Code != http.StatusOK {
			t.Fatalf("Act: status %d: %s", res.Code, res.Body)
		}
		time.Sleep(time.Second)
		close(release)
		synctest.Wait()

		if entries := componentEntries(h); len(entries) != 2 || entries[1].Method != "WaitAction" || entries[1].Outcome != appstate.OK {
			t.Errorf("entries = %+v, want greeter's WaitAction ok", entries)
		}
	})
}

func TestACoreActionWhoseBrowserLeftRunsToItsEnd(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		release := make(chan struct{})
		// Start on the saved config waits until released.
		slow := sample.Hooks{Start: func(_ context.Context, _ *sdk.Env, cfg *samplepb.SampleConfig) error {
			if cfg.GetGreeting() == "slow" {
				<-release
			}
			return nil
		}}
		h := coretest.New(t, coretest.Options{
			Components: []sdk.Definition{greeter(slow)},
			Plugins:    []coretest.Plugin{hello},
		})
		h.SeedConfig("greeter", configured)
		h.Install("hello")
		h.WaitState("greeter", runtime.OK)

		leave, ended := leaving(h, coretest.CoreRequest("SaveConfig", `{"owner":"greeter","values":{"target":"world","greeting":"slow"}}`))
		time.Sleep(time.Second)
		leave()
		time.Sleep(time.Second)
		close(release)
		<-ended

		if entry := h.AuditLog()[0]; entry.Method != "SaveConfig" || entry.Outcome != appstate.OK {
			t.Errorf("newest entry = %+v, want SaveConfig ok", entry)
		}
	})
}

func TestATestActionWhoseBrowserLeftRunsToItsEnd(t *testing.T) {
	publishing, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		once.Do(func() { close(publishing) })
		<-release
		_, _ = io.WriteString(w, `{"id":"sPs71M8A2T"}`)
	}))
	defer srv.Close()
	h := coretest.New(t, coretest.Options{Components: []sdk.Definition{ntfyapi.Definition()}, Plugins: []coretest.Plugin{notify}})
	h.Install("notify")
	h.WaitState("ntfy-api", runtime.Unconfigured)
	coreJSON(t, h, "SaveConfig", `{"owner":"ntfy-api","values":{"server":"`+srv.URL+`","topic":"ddash-alerts"}}`)

	leave, ended := leaving(h, coretest.CoreRequest("RunTestAction", `{"owner":"ntfy-api"}`))
	<-publishing
	leave()
	// Ending this early is the Action cut short.
	select {
	case <-ended:
	case <-time.After(200 * time.Millisecond):
	}
	close(release)
	<-ended
	if e := lastEntry(h); e.Method != "SendTest" || e.Outcome != appstate.OK {
		t.Errorf("newest entry = %+v, want SendTest ok", e)
	}
}

func TestAGetterWhoseBrowserLeftIsCancelled(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newHello(t, configured)
		h.WaitState("greeter", runtime.OK)
		r := coretest.ConnectRequest("greeter", "Wait", `{}`)
		r.Header.Set(coretest.PluginHeader, "hello")
		start := time.Now()

		leave, ended := leaving(h, r)
		time.Sleep(time.Second)
		leave()
		<-ended

		if took := time.Since(start); took != time.Second {
			t.Errorf("Wait ended after %v, want once the browser left at 1s", took)
		}
	})
}
