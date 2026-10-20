package checks_test

import (
	"testing"
	"testing/synctest"
	"time"

	"github.com/google/go-cmp/cmp"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/testing/protocmp"

	"github.com/itsKontra/ddash/components/checks"
	"github.com/itsKontra/ddash/components/checks/checkspb"
	workloadpb "github.com/itsKontra/ddash/proto/ddash/workload"
	"github.com/itsKontra/ddash/sdk/sdktest"
)

// app is an App's Check as apps declares it, shown.
func app(id, name, url string) *checkspb.AppCheck {
	return &checkspb.AppCheck{AppId: id, Name: name, Url: url, Enabled: true}
}

// hidden is a, its App hidden.
func hidden(a *checkspb.AppCheck) *checkspb.AppCheck {
	a.Enabled = false
	return a
}

func setApps(t *testing.T, c checkspb.ChecksServiceClient, apps ...*checkspb.AppCheck) {
	t.Helper()
	if _, err := c.SetAppChecks(t.Context(), &checkspb.SetAppChecksRequest{Checks: apps}); err != nil {
		t.Fatalf("SetAppChecks: %v", err)
	}
}

// appCheck is App id's listed Check, or nil.
func appCheck(t *testing.T, c checkspb.ChecksServiceClient, id string) *checkspb.Check {
	t.Helper()
	for _, ch := range list(t, c) {
		if ch.GetAppId() == id {
			return ch
		}
	}
	return nil
}

var (
	jellyfin = app("proxy:jellyfin.home.example", "Jellyfin", "https://jellyfin.home.example")
	printer  = app("proxy:printer.home.example", "Printer", "http://printer.home.example:8080")
)

func TestEachAppGetsAnHTTPCheckProbedAtOnce(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		l := newLab()
		l.set("printer.home.example:8080", "http 200")
		_, c := startIn(t, l)

		setApps(t, c, printer)
		synctest.Wait()

		got := appCheck(t, c, "proxy:printer.home.example")
		if got == nil {
			t.Fatal("no Check for the App")
		}
		s := got.GetSpec()
		if s.GetName() != "Printer" || s.GetType() != checkspb.CheckType_CHECK_TYPE_HTTP || s.GetTarget() != "http://printer.home.example:8080" {
			t.Errorf("spec %v, want an HTTP Check of the App's URL named after it", s)
		}
		if got.GetState() != checkspb.State_STATE_UP || got.GetLastBeat() == nil {
			t.Errorf("state %v (last beat %v), want up after its first beat", got.GetState(), got.GetLastBeat())
		}
	})
}

func TestAnHTTPCheckIsDownAfterThreeFailuresWithTheStatusAsReason(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		l := newLab()
		l.set("printer.home.example:8080", "http 502")
		_, c := startIn(t, l)
		setApps(t, c, printer)
		synctest.Wait()
		after(2 * Interval11())

		got := appCheck(t, c, "proxy:printer.home.example")
		if got.GetState() != checkspb.State_STATE_DOWN || got.GetReason() != "HTTP 502" {
			t.Errorf("%v (%q), want down: HTTP 502", got.GetState(), got.GetReason())
		}
	})
}

func TestAnHTTPProbeTimesOutAfterTenSeconds(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		l := newLab()
		l.set("printer.home.example:8080", "hang")
		_, c := startIn(t, l)
		setApps(t, c, printer)
		after(checks.Timeout - time.Millisecond)
		if got := appCheck(t, c, "proxy:printer.home.example"); got.GetLastBeat() != nil {
			t.Fatalf("a beat was recorded before the timeout, at %v", got.GetLastBeat().AsTime())
		}

		after(time.Millisecond)
		if got := appCheck(t, c, "proxy:printer.home.example"); got.GetReason() != "timeout" || got.GetFailures() != 1 {
			t.Errorf("%d failures (%q), want 1: timeout", got.GetFailures(), got.GetReason())
		}
	})
}

func TestAHiddenAppsCheckIsDisabled(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		l := newLab()
		l.set("printer.home.example:8080", "http 200")
		_, c := startIn(t, l)

		setApps(t, c, hidden(app("proxy:printer.home.example", "Printer", "http://printer.home.example:8080")))
		after(3 * Interval11())
		got := appCheck(t, c, "proxy:printer.home.example")
		if !got.GetDisabled() || got.GetLastBeat() != nil {
			t.Fatalf("hidden App's Check: disabled %v, last beat %v; want disabled, never probed", got.GetDisabled(), got.GetLastBeat())
		}
		_, err := c.SetDisabled(t.Context(), &checkspb.SetDisabledRequest{Id: got.GetId(), Disabled: false})
		if status.Code(err) != codes.FailedPrecondition {
			t.Errorf("enabling a hidden App's Check by hand: %v, want FailedPrecondition", err)
		}

		setApps(t, c, printer)
		synctest.Wait()
		if got := appCheck(t, c, "proxy:printer.home.example"); got.GetDisabled() || got.GetState() != checkspb.State_STATE_UP {
			t.Errorf("shown again: disabled %v, %v; want probed and up", got.GetDisabled(), got.GetState())
		}
	})
}

func TestAVanishedProxyHostStopsItsCheckUntilItReturns(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		l := newLab()
		l.set("printer.home.example:8080", "http 200")
		_, c := startIn(t, l)
		setApps(t, c, printer)
		synctest.Wait()
		id := appCheck(t, c, "proxy:printer.home.example").GetId()
		after(Interval11())
		before := len(beats(t, c, id))

		setApps(t, c)
		if got := appCheck(t, c, "proxy:printer.home.example"); got != nil {
			t.Errorf("listed while its App is gone: %v", got)
		}
		after(3 * Interval11())
		if got := len(beats(t, c, id)); got != before {
			t.Errorf("%d beats while gone, want none", got-before)
		}

		setApps(t, c, printer)
		synctest.Wait()
		got := appCheck(t, c, "proxy:printer.home.example")
		if got.GetId() != id {
			t.Fatalf("returned as Check %d, want %d again", got.GetId(), id)
		}
		if n := len(beats(t, c, id)); n != before+1 {
			t.Errorf("%d beats after its return, want its history plus one", n)
		}
	})
}

func TestARenameStartsAFreshCheck(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		l := newLab()
		l.set("printer.home.example:8080", "http 200")
		l.set("print.home.example:8080", "http 200")
		_, c := startIn(t, l)
		setApps(t, c, printer)
		synctest.Wait()
		old := appCheck(t, c, "proxy:printer.home.example").GetId()

		setApps(t, c, app("proxy:print.home.example", "Print", "http://print.home.example:8080"))
		synctest.Wait()

		got := appCheck(t, c, "proxy:print.home.example")
		if got.GetId() == old {
			t.Fatalf("the renamed App kept Check %d", old)
		}
		if n := len(beats(t, c, got.GetId())); n != 1 {
			t.Errorf("%d beats, want only its own first one", n)
		}
		if appCheck(t, c, "proxy:printer.home.example") != nil {
			t.Error("the old name's Check is still listed")
		}
	})
}

func TestANewNameKeepsTheStateAndANewURLStartsOver(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		l := newLab()
		l.set("printer.home.example:8080", "http 200")
		l.set("printer.home.example:443", "hang")
		_, c := startIn(t, l)
		setApps(t, c, printer)
		synctest.Wait()

		setApps(t, c, app("proxy:printer.home.example", "Office printer", "http://printer.home.example:8080"))
		got := appCheck(t, c, "proxy:printer.home.example")
		if got.GetSpec().GetName() != "Office printer" || got.GetState() != checkspb.State_STATE_UP {
			t.Errorf("renamed: %q %v, want Office printer, still up", got.GetSpec().GetName(), got.GetState())
		}

		setApps(t, c, app("proxy:printer.home.example", "Office printer", "https://printer.home.example"))
		got = appCheck(t, c, "proxy:printer.home.example")
		if got.GetState() != checkspb.State_STATE_PENDING || got.GetSpec().GetTarget() != "https://printer.home.example" {
			t.Errorf("new URL: %v of %q, want pending on the new URL", got.GetState(), got.GetSpec().GetTarget())
		}
	})
}

func TestAnAppChecksWorkloadIsKeptAndChangesWithoutResettingIt(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		l := newLab()
		l.set("printer.home.example:8080", "http 200")
		h, c := startIn(t, l)
		lxc := &workloadpb.WorkloadRef{Kind: workloadpb.Kind_KIND_LXC, Host: "pve1", Id: "120"}
		onLxc := app("proxy:printer.home.example", "Printer", "http://printer.home.example:8080")
		onLxc.Workload = lxc
		setApps(t, c, onLxc)
		synctest.Wait()
		if d := cmp.Diff(lxc, appCheck(t, c, "proxy:printer.home.example").GetWorkload(), protocmp.Transform()); d != "" {
			t.Errorf("workload (-want +got):\n%s", d)
		}

		docker := &workloadpb.WorkloadRef{Kind: workloadpb.Kind_KIND_DOCKER, Host: "media", Id: "cups"}
		onDocker := app("proxy:printer.home.example", "Printer", "http://printer.home.example:8080")
		onDocker.Workload = docker
		setApps(t, c, onDocker)
		got := appCheck(t, c, "proxy:printer.home.example")
		if d := cmp.Diff(docker, got.GetWorkload(), protocmp.Transform()); d != "" {
			t.Errorf("moved workload (-want +got):\n%s", d)
		}
		if got.GetState() != checkspb.State_STATE_UP {
			t.Errorf("state after a Workload change: %v, want still up", got.GetState())
		}

		h.Stop()
		_, c = startIn(t, l, sdktest.WithDB(h.DBPath))
		if d := cmp.Diff(docker, appCheck(t, c, "proxy:printer.home.example").GetWorkload(), protocmp.Transform()); d != "" {
			t.Errorf("workload after a restart (-want +got):\n%s", d)
		}

		setApps(t, c, printer)
		if got := appCheck(t, c, "proxy:printer.home.example").GetWorkload(); got != nil {
			t.Errorf("workload after apps found none: %v, want unset", got)
		}
	})
}

func TestAppChecksFollowTheirAppNotTheOperator(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		_, c := startIn(t, newLab())
		setApps(t, c, printer)
		id := appCheck(t, c, "proxy:printer.home.example").GetId()

		_, err := c.UpdateCheck(t.Context(), &checkspb.UpdateCheckRequest{Id: id, Spec: tcp("printer.home.example:9100")})
		if status.Code(err) != codes.FailedPrecondition {
			t.Errorf("UpdateCheck: %v, want FailedPrecondition", err)
		}
		_, err = c.DeleteCheck(t.Context(), &checkspb.DeleteCheckRequest{Id: id})
		if status.Code(err) != codes.FailedPrecondition {
			t.Errorf("DeleteCheck: %v, want FailedPrecondition", err)
		}
		if _, err := c.SetMuted(t.Context(), &checkspb.SetMutedRequest{Id: id, Muted: true}); err != nil {
			t.Errorf("SetMuted: %v, want it allowed", err)
		}
		if _, err := c.SetDisabled(t.Context(), &checkspb.SetDisabledRequest{Id: id, Disabled: true}); err != nil {
			t.Errorf("SetDisabled: %v, want it allowed", err)
		}
	})
}

func TestSetAppChecksRefusesABadSet(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		_, c := startIn(t, newLab())
		for name, set := range map[string][]*checkspb.AppCheck{
			"no app id":   {app("", "Printer", "http://printer.home.example")},
			"twice":       {printer, printer},
			"not a URL":   {app("proxy:a.home.example", "A", "a.home.example")},
			"not http(s)": {app("proxy:a.home.example", "A", "ftp://a.home.example")},
		} {
			_, err := c.SetAppChecks(t.Context(), &checkspb.SetAppChecksRequest{Checks: set})
			if status.Code(err) != codes.InvalidArgument {
				t.Errorf("%s: %v, want InvalidArgument", name, err)
			}
		}
		if got := list(t, c); len(got) != 0 {
			t.Errorf("Checks after refused sets: %v", got)
		}
	})
}

func TestAppChecksSurviveARestart(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		l := newLab()
		l.set("printer.home.example:8080", "http 200")
		h, c := startIn(t, l)
		setApps(t, c, printer, hidden(jellyfin))
		synctest.Wait()
		id := appCheck(t, c, "proxy:printer.home.example").GetId()
		h.Stop()

		_, c = startIn(t, l, sdktest.WithDB(h.DBPath))
		got := appCheck(t, c, "proxy:printer.home.example")
		if got.GetId() != id || got.GetState() != checkspb.State_STATE_UP {
			t.Errorf("after a restart: Check %d %v, want %d still up", got.GetId(), got.GetState(), id)
		}
		if !appCheck(t, c, "proxy:jellyfin.home.example").GetDisabled() {
			t.Error("the hidden App's Check is enabled after a restart")
		}

		// apps sends its set again after its own Start: nothing changes.
		setApps(t, c, printer, hidden(jellyfin))
		if got := appCheck(t, c, "proxy:printer.home.example"); got.GetId() != id || got.GetState() != checkspb.State_STATE_UP {
			t.Errorf("after the same set: Check %d %v, want %d still up", got.GetId(), got.GetState(), id)
		}
	})
}
