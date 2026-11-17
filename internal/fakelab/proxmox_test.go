package fakelab_test

import (
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"google.golang.org/protobuf/encoding/protojson"

	"github.com/itsKontra/ddash/agent"
	"github.com/itsKontra/ddash/agent/agentfake"
	"github.com/itsKontra/ddash/components/checks"
	"github.com/itsKontra/ddash/components/checks/checkspb"
	hostagentapi "github.com/itsKontra/ddash/components/host-agent-api"
	ntfyapi "github.com/itsKontra/ddash/components/ntfy-api"
	"github.com/itsKontra/ddash/components/proxmox"
	proxmoxapi "github.com/itsKontra/ddash/components/proxmox-api"
	"github.com/itsKontra/ddash/components/proxmox-api/proxmoxfake"
	"github.com/itsKontra/ddash/components/proxmox/proxmoxpb"
	telegramapi "github.com/itsKontra/ddash/components/telegram-api"
	"github.com/itsKontra/ddash/components/telegram-api/telegramfake"
	"github.com/itsKontra/ddash/core/appstate"
	"github.com/itsKontra/ddash/core/coretest"
	"github.com/itsKontra/ddash/core/runtime"
	"github.com/itsKontra/ddash/internal/fakelab"
	jobpb "github.com/itsKontra/ddash/proto/ddash/job"
	"github.com/itsKontra/ddash/sdk"
)

// bootProxmoxLab boots the core with the proxmox Plugin's Components
// (proxmox, proxmox-api, and checks) and both channel Plugins in-process
// against a lab serving scenarios, configured as `make dev` does.
func bootProxmoxLab(t *testing.T, scenarios ...string) (*coretest.Harness, *fakelab.Lab) {
	lab, err := fakelab.Start(repoRoot, "127.0.0.1", scenarios...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(lab.Close)
	h := coretest.New(t, coretest.Options{
		Components: []sdk.Definition{proxmox.Definition(), proxmoxapi.Definition(), hostagentapi.Definition(), checks.Definition(), ntfyapi.Definition(), telegramapi.Definition()},
		Plugins: []coretest.Plugin{
			{Name: "proxmox", Requires: []string{"proxmox", "proxmox-api", "host-agent-api"}},
			{Name: "checks", Requires: []string{"checks"}},
			{Name: "ntfy", Requires: []string{"ntfy-api"}, Headless: true},
			{Name: "telegram", Requires: []string{"telegram-api"}, Headless: true},
		},
	})
	for component, cfg := range lab.Config() {
		if component == "proxmox-api" || component == "ntfy-api" || component == "telegram-api" {
			h.SeedConfig(component, cfg)
		}
	}
	for _, p := range []string{"proxmox", "checks", "ntfy", "telegram"} {
		h.Install(p)
	}
	for _, c := range []string{"proxmox-api", "checks", "proxmox", "ntfy-api", "telegram-api"} {
		h.WaitState(c, runtime.OK)
	}
	return h, lab
}

// hopsIn returns the Audit Log's Component hops, newest first, as
// "<target> <method> <outcome> <user>", and how many chains they span.
func hopsIn(h *coretest.Harness) ([]string, int) {
	var hops []string
	chains := map[string]bool{}
	for _, e := range componentHops(h) {
		hops = append(hops, fmt.Sprintf("%s %s %s %s", e.Target, e.Method, e.Outcome, e.Username))
		chains[e.Chain] = true
	}
	return hops, len(chains)
}

func TestStopShutsTheWorkloadDownInMaintenanceInOneAuditChain(t *testing.T) {
	h, lab := bootProxmoxLab(t)

	res := h.Call("proxmox", "proxmox", "StopWorkload", `{"workload":{"kind":"KIND_LXC","host":"pve","id":"212"}}`)
	if res.Code != http.StatusOK {
		t.Fatalf("StopWorkload: status %d: %s", res.Code, res.Body)
	}

	pve := lab.Handler("proxmox-api").(*proxmoxfake.Proxmox)
	if got := pve.Status(212); got != "stopped" {
		t.Errorf("212 is %s, want stopped", got)
	}
	var shutdowns []string
	for _, r := range pve.Log() {
		if strings.HasPrefix(r, "POST ") {
			shutdowns = append(shutdowns, r)
		}
	}
	if diff := cmp.Diff([]string{"POST /api2/json/nodes/pve/lxc/212/status/shutdown forceStop=1 timeout=45"}, shutdowns); diff != "" {
		t.Errorf("power requests (-want +got):\n%s", diff)
	}

	// 212 stays in Maintenance until ddash starts it or its Checks are up.
	res = h.Call("checks", "checks", "ListMaintenance", `{}`)
	var maint checkspb.ListMaintenanceResponse
	if err := protojson.Unmarshal(res.Body.Bytes(), &maint); err != nil {
		t.Fatal(err)
	}
	if m := maint.GetMaintenance(); len(m) != 1 || m[0].GetScope().GetWorkload().GetId() != "212" || !m[0].GetUntilUp() || m[0].GetEnds() != nil {
		t.Errorf("Maintenance = %v, want LXC 212's, until up or ended", m)
	}

	hops, chains := hopsIn(h)
	want := []string{
		"checks StartMaintenance ok test",
		"proxmox-api ShutdownWorkload ok test",
		"checks StartMaintenance ok test",
		"proxmox StopWorkload ok test",
	}
	if diff := cmp.Diff(want, hops); diff != "" {
		t.Errorf("Audit Log (-want +got):\n%s", diff)
	}
	if chains != 1 {
		t.Errorf("%d chains, want 1", chains)
	}
}

func TestAProtectedWorkloadIsRefusedAndKeepsRunning(t *testing.T) {
	// The real token holds VM.PowerMgmt everywhere; in the protected
	// scenario nginx (210) has only VM.Audit and VM.GuestAgent.Audit.
	h, lab := bootProxmoxLab(t, "protected")

	res := h.Call("proxmox", "proxmox", "RestartWorkload", `{"workload":{"kind":"KIND_LXC","host":"pve","id":"210"}}`)
	if res.Code != http.StatusForbidden || !strings.Contains(res.Body.String(), "protected in Proxmox") {
		t.Errorf("RestartWorkload 210: status %d: %s; want 403 protected in Proxmox", res.Code, res.Body)
	}

	pve := lab.Handler("proxmox-api").(*proxmoxfake.Proxmox)
	if got := pve.Status(210); got != "running" {
		t.Errorf("210 is %s, want running", got)
	}
	for _, r := range pve.Log() {
		if strings.HasPrefix(r, "POST ") {
			t.Errorf("Proxmox got %s, want no power request", r)
		}
	}
	hops, _ := hopsIn(h)
	if diff := cmp.Diff([]string{"proxmox RestartWorkload PermissionDenied test"}, hops); diff != "" {
		t.Errorf("Audit Log (-want +got):\n%s", diff)
	}
}

// An update through a paired Host Agent, end to end: it outlives its
// Action, keeps exactly one ddash Snapshot without touching the manual
// one, and its whole run, the job's collect included, is one Audit Log
// chain.
func TestAnUpdateRunsInTheBackgroundInItsActionsChain(t *testing.T) {
	h, lab := bootProxmoxLab(t)
	h.WaitState("host-agent-api", runtime.OK)
	// The lab's own Agents take 1.5 s a step; this one is quick.
	fake, err := agentfake.NewHost("", agentfake.HostSeed{
		OS: "debian", Pending: []agent.Package{{Name: "libc6", Current: "2.36-9+deb12u7", New: "2.36-9+deb12u9"}},
	}, agentfake.Options{StateDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	u, stop, err := fake.Serve("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(stop)
	for _, call := range []struct{ method, body string }{
		{"AddAgent", fmt.Sprintf(`{"vmid":201,"name":"docker-host","url":%q}`, u)},
		{"PairAgent", `{"vmid":201}`},
	} {
		if res := h.Call("proxmox", "host-agent-api", call.method, call.body); res.Code != http.StatusOK {
			t.Fatalf("%s: %d %s", call.method, res.Code, res.Body)
		}
	}
	waitFor(t, func() bool {
		res := h.Call("proxmox", "host-agent-api", "ListPackages", `{}`)
		return strings.Contains(res.Body.String(), "libc6")
	})

	res := h.Call("proxmox", "proxmox", "RunUpdate", `{"workload":{"kind":"KIND_LXC","host":"pve","id":"201"}}`)
	if res.Code != http.StatusOK {
		t.Fatalf("RunUpdate: %d %s", res.Code, res.Body)
	}
	var updates proxmoxpb.ListUpdatesResponse
	waitFor(t, func() bool {
		res := h.Call("proxmox", "proxmox", "ListUpdates", `{}`)
		if err := protojson.Unmarshal(res.Body.Bytes(), &updates); err != nil {
			t.Fatal(err)
		}
		return len(updates.GetRuns()) == 1 && updates.GetRuns()[0].GetEnded() != nil
	})
	if r := updates.GetRuns()[0]; r.GetState() != jobpb.State_STATE_DONE || !r.GetRebootRequired() || r.GetUpdated() != 1 {
		t.Fatalf("run = %v", r)
	}

	pve := lab.Handler("proxmox-api").(*proxmoxfake.Proxmox)
	var names []string
	for _, s := range pve.Snapshots(201) {
		names = append(names, s.Name)
	}
	if len(names) != 2 || names[0] != "before-docker-27" || !strings.HasPrefix(names[1], "ddash-") || names[1] == "ddash-20261020-031500" {
		t.Errorf("201's snapshots = %v, want the manual one and the new ddash Snapshot", names)
	}

	// The outcome's Notification follows the run's end, through the core
	// to both channels.
	delivered := func(e appstate.Entry) bool { return e.Method == "Deliver" && e.Outcome != "" }
	waitFor(t, func() bool {
		n := 0
		for _, e := range h.AuditLog() {
			if delivered(e) {
				n++
			}
		}
		return n == 2
	})
	var hops []string
	chains := map[string]bool{}
	for _, e := range slices.Backward(h.AuditLog()) {
		if e.Method == "RunUpdate" || chains[e.Chain] {
			chains[e.Chain] = true
			hops = append(hops, fmt.Sprintf("%s %s %s", e.Target, e.Method, e.Outcome))
		}
	}
	// The channels are delivered to at once.
	slices.Sort(hops[len(hops)-2:])
	want := []string{
		"proxmox RunUpdate ok",
		"checks StartMaintenance ok",
		"proxmox-api CreateSnapshot ok",
		"proxmox-api DeleteSnapshot ok",
		"host-agent-api UpgradePackages ok",
		"host-agent-api CollectJob ok",
		"checks StartMaintenance ok",
		"core Send ok",
		"ntfy-api Deliver ok",
		"telegram-api Deliver ok",
	}
	if diff := cmp.Diff(want, hops); diff != "" {
		t.Errorf("the update's chain, oldest first (-want +got):\n%s", diff)
	}
	if len(chains) != 1 {
		t.Errorf("%d chains, want 1", len(chains))
	}
	msgs := lab.Handler("telegram-api").(*telegramfake.Bot).Messages()
	if len(msgs) != 1 || !strings.Contains(msgs[0].Plain, "Updated ") {
		t.Errorf("Telegram got %+v, want the update's outcome", msgs)
	}
}

func waitFor(t *testing.T, ok func() bool) {
	t.Helper()
	for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); time.Sleep(200 * time.Millisecond) {
		if ok() {
			return
		}
	}
	t.Fatal("timed out")
}
