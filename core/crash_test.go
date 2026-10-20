package core_test

import (
	"context"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/itsKontra/ddash/core/coretest"
	"github.com/itsKontra/ddash/core/internal/sample"
	"github.com/itsKontra/ddash/core/runtime"
	"github.com/itsKontra/ddash/sdk"
)

// crashing wraps a launcher so a test can make a running Component's
// process end on its own, as a crash would.
type crashing struct {
	runtime.Launcher

	mu       sync.Mutex
	running  map[string]*crashingProcess
	launches map[string]int
}

func newCrashing(l runtime.Launcher) *crashing {
	return &crashing{Launcher: l, running: map[string]*crashingProcess{}, launches: map[string]int{}}
}

type crashingProcess struct {
	runtime.Process
	crash chan struct{}
	done  chan struct{}
}

func (p *crashingProcess) Done() <-chan struct{} { return p.done }

func (l *crashing) Launch(ctx context.Context, spec runtime.LaunchSpec) (runtime.Process, error) {
	proc, err := l.Launcher.Launch(ctx, spec)
	if err != nil {
		return nil, err
	}
	p := &crashingProcess{Process: proc, crash: make(chan struct{}), done: make(chan struct{})}
	go func() {
		select {
		case <-p.crash:
		case <-proc.Done():
		}
		close(p.done)
	}()
	l.mu.Lock()
	defer l.mu.Unlock()
	l.running[spec.Name] = p
	l.launches[spec.Name]++
	return p, nil
}

// crash ends name's running process.
func (l *crashing) crash(name string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	close(l.running[name].crash)
}

func (l *crashing) launched(name string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.launches[name]
}

func crashHarness(t *testing.T, plugin coretest.Plugin, defs ...sdk.Definition) (*coretest.Harness, *crashing) {
	l := newCrashing(runtime.InProcess(nil, defs...))
	h := coretest.New(t, coretest.Options{
		Components: defs,
		Plugins:    []coretest.Plugin{plugin},
		Launcher:   l,
	})
	for _, d := range defs {
		h.SeedConfig(d.Name(), configured)
	}
	h.Install(plugin.Name)
	return h, l
}

func TestTheCatalogueOffersRestartForAComponentHeldDownAndTheCoreAPIRestartsIt(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h, l := crashHarness(t, hello, greeter(sample.Hooks{}))
		synctest.Wait()
		greeterIn := func() map[string]any {
			plugins := coreJSON(t, h, "ListCatalogue", `{}`)["plugins"].([]any)
			return plugins[0].(map[string]any)["components"].([]any)[0].(map[string]any)
		}

		l.crash("greeter")
		synctest.Wait()
		if got := greeterIn(); got["state"] != "crashed" || got["held"] != nil {
			t.Errorf("after one crash, the Catalogue shows greeter %v, want crashed, not held", got)
		}
		for range 4 { // 5 crashes in all
			time.Sleep(30 * time.Second)
			synctest.Wait()
			l.crash("greeter")
		}
		synctest.Wait()
		if got := greeterIn(); got["state"] != "crashed" || got["held"] != true {
			t.Fatalf("after 5 crashes, the Catalogue shows greeter %v, want crashed and held", got)
		}

		coreJSON(t, h, "Restart", `{"component":"greeter"}`)
		synctest.Wait()

		if got := greeterIn(); got["state"] != "ok" || got["held"] != nil {
			t.Errorf("after Restart, the Catalogue shows greeter %v, want ok", got)
		}
		entries := coreEntries(h)
		if last := entries[len(entries)-1]; last.Method != "Restart" || !last.Disruptive || last.Username != "test" {
			t.Errorf("last core entry = %+v, want operator test's disruptive Restart", last)
		}
	})
}

func TestACrashedComponentRestartsWithExponentialBackoff(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h, l := crashHarness(t, hello, greeter(sample.Hooks{}))
		synctest.Wait()

		l.crash("greeter")
		synctest.Wait()
		if got := h.Status("greeter"); got.State != runtime.Crashed {
			t.Fatalf("after a crash: %v, want crashed", got)
		}
		time.Sleep(time.Second)
		synctest.Wait()
		if got := h.Status("greeter"); got.State != runtime.OK || l.launched("greeter") != 2 {
			t.Fatalf("1 s after the crash: %v after %d launches, want ok after 2", got, l.launched("greeter"))
		}

		l.crash("greeter")
		time.Sleep(time.Second)
		synctest.Wait()
		if got := h.Status("greeter"); got.State != runtime.Crashed {
			t.Errorf("1 s after a second crash: %v, want still crashed", got)
		}
		time.Sleep(time.Second)
		synctest.Wait()
		if got := h.Status("greeter"); got.State != runtime.OK {
			t.Errorf("2 s after a second crash: %v, want ok", got)
		}
	})
}

func TestAComponentCrashingFiveTimesInTenMinutesStaysDownUntilRestarted(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h, l := crashHarness(t, hello, greeter(sample.Hooks{}))
		synctest.Wait()

		for range 5 {
			l.crash("greeter")
			time.Sleep(30 * time.Second)
			synctest.Wait()
		}
		time.Sleep(time.Hour)
		synctest.Wait()

		got := h.Status("greeter")
		if got.State != runtime.Crashed || !strings.Contains(got.Message, "restart") {
			t.Errorf("an hour after 5 crashes: %v, want crashed until restarted", got)
		}
		if n := l.launched("greeter"); n != 5 {
			t.Errorf("launched %d times, want 5: none after the fifth crash", n)
		}

		if err := h.Restart("greeter"); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		if got := h.Status("greeter"); got.State != runtime.OK {
			t.Errorf("after Restart: %v, want ok", got)
		}
	})
}

func TestCrashesFurtherApartThanTenMinutesDontAddUp(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h, l := crashHarness(t, hello, greeter(sample.Hooks{}))
		synctest.Wait()

		for range 6 {
			l.crash("greeter")
			time.Sleep(3 * time.Minute)
			synctest.Wait()
		}

		if got := h.Status("greeter"); got.State != runtime.OK {
			t.Errorf("after 6 crashes 3 min apart: %v, want ok", got)
		}
	})
}

func TestACrashNeverRestartsItsDependents(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h, l := crashHarness(t, coretest.Plugin{Name: "relayer", Requires: []string{"relay"}}, greeter(sample.Hooks{}), sample.Define("relay", []string{"greeter"}, sample.Hooks{}))
		synctest.Wait()

		l.crash("greeter")
		time.Sleep(time.Minute)
		synctest.Wait()

		if got := h.Status("greeter"); got.State != runtime.OK {
			t.Errorf("greeter is %v, want ok again", got)
		}
		if got := h.Status("relay"); got.State != runtime.OK {
			t.Errorf("relay is %v, want ok throughout", got)
		}
		if n := l.launched("relay"); n != 1 {
			t.Errorf("relay launched %d times, want once", n)
		}
	})
}
