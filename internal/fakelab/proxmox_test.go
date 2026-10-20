package fakelab_test

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"google.golang.org/protobuf/encoding/protojson"

	"github.com/itsKontra/ddash/components/checks"
	"github.com/itsKontra/ddash/components/checks/checkspb"
	ntfyapi "github.com/itsKontra/ddash/components/ntfy-api"
	"github.com/itsKontra/ddash/components/proxmox"
	proxmoxapi "github.com/itsKontra/ddash/components/proxmox-api"
	"github.com/itsKontra/ddash/components/proxmox-api/proxmoxfake"
	"github.com/itsKontra/ddash/core/coretest"
	"github.com/itsKontra/ddash/core/runtime"
	"github.com/itsKontra/ddash/internal/fakelab"
	"github.com/itsKontra/ddash/sdk"
)

// bootProxmoxLab boots the core with the proxmox Plugin's Components
// (proxmox, proxmox-api, and checks with its ntfy-api) in-process against
// the lab, configured as `make dev` does.
func bootProxmoxLab(t *testing.T) (*coretest.Harness, *fakelab.Lab) {
	lab, err := fakelab.Start(repoRoot, "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(lab.Close)
	h := coretest.New(t, coretest.Options{
		Components: []sdk.Definition{proxmox.Definition(), proxmoxapi.Definition(), checks.Definition(), ntfyapi.Definition()},
		Plugins: []coretest.Plugin{
			{Name: "proxmox", Requires: []string{"proxmox", "proxmox-api"}},
			{Name: "checks", Requires: []string{"checks"}},
		},
	})
	for component, cfg := range lab.Config() {
		if component == "proxmox-api" || component == "ntfy-api" {
			h.SeedConfig(component, cfg)
		}
	}
	h.Install("proxmox")
	h.Install("checks")
	for _, c := range []string{"proxmox-api", "checks", "proxmox"} {
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

	res := h.Call("proxmox", "proxmox", "StopWorkload", `{"workload":{"kind":"KIND_LXC","host":"pve","id":"102"}}`)
	if res.Code != http.StatusOK {
		t.Fatalf("StopWorkload: status %d: %s", res.Code, res.Body)
	}

	pve := lab.Handler("proxmox-api").(*proxmoxfake.Proxmox)
	if got := pve.Status(102); got != "stopped" {
		t.Errorf("102 is %s, want stopped", got)
	}
	var shutdowns []string
	for _, r := range pve.Log() {
		if strings.HasPrefix(r, "POST ") {
			shutdowns = append(shutdowns, r)
		}
	}
	if diff := cmp.Diff([]string{"POST /api2/json/nodes/pve/lxc/102/status/shutdown forceStop=1 timeout=45"}, shutdowns); diff != "" {
		t.Errorf("power requests (-want +got):\n%s", diff)
	}

	// 102 stays in Maintenance until ddash starts it or its Checks are up.
	res = h.Call("checks", "checks", "ListMaintenance", `{}`)
	var maint checkspb.ListMaintenanceResponse
	if err := protojson.Unmarshal(res.Body.Bytes(), &maint); err != nil {
		t.Fatal(err)
	}
	if m := maint.GetMaintenance(); len(m) != 1 || m[0].GetScope().GetWorkload().GetId() != "102" || !m[0].GetUntilUp() || m[0].GetEnds() != nil {
		t.Errorf("Maintenance = %v, want LXC 102's, until up or ended", m)
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
	h, lab := bootProxmoxLab(t)

	// The baseline's nginx (101) has only VM.Audit and VM.GuestAgent.Audit.
	res := h.Call("proxmox", "proxmox", "RestartWorkload", `{"workload":{"kind":"KIND_LXC","host":"pve","id":"101"}}`)
	if res.Code != http.StatusForbidden || !strings.Contains(res.Body.String(), "protected in Proxmox") {
		t.Errorf("RestartWorkload 101: status %d: %s; want 403 protected in Proxmox", res.Code, res.Body)
	}

	pve := lab.Handler("proxmox-api").(*proxmoxfake.Proxmox)
	if got := pve.Status(101); got != "running" {
		t.Errorf("101 is %s, want running", got)
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
