package apps_test

import (
	"context"
	"sync"
	"testing"
	"testing/synctest"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/itsKontra/ddash/components/apps/appspb"
	"github.com/itsKontra/ddash/components/checks/checkspb"
	"github.com/itsKontra/ddash/sdk"
)

// checksFake fakes the checks Component: it records every SetAppChecks.
type checksFake struct {
	checkspb.ChecksServiceClient

	mu   sync.Mutex
	sets [][]*checkspb.AppCheck
	err  error
	// reports are the ReportState calls accepted; reportErr refuses them.
	reports   []*checkspb.ReportStateRequest
	reportErr error
}

func (f *checksFake) SetAppChecks(_ context.Context, req *checkspb.SetAppChecksRequest, _ ...grpc.CallOption) (*checkspb.SetAppChecksResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return nil, f.err
	}
	f.sets = append(f.sets, req.GetChecks())
	return &checkspb.SetAppChecksResponse{}, nil
}

// fail makes checks refuse SetAppChecks with err; nil answers again.
func (f *checksFake) fail(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.err = err
}

// calls counts the sets checks accepted.
func (f *checksFake) calls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.sets)
}

// last is the last set checks accepted.
func (f *checksFake) last() []*checkspb.AppCheck {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.sets) == 0 {
		return nil
	}
	return f.sets[len(f.sets)-1]
}

func appCheck(id, name, url string, enabled bool) *checkspb.AppCheck {
	return &checkspb.AppCheck{AppId: id, Name: name, Url: url, Enabled: enabled}
}

var printer = proxy([]string{"printer.home.example"}, "http://198.18.1.40", "8080")

func TestAppsDeclaresEveryAppsCheckOnceAfterItsStart(t *testing.T) {
	s := start(t, newNginx(jellyfin, printer))
	s.apps(t) // waits for the first poll

	want := []*checkspb.AppCheck{
		appCheck("proxy:jellyfin.home.example", "Jellyfin", "https://jellyfin.home.example", true),
		appCheck("proxy:printer.home.example", "Printer", "http://printer.home.example:8080", true),
	}
	if s.checks.calls() != 1 {
		t.Fatalf("%d SetAppChecks calls after Start, want 1", s.checks.calls())
	}
	if d := diff(want, s.checks.last()); d != "" {
		t.Errorf("App Checks (-want +got):\n%s", d)
	}
}

func TestAppsDeclaresTheSetOnlyWhenItChanges(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		n := newNginx(jellyfin)
		s := start(t, n)
		synctest.Wait()

		afterPoll()
		n.set(jellyfin) // a new revision, the same Proxy Hosts
		afterPoll()
		if got := s.checks.calls(); got != 1 {
			t.Fatalf("%d calls without a change, want only the one after Start", got)
		}

		n.set(jellyfin, printer)
		afterPoll()
		if got := s.checks.calls(); got != 2 {
			t.Fatalf("%d calls after a new Proxy Host, want 2", got)
		}
		if got := len(s.checks.last()); got != 2 {
			t.Errorf("%d App Checks, want the new Proxy Host's too", got)
		}

		n.set(printer)
		afterPoll()
		want := []*checkspb.AppCheck{appCheck("proxy:printer.home.example", "Printer", "http://printer.home.example:8080", true)}
		if d := diff(want, s.checks.last()); d != "" {
			t.Errorf("after a Proxy Host vanished (-want +got):\n%s", d)
		}
	})
}

func TestHidingOrRenamingAnAppRedeclaresItsCheck(t *testing.T) {
	s := start(t, newNginx(jellyfin))
	s.apps(t)

	s.setOverlay(t, &appspb.Overlay{ServerName: "jellyfin.home.example", Name: "Media", Hidden: true})

	want := []*checkspb.AppCheck{appCheck("proxy:jellyfin.home.example", "Media", "https://jellyfin.home.example", false)}
	if d := diff(want, s.checks.last()); d != "" {
		t.Errorf("after hiding and renaming (-want +got):\n%s", d)
	}
}

func TestManualAppsGetChecksToo(t *testing.T) {
	s := start(t, newNginx())
	s.apps(t)

	created, err := s.c.CreateApp(t.Context(), &appspb.CreateAppRequest{App: &appspb.ManualApp{Name: "pfSense", Url: "https://198.18.0.1"}})
	if err != nil {
		t.Fatal(err)
	}
	id := created.GetApp().GetId()
	want := []*checkspb.AppCheck{appCheck(id, "pfSense", "https://198.18.0.1", true)}
	if d := diff(want, s.checks.last()); d != "" {
		t.Errorf("after CreateApp (-want +got):\n%s", d)
	}

	if _, err := s.c.UpdateApp(t.Context(), &appspb.UpdateAppRequest{Id: id, App: &appspb.ManualApp{Name: "Firewall", Url: "https://198.18.0.1:8443"}}); err != nil {
		t.Fatal(err)
	}
	want = []*checkspb.AppCheck{appCheck(id, "Firewall", "https://198.18.0.1:8443", true)}
	if d := diff(want, s.checks.last()); d != "" {
		t.Errorf("after UpdateApp (-want +got):\n%s", d)
	}

	if _, err := s.c.DeleteApp(t.Context(), &appspb.DeleteAppRequest{Id: id}); err != nil {
		t.Fatal(err)
	}
	if got := s.checks.last(); len(got) != 0 {
		t.Errorf("after DeleteApp: %v, want no App Checks", got)
	}
}

func TestNoSetIsDeclaredBeforeNginxConfigWasRead(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		n := newNginx(jellyfin)
		n.fail(status.Error(codes.Unavailable, "nginx-config is down"))
		s := start(t, n)
		synctest.Wait()

		if _, err := s.c.CreateApp(t.Context(), &appspb.CreateAppRequest{App: &appspb.ManualApp{Name: "pfSense", Url: "https://198.18.0.1"}}); err != nil {
			t.Fatal(err)
		}
		if got := s.checks.calls(); got != 0 {
			t.Fatalf("%d calls before a snapshot was read, want none: they'd stop every Proxy Host's Check", got)
		}

		n.fail(nil)
		afterPoll()
		if got := len(s.checks.last()); got != 2 {
			t.Errorf("%d App Checks once nginx-config answers, want the Proxy Host's and the manual App's", got)
		}
	})
}

func TestAFailedDeclarationIsRetriedOnTheNextPoll(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := &checksFake{}
		f.fail(status.Error(codes.Unavailable, "checks is down"))
		s := startWith(t, newNginx(jellyfin), f)
		synctest.Wait()

		want := sdk.Health{State: sdk.Degraded, Message: "checks unavailable: checks is down"}
		if got := s.h.Health(); got != want {
			t.Errorf("health = %v, want %v", got, want)
		}

		f.fail(nil)
		afterPoll()
		if got := f.calls(); got != 1 {
			t.Errorf("%d calls once checks answers, want the retried one", got)
		}
		if got := s.h.Health(); got.State != sdk.OK {
			t.Errorf("health = %v, want ok", got)
		}
	})
}
