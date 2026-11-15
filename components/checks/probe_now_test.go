package checks_test

import (
	"testing"
	"testing/synctest"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/itsKontra/ddash/components/checks"
	"github.com/itsKontra/ddash/components/checks/checkspb"
	workloadpb "github.com/itsKontra/ddash/proto/ddash/workload"
)

func probeNow(t *testing.T, c checkspb.ChecksServiceClient, scope *checkspb.MaintenanceScope) {
	t.Helper()
	if _, err := c.ProbeNow(t.Context(), &checkspb.ProbeNowRequest{Scope: scope}); err != nil {
		t.Fatalf("ProbeNow: %v", err)
	}
}

var (
	n8nLXC = &workloadpb.WorkloadRef{Kind: workloadpb.Kind_KIND_LXC, Host: "pve1", Id: "206"}
	n8n    = func() *checkspb.AppCheck {
		return on(app("proxy:n8n.home.example", "N8n", "http://n8n.home.example"), n8nLXC)
	}
	grafana = func() *checkspb.AppCheck {
		return on(app("proxy:grafana.home.example", "Grafana", "http://grafana.home.example"), &workloadpb.WorkloadRef{Kind: workloadpb.Kind_KIND_LXC, Host: "pve1", Id: "204"})
	}
)

// downApps starts checks with N8n on LXC 206 and Grafana on LXC 204, both
// answering 502 until down.
func downApps(t *testing.T, l *lab) checkspb.ChecksServiceClient {
	t.Helper()
	l.set("n8n.home.example:80", "http 502")
	l.set("grafana.home.example:80", "http 502")
	_, c := startIn(t, l)
	setApps(t, c, n8n(), grafana())
	synctest.Wait()
	after(2 * Interval11())
	for _, id := range []string{"proxy:n8n.home.example", "proxy:grafana.home.example"} {
		if got := appCheck(t, c, id).GetState(); got != checkspb.State_STATE_DOWN {
			t.Fatalf("%s: %v, want down", id, got)
		}
	}
	return c
}

func TestProbeNowProbesAWorkloadsAppChecksAtOnce(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		l := newLab()
		c := downApps(t, l)
		l.set("n8n.home.example:80", "http 200")
		l.set("grafana.home.example:80", "http 200")
		grafanaBeat := appCheck(t, c, "proxy:grafana.home.example").GetLastBeat().AsTime()

		// Proxmox started LXC 206 on another node: VMID 206 is still n8n.
		probeNow(t, c, workloadScope(workloadpb.Kind_KIND_LXC, "pve2", "206"))
		synctest.Wait()

		got := appCheck(t, c, "proxy:n8n.home.example")
		if got.GetState() != checkspb.State_STATE_UP || !got.GetLastBeat().AsTime().Equal(time.Now()) {
			t.Errorf("N8n: %v, last beat %v, want up, probed now (%v)", got.GetState(), got.GetLastBeat().AsTime(), time.Now())
		}
		if g := appCheck(t, c, "proxy:grafana.home.example"); g.GetState() != checkspb.State_STATE_DOWN || !g.GetLastBeat().AsTime().Equal(grafanaBeat) {
			t.Errorf("Grafana on LXC 204: %v, last beat %v, want down, not probed since %v", g.GetState(), g.GetLastBeat().AsTime(), grafanaBeat)
		}

		// Its loop goes on from the probe: the next beat a minute later.
		probed := time.Now()
		l.set("n8n.home.example:80", "http 502")
		after(checks.Interval - time.Second)
		if last := appCheck(t, c, "proxy:n8n.home.example").GetLastBeat().AsTime(); !last.Equal(probed) {
			t.Errorf("N8n beat again at %v, before its minute from %v", last, probed)
		}
		after(Interval11() - checks.Interval + time.Second)
		if got := appCheck(t, c, "proxy:n8n.home.example"); !got.GetLastBeat().AsTime().After(probed) || got.GetFailures() != 1 {
			t.Errorf("N8n a minute on: last beat %v, %d failures, want its next beat", got.GetLastBeat().AsTime(), got.GetFailures())
		}
	})
}

func TestACheckStillDownIsProbedAgainSoonUntilItIsUp(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		l := newLab()
		c := downApps(t, l)
		before := appCheck(t, c, "proxy:n8n.home.example").GetFailures()

		probeNow(t, c, workloadScope(workloadpb.Kind_KIND_LXC, "pve1", "206"))
		start := time.Now()
		synctest.Wait()
		after(checks.ProbeAgain[0])
		got := appCheck(t, c, "proxy:n8n.home.example")
		if got.GetFailures() != before+2 || !got.GetLastBeat().AsTime().Equal(start.Add(checks.ProbeAgain[0])) {
			t.Fatalf("5 s after: %d failures, last beat %v; want %d, probed at once and again at %v", got.GetFailures(), got.GetLastBeat().AsTime(), before+2, start.Add(checks.ProbeAgain[0]))
		}

		// n8n answers before the 15 s probe: up then, and not probed again soon.
		l.set("n8n.home.example:80", "http 200")
		after(checks.ProbeAgain[1] - checks.ProbeAgain[0])
		got = appCheck(t, c, "proxy:n8n.home.example")
		if got.GetState() != checkspb.State_STATE_UP || !got.GetLastBeat().AsTime().Equal(start.Add(checks.ProbeAgain[1])) {
			t.Fatalf("15 s after: %v at %v, want up at %v", got.GetState(), got.GetLastBeat().AsTime(), start.Add(checks.ProbeAgain[1]))
		}
		after(checks.ProbeAgain[2] - checks.ProbeAgain[1])
		if last := appCheck(t, c, "proxy:n8n.home.example").GetLastBeat().AsTime(); !last.Equal(start.Add(checks.ProbeAgain[1])) {
			t.Errorf("probed again at %v once up", last)
		}
	})
}

func TestACheckThatWasUpIsProbedOnceOnly(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		l := newLab()
		l.set("n8n.home.example:80", "http 200")
		_, c := startIn(t, l)
		setApps(t, c, n8n())
		synctest.Wait()

		// What was just restarted doesn't answer yet: one failure is no
		// reason to probe it faster toward down.
		l.set("n8n.home.example:80", "http 502")
		probeNow(t, c, workloadScope(workloadpb.Kind_KIND_LXC, "pve1", "206"))
		after(checks.ProbeAgain[len(checks.ProbeAgain)-1] + time.Second)
		got := appCheck(t, c, "proxy:n8n.home.example")
		if got.GetState() != checkspb.State_STATE_PENDING || got.GetFailures() != 1 {
			t.Errorf("%v with %d failures, want pending after its one probe", got.GetState(), got.GetFailures())
		}
	})
}

func TestProbeNowLeavesAChecksThatIsntProbedAlone(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		l := newLab()
		c := downApps(t, l)
		l.set("n8n.home.example:80", "http 200")
		setApps(t, c, hidden(n8n()), grafana())
		before := appCheck(t, c, "proxy:n8n.home.example").GetLastBeat().AsTime()

		probeNow(t, c, everything())
		synctest.Wait()
		if got := appCheck(t, c, "proxy:n8n.home.example"); got.GetState() != checkspb.State_STATE_DOWN || !got.GetLastBeat().AsTime().Equal(before) {
			t.Errorf("hidden N8n: %v, last beat %v, want not probed", got.GetState(), got.GetLastBeat().AsTime())
		}
	})
}

func TestProbeNowRefusesABadScope(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		_, c := startIn(t, newLab())
		_, err := c.ProbeNow(t.Context(), &checkspb.ProbeNowRequest{Scope: workloadScope(workloadpb.Kind_KIND_LXC, "pve1", "")})
		if status.Code(err) != codes.InvalidArgument {
			t.Errorf("an LXC without VMID: %v, want InvalidArgument", err)
		}
	})
}
