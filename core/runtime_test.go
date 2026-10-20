package core_test

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/itsKontra/ddash/core"
	"github.com/itsKontra/ddash/core/coretest"
	"github.com/itsKontra/ddash/core/internal/sample"
	"github.com/itsKontra/ddash/core/runtime"
	"github.com/itsKontra/ddash/sdk"
)

// launcher wraps the in-process launcher, recording launches and letting a
// test hold or fail them.
type launcher struct {
	runtime.Launcher
	// before runs before each launch; an error fails the launch.
	before func(ctx context.Context, name string) error

	mu       sync.Mutex
	launched []string
}

func (l *launcher) Launch(ctx context.Context, spec runtime.LaunchSpec) (runtime.Process, error) {
	if l.before != nil {
		if err := l.before(ctx, spec.Name); err != nil {
			return nil, err
		}
	}
	l.mu.Lock()
	l.launched = append(l.launched, spec.Name)
	l.mu.Unlock()
	return l.Launcher.Launch(ctx, spec)
}

func (l *launcher) order() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.Clone(l.launched)
}

func TestAComponentThatIsntUpYetIsUnavailableWhileHTTPServes(t *testing.T) {
	release := make(chan struct{})
	l := &launcher{
		Launcher: runtime.InProcess(nil, greeter(sample.Hooks{})),
		before: func(ctx context.Context, _ string) error {
			select {
			case <-release:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		},
	}
	h := coretest.New(t, coretest.Options{
		Components: []sdk.Definition{greeter(sample.Hooks{})},
		Plugins:    []coretest.Plugin{hello},
		Launcher:   l,
	})
	h.SeedConfig("greeter", configured)
	h.Install("hello")

	h.WaitState("greeter", runtime.Starting)
	wantError(t, h.Call("hello", "greeter", "Greet", `{"name":"Ada"}`), http.StatusServiceUnavailable, "unavailable", "greeter is starting")

	close(release)
	h.WaitState("greeter", runtime.OK)
	if res := h.Call("hello", "greeter", "Greet", `{"name":"Ada"}`); res.Code != http.StatusOK {
		t.Errorf("once up: status %d: %s", res.Code, res.Body)
	}
}

func TestComponentsStartInDependencyOrderWithoutWaitingOnHealth(t *testing.T) {
	healthy := make(chan struct{})
	t.Cleanup(func() { close(healthy) })
	slowHealth := sample.Hooks{Health: func(ctx context.Context) sdk.Health {
		select {
		case <-healthy:
		case <-ctx.Done():
		}
		return sdk.Health{State: sdk.OK}
	}}
	defs := []sdk.Definition{
		sample.Define("relay", []string{"greeter"}, sample.Hooks{}),
		greeter(slowHealth),
	}
	l := &launcher{Launcher: runtime.InProcess(nil, defs...)}
	h := coretest.New(t, coretest.Options{
		Components: defs,
		Plugins:    []coretest.Plugin{{Name: "relayer", Requires: []string{"relay"}}},
		Launcher:   l,
	})
	h.SeedConfig("greeter", configured)
	h.SeedConfig("relay", configured)

	h.Install("relayer")
	h.WaitState("relay", runtime.OK)

	if got := h.Status("greeter"); got.State != runtime.Starting {
		t.Errorf("greeter is %s, want still starting: its health hasn't answered", got.State)
	}
	if got, want := l.order(), []string{"greeter", "relay"}; !slices.Equal(got, want) {
		t.Errorf("launched %v, want %v", got, want)
	}
}

func TestHealthIsPolledRightAfterStartAndEveryThirtySeconds(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var mu sync.Mutex
		polls, health := 0, sdk.Health{State: sdk.OK}
		hooks := sample.Hooks{Health: func(context.Context) sdk.Health {
			mu.Lock()
			defer mu.Unlock()
			polls++
			return health
		}}
		h := coretest.New(t, coretest.Options{
			Components: []sdk.Definition{greeter(hooks)},
			Plugins:    []coretest.Plugin{hello},
		})
		h.SeedConfig("greeter", configured)
		h.Install("hello")
		synctest.Wait()

		mu.Lock()
		if polls != 1 || h.Status("greeter").State != runtime.OK {
			t.Errorf("after Start: %d polls, %s; want 1, ok", polls, h.Status("greeter").State)
		}
		health = sdk.Health{State: sdk.Degraded, Message: "media unreachable"}
		mu.Unlock()

		time.Sleep(runtime.HealthInterval)
		synctest.Wait()

		mu.Lock()
		defer mu.Unlock()
		want := runtime.Status{State: runtime.Degraded, Message: "media unreachable"}
		if got := h.Status("greeter"); polls != 2 || got != want {
			t.Errorf("30 s later: %d polls, %v; want 2, %v", polls, got, want)
		}
	})
}

func TestAFailedLaunchIsDownAndRetried(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		failures := 2
		l := &launcher{
			Launcher: runtime.InProcess(nil, greeter(sample.Hooks{})),
			before: func(context.Context, string) error {
				if failures > 0 {
					failures--
					return errors.New("exec format error")
				}
				return nil
			},
		}
		h := coretest.New(t, coretest.Options{
			Components: []sdk.Definition{greeter(sample.Hooks{})},
			Plugins:    []coretest.Plugin{hello},
			Launcher:   l,
		})
		h.SeedConfig("greeter", configured)
		h.Install("hello")
		synctest.Wait()

		if got := h.Status("greeter"); got.State != runtime.Down || !strings.Contains(got.Message, "exec format error") {
			t.Errorf("after a failed launch: %v, want down with the error", got)
		}

		time.Sleep(time.Minute)
		synctest.Wait()

		if got := h.Status("greeter"); got.State != runtime.OK {
			t.Errorf("a minute later: %v, want ok after retrying", got)
		}
	})
}

func TestTheInstalledSetSurvivesARestart(t *testing.T) {
	opts := coretest.Options{
		Components: []sdk.Definition{greeter(sample.Hooks{})},
		Plugins:    []coretest.Plugin{hello},
		DataDir:    t.TempDir(),
	}
	first := coretest.New(t, opts)
	first.SeedConfig("greeter", configured)
	first.Install("hello")
	first.WaitState("greeter", runtime.OK)
	first.Shutdown()

	second := coretest.New(t, opts)

	second.WaitState("greeter", runtime.OK)
	if res := second.Call("hello", "greeter", "Greet", `{"name":"Ada"}`); res.Code != http.StatusOK {
		t.Errorf("after restart: status %d: %s", res.Code, res.Body)
	}
}

func TestACatalogueWithAnUnresolvedRequiresIsRefused(t *testing.T) {
	dir := t.TempDir()
	catalogue := `{"release":"dev","plugins":[{"name":"hello","requires":["greeter"]}],"components":[]}`
	if err := os.WriteFile(filepath.Join(dir, "catalogue.json"), []byte(catalogue), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := core.New(t.Context(), core.Options{
		DataDir:  t.TempDir(),
		Release:  runtime.Dir(dir),
		Launcher: runtime.InProcess(slog.Default()),
	})

	if err == nil || !strings.Contains(err.Error(), "hello requires greeter") {
		t.Errorf("err = %v, want hello's unresolved requires named", err)
	}
}
