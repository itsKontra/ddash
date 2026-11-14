package proxmox_test

import (
	"context"
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/google/go-cmp/cmp"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/itsKontra/ddash/components/checks/checkspb"
	"github.com/itsKontra/ddash/components/host-agent-api/hostagentapipb"
	"github.com/itsKontra/ddash/components/notify/notifypb"
	"github.com/itsKontra/ddash/components/proxmox"
	"github.com/itsKontra/ddash/components/proxmox-api/proxmoxapipb"
	"github.com/itsKontra/ddash/components/proxmox/proxmoxpb"
	jobpb "github.com/itsKontra/ddash/proto/ddash/job"
	workloadpb "github.com/itsKontra/ddash/proto/ddash/workload"
	"github.com/itsKontra/ddash/sdk/sdktest"
)

// --- proxmox-api's snapshots, on the lab of proxmox_test.go ---

func (p proxmoxAPI) ListSnapshots(_ context.Context, req *proxmoxapipb.ListSnapshotsRequest, _ ...grpc.CallOption) (*proxmoxapipb.ListSnapshotsResponse, error) {
	p.l.mu.Lock()
	defer p.l.mu.Unlock()
	vmid := req.GetWorkload().GetVmid()
	out := &proxmoxapipb.ListSnapshotsResponse{Supported: !p.l.noSnap[vmid]}
	for _, s := range p.l.snaps[vmid] {
		out.Snapshots = append(out.Snapshots, proto.CloneOf(s))
	}
	return out, nil
}

func (p proxmoxAPI) CreateSnapshot(_ context.Context, req *proxmoxapipb.CreateSnapshotRequest, _ ...grpc.CallOption) (*proxmoxapipb.CreateSnapshotResponse, error) {
	p.l.mu.Lock()
	defer p.l.mu.Unlock()
	vmid := req.GetWorkload().GetVmid()
	p.l.log = append(p.l.log, fmt.Sprintf("proxmox-api CreateSnapshot %d %q", vmid, req.GetDescription()))
	if p.l.noSnap[vmid] {
		return nil, status.Error(codes.FailedPrecondition, "snapshots not supported: Proxmox's vzsnapshot task failed: snapshot feature is not available")
	}
	if why := p.l.failSnapshot; why != "" {
		p.l.failSnapshot = ""
		return nil, status.Errorf(codes.FailedPrecondition, "Proxmox's vzsnapshot task failed: %s", why)
	}
	p.l.snaps[vmid] = append(p.l.snaps[vmid], &proxmoxapipb.Snapshot{Name: req.GetName(), Description: req.GetDescription(), Created: timestamppb.Now()})
	return &proxmoxapipb.CreateSnapshotResponse{Upid: "UPID:pve2:snap"}, nil
}

func (p proxmoxAPI) DeleteSnapshot(_ context.Context, req *proxmoxapipb.DeleteSnapshotRequest, _ ...grpc.CallOption) (*proxmoxapipb.DeleteSnapshotResponse, error) {
	p.l.mu.Lock()
	defer p.l.mu.Unlock()
	vmid := req.GetWorkload().GetVmid()
	p.l.log = append(p.l.log, fmt.Sprintf("proxmox-api DeleteSnapshot %d %s", vmid, req.GetName()))
	p.l.snaps[vmid] = slices.DeleteFunc(p.l.snaps[vmid], func(s *proxmoxapipb.Snapshot) bool { return s.GetName() == req.GetName() })
	return &proxmoxapipb.DeleteSnapshotResponse{}, nil
}

func (p proxmoxAPI) Backup(_ context.Context, req *proxmoxapipb.BackupRequest, _ ...grpc.CallOption) (*proxmoxapipb.BackupResponse, error) {
	p.l.record("proxmox-api Backup %d to %s %v", req.GetWorkload().GetVmid(), req.GetStorage(), req.GetMode())
	return &proxmoxapipb.BackupResponse{Upid: fmt.Sprintf("UPID:pve2:vzdump:%d", req.GetWorkload().GetVmid())}, nil
}

func (p proxmoxAPI) ListTasks(_ context.Context, req *proxmoxapipb.ListTasksRequest, _ ...grpc.CallOption) (*proxmoxapipb.ListTasksResponse, error) {
	return &proxmoxapipb.ListTasksResponse{Tasks: []*proxmoxapipb.TaskStatus{{
		Upid: fmt.Sprintf("UPID:pve2:vzdump:%d", req.GetVmid()), Type: "vzdump", Stopped: true, ExitStatus: "OK", Started: timestamppb.Now(), Ended: timestamppb.Now(),
	}}}, nil
}

func snapNames(l *lab, vmid uint32) []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []string
	for _, s := range l.snaps[vmid] {
		out = append(out, s.GetName())
	}
	return out
}

func (l *lab) seedSnapshots(vmid uint32, names ...string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for i, n := range names {
		l.snaps[vmid] = append(l.snaps[vmid], &proxmoxapipb.Snapshot{Name: n, Created: timestamppb.New(time.Date(2026, 10, 1+i, 3, 0, 0, 0, time.UTC))})
	}
}

// --- host-agent-api ---

// agents fakes host-agent-api: a paired Agent on every Workload of the
// lab, each with pending packages; an upgrade job ends on its first read.
type agents struct {
	hostagentapipb.HostAgentApiServiceClient
	l *lab

	mu       sync.Mutex
	pending  map[uint32]int
	jobs     map[uint32]*jobpb.Job
	failNext map[uint32]string
	keys     map[uint32][]string
}

func newAgents(l *lab) *agents {
	return &agents{l: l, pending: map[uint32]int{101: 3, 102: 12, 103: 2, 110: 4}, jobs: map[uint32]*jobpb.Job{}, failNext: map[uint32]string{}, keys: map[uint32][]string{}}
}

func (a *agents) ListAgents(context.Context, *hostagentapipb.ListAgentsRequest, ...grpc.CallOption) (*hostagentapipb.ListAgentsResponse, error) {
	out := &hostagentapipb.ListAgentsResponse{}
	for _, vmid := range []uint32{101, 102, 103, 110} {
		out.Agents = append(out.Agents, &hostagentapipb.Agent{Vmid: vmid, State: hostagentapipb.AgentState_AGENT_STATE_PAIRED})
	}
	return out, nil
}

func (a *agents) ListPackages(context.Context, *hostagentapipb.ListPackagesRequest, ...grpc.CallOption) (*hostagentapipb.ListPackagesResponse, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := &hostagentapipb.ListPackagesResponse{}
	for _, vmid := range []uint32{101, 102, 103, 110} {
		h := &hostagentapipb.HostPackages{Vmid: vmid, Read: timestamppb.Now()}
		for i := range a.pending[vmid] {
			h.Packages = append(h.Packages, &hostagentapipb.Package{Name: fmt.Sprintf("pkg%d", i), Current: "1", New: "2"})
		}
		if vmid == 102 && a.pending[vmid] > 0 {
			h.Packages[0].Name = "libc6"
		}
		out.Hosts = append(out.Hosts, h)
	}
	return out, nil
}

func (a *agents) UpgradePackages(_ context.Context, req *hostagentapipb.UpgradePackagesRequest, _ ...grpc.CallOption) (*hostagentapipb.UpgradePackagesResponse, error) {
	a.l.record("host-agent-api UpgradePackages %d", req.GetVmid())
	a.mu.Lock()
	defer a.mu.Unlock()
	j := &jobpb.Job{
		Id: "up-" + strconv.Itoa(int(req.GetVmid())), Op: "packages.upgrade", State: jobpb.State_STATE_RUNNING,
		Steps: []*jobpb.Step{{Title: "Refresh the package index", State: jobpb.State_STATE_PENDING}, {Title: "Install updates", State: jobpb.State_STATE_PENDING}, {Title: "Check whether a reboot is required", State: jobpb.State_STATE_PENDING}},
	}
	a.jobs[req.GetVmid()] = j
	return &hostagentapipb.UpgradePackagesResponse{Job: proto.CloneOf(j)}, nil
}

func (a *agents) ListJobs(context.Context, *hostagentapipb.ListJobsRequest, ...grpc.CallOption) (*hostagentapipb.ListJobsResponse, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := &hostagentapipb.ListJobsResponse{}
	for _, vmid := range []uint32{101, 102, 103, 110} {
		h := &hostagentapipb.HostJobs{Vmid: vmid}
		if j := a.jobs[vmid]; j != nil {
			if j.GetEnd() == nil {
				j.End = timestamppb.Now()
				if why := a.failNext[vmid]; why != "" {
					delete(a.failNext, vmid)
					j.State, j.Error = jobpb.State_STATE_FAILED, why
					j.Steps[0].State, j.Steps[1].State, j.Steps[1].Detail = jobpb.State_STATE_DONE, jobpb.State_STATE_FAILED, why
				} else {
					j.State = jobpb.State_STATE_DONE
					reboot := strconv.FormatBool(vmid == 102)
					j.Result = map[string]string{"updated": strconv.Itoa(a.pending[vmid]), "reboot_required": reboot}
					for _, s := range j.Steps {
						s.State = jobpb.State_STATE_DONE
					}
					a.pending[vmid] = 0
				}
			}
			h.Jobs = append(h.Jobs, proto.CloneOf(j))
		}
		out.Hosts = append(out.Hosts, h)
	}
	return out, nil
}

func (a *agents) CollectJob(_ context.Context, req *hostagentapipb.CollectJobRequest, _ ...grpc.CallOption) (*hostagentapipb.CollectJobResponse, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	j := a.jobs[req.GetVmid()]
	if j == nil || j.GetId() != req.GetId() {
		return nil, status.Error(codes.NotFound, "no such job")
	}
	delete(a.jobs, req.GetVmid())
	outcome := "ok"
	if j.GetState() == jobpb.State_STATE_FAILED {
		outcome = "Aborted"
	}
	a.l.record("host-agent-api CollectJob %d %s", req.GetVmid(), outcome)
	if outcome != "ok" {
		return nil, status.Errorf(codes.Aborted, "Update packages failed: %s", j.GetError())
	}
	return &hostagentapipb.CollectJobResponse{Job: proto.CloneOf(j)}, nil
}

func (a *agents) InstallKey(_ context.Context, req *hostagentapipb.InstallKeyRequest, _ ...grpc.CallOption) (*hostagentapipb.InstallKeyResponse, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if slices.Contains(a.keys[req.GetVmid()], req.GetName()) {
		return nil, status.Error(codes.AlreadyExists, "ops already has this key")
	}
	a.keys[req.GetVmid()] = append(a.keys[req.GetVmid()], req.GetName())
	a.l.record("host-agent-api InstallKey %d %s %s %s", req.GetVmid(), req.GetUser(), req.GetName(), strings.Fields(req.GetPublicKey())[0])
	return &hostagentapipb.InstallKeyResponse{}, nil
}

type notifier struct {
	notifypb.NotifyServiceClient
	l *lab
}

func (n notifier) Send(_ context.Context, req *notifypb.SendRequest, _ ...grpc.CallOption) (*notifypb.SendResponse, error) {
	m := req.GetNotification()
	n.l.record("notify %s: %s (%s) %s", m.GetTitle(), m.GetMessage(), m.GetCategory(), m.GetClick())
	return &notifypb.SendResponse{}, nil
}

func startAll(t *testing.T, l *lab, a *agents) (proxmoxpb.ProxmoxServiceClient, *sdktest.Harness) {
	t.Helper()
	h := sdktest.New(t, proxmox.Definition(),
		sdktest.WithClient[proxmoxapipb.ProxmoxApiServiceClient](proxmoxAPI{l: l}),
		sdktest.WithClient[checkspb.ChecksServiceClient](checks{l: l}),
		sdktest.WithClient[hostagentapipb.HostAgentApiServiceClient](a),
		sdktest.WithClient[notifypb.NotifyServiceClient](notifier{l: l}))
	h.MustStart(&proxmoxpb.ProxmoxConfig{})
	return proxmoxpb.NewProxmoxServiceClient(h.Conn), h
}

// settle lets a background update run to its end on the fake clock.
func settle() {
	time.Sleep(time.Minute)
	synctest.Wait()
}

func runOf(t *testing.T, c proxmoxpb.ProxmoxServiceClient, vmid string) *proxmoxpb.UpdateRun {
	t.Helper()
	res, err := c.ListUpdates(t.Context(), &proxmoxpb.ListUpdatesRequest{})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range res.GetRuns() {
		if r.GetWorkload().GetId() == vmid {
			return r
		}
	}
	t.Fatalf("no update of %s", vmid)
	return nil
}

func stepStates(r *proxmoxpb.UpdateRun) []string {
	var out []string
	for _, s := range r.GetSteps() {
		out = append(out, fmt.Sprintf("%s %v", s.GetTitle(), s.GetState()))
	}
	return out
}

// --- Retention ---

func TestAnUpdateSnapshotsFirstAndDeletesOnlyTheOlderDdashSnapshot(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		l := newLab()
		l.seedSnapshots(102, "before-upgrade", "ddash-20261001-030000")
		c, _ := startAll(t, l, newAgents(l))

		res, err := c.RunUpdate(actionCtx(t), &proxmoxpb.RunUpdateRequest{Workload: lxc(102)})
		if err != nil {
			t.Fatal(err)
		}
		if res.GetRun().GetState() != jobpb.State_STATE_RUNNING {
			t.Fatalf("RunUpdate returned %v, want it running in the background", res.GetRun())
		}
		settle()

		names := snapNames(l, 102)
		if len(names) != 2 || names[0] != "before-upgrade" || !strings.HasPrefix(names[1], "ddash-") || names[1] == "ddash-20261001-030000" {
			t.Fatalf("snapshots after = %v, want the manual one and one new ddash Snapshot", names)
		}
		want := []string{
			"checks StartMaintenance KIND_LXC/pve2/102",
			`proxmox-api CreateSnapshot 102 "Created by ddash before an update"`,
			"proxmox-api DeleteSnapshot 102 ddash-20261001-030000",
			"host-agent-api UpgradePackages 102",
			"host-agent-api CollectJob 102 ok",
			"checks StartMaintenance KIND_LXC/pve2/102 for 5m0s until up",
			"notify Updated LXC 102 media-stack: 12 packages updated; reboot required: restart it when it suits (CATEGORY_ACTION_OUTCOMES) " + sdktest.PublicURL + "/proxmox/workloads/102",
		}
		if diff := cmp.Diff(want, l.calls()); diff != "" {
			t.Errorf("calls (-want +got):\n%s", diff)
		}
		r := runOf(t, c, "102")
		if r.GetState() != jobpb.State_STATE_DONE || !r.GetRebootRequired() || r.GetUpdated() != 12 || r.GetSnapshot() != names[1] {
			t.Errorf("run = %v", r)
		}
		wantSteps := []string{
			"Take a ddash Snapshot STATE_DONE", "Delete the older ddash Snapshots STATE_DONE",
			"Refresh the package index STATE_DONE", "Install updates STATE_DONE", "Check whether a reboot is required STATE_DONE",
		}
		if diff := cmp.Diff(wantSteps, stepStates(r)); diff != "" {
			t.Errorf("steps (-want +got):\n%s", diff)
		}
	})
}

func TestAFailedSnapshotDeletesNothingAndUpdatesNothing(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		l := newLab()
		l.seedSnapshots(102, "before-upgrade", "ddash-20261001-030000")
		l.failSnapshot = "zfs error: out of space"
		c, _ := startAll(t, l, newAgents(l))

		if _, err := c.RunUpdate(actionCtx(t), &proxmoxpb.RunUpdateRequest{Workload: lxc(102)}); err != nil {
			t.Fatal(err)
		}
		settle()

		if diff := cmp.Diff([]string{"before-upgrade", "ddash-20261001-030000"}, snapNames(l, 102)); diff != "" {
			t.Errorf("snapshots (-want +got):\n%s", diff)
		}
		for _, call := range l.calls() {
			if strings.Contains(call, "DeleteSnapshot") || strings.Contains(call, "UpgradePackages") {
				t.Errorf("after a failed snapshot: %s", call)
			}
		}
		r := runOf(t, c, "102")
		if r.GetState() != jobpb.State_STATE_FAILED || !strings.Contains(r.GetError(), "out of space") {
			t.Errorf("run = %v", r)
		}
		if got := l.calls(); !strings.HasPrefix(got[len(got)-1], "notify Update of LXC 102 media-stack failed") {
			t.Errorf("last call = %s, want the failure's Notification", got[len(got)-1])
		}
	})
}

func TestTakeSnapshotNeverTouchesManualSnapshots(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		l := newLab()
		l.seedSnapshots(110, "pre-hassio-2026", "clean-install")
		c, _ := startAll(t, l, newAgents(l))
		ref := lxc(110)
		ref.Kind = workloadpb.Kind_KIND_VM

		res, err := c.TakeSnapshot(actionCtx(t), &proxmoxpb.TakeSnapshotRequest{Workload: ref})
		if err != nil {
			t.Fatal(err)
		}
		if len(res.GetDeleted()) != 0 || !strings.HasPrefix(res.GetSnapshot().GetName(), "ddash-") {
			t.Fatalf("TakeSnapshot = %v", res)
		}
		got, err := c.GetSnapshots(actionCtx(t), &proxmoxpb.GetSnapshotsRequest{Workload: ref})
		if err != nil {
			t.Fatal(err)
		}
		if got.GetDdashSnapshot().GetName() != res.GetSnapshot().GetName() || len(got.GetOthers()) != 2 || !got.GetSupported() {
			t.Errorf("GetSnapshots = %v", got)
		}
		if got.GetDdashSnapshot().GetDescription() != "Created by ddash before a change by hand" {
			t.Errorf("description = %q", got.GetDdashSnapshot().GetDescription())
		}
	})
}

// --- Plans and Protected ---

func TestPlanUpdateListsThePackagesAndTheSnapshotItReplaces(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		l := newLab()
		l.seedSnapshots(102, "ddash-20261001-030000")
		c, _ := startAll(t, l, newAgents(l))

		res, err := c.PlanUpdate(actionCtx(t), &proxmoxpb.PlanUpdateRequest{Workload: lxc(102)})
		if err != nil {
			t.Fatal(err)
		}
		p := res.GetPlan()
		if len(p.GetPackages()) != 12 || p.GetPackages()[0].GetName() != "libc6" || !p.GetSnapshot() || !p.GetSnapshotSupported() ||
			p.GetReplacesSnapshot() != "ddash-20261001-030000" || p.GetProtected() || p.GetBlocked() != "" || p.GetLabel() != "LXC 102 media-stack" {
			t.Fatalf("plan = %v", p)
		}

		// Turned off for the Workload: the Plan says so; one run can turn it on again.
		if _, err := c.SetUpdateSettings(actionCtx(t), &proxmoxpb.SetUpdateSettingsRequest{Settings: &proxmoxpb.UpdateSettings{Vmid: 102}}); err != nil {
			t.Fatal(err)
		}
		res, _ = c.PlanUpdate(actionCtx(t), &proxmoxpb.PlanUpdateRequest{Workload: lxc(102)})
		if res.GetPlan().GetSnapshot() {
			t.Error("the Plan still takes a snapshot after the setting turned it off")
		}

		// Stopped: blocked.
		res, _ = c.PlanUpdate(actionCtx(t), &proxmoxpb.PlanUpdateRequest{Workload: lxc(103)})
		if !strings.Contains(res.GetPlan().GetBlocked(), "isn't running") {
			t.Errorf("103's Plan = %v, want it blocked as stopped", res.GetPlan())
		}
	})
}

func TestAProtectedWorkloadsUpdateNeedsTheOverride(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		l := newLab()
		c, _ := startAll(t, l, newAgents(l))

		_, err := c.RunUpdate(actionCtx(t), &proxmoxpb.RunUpdateRequest{Workload: lxc(101)})
		if status.Code(err) != codes.PermissionDenied {
			t.Fatalf("updating Protected 101 without the override: %v", err)
		}
		if len(l.calls()) != 0 {
			t.Fatalf("calls after the refusal = %v, want none", l.calls())
		}
		off := false
		if _, err := c.RunUpdate(actionCtx(t), &proxmoxpb.RunUpdateRequest{Workload: lxc(101), OverrideProtected: true, Snapshot: &off}); err != nil {
			t.Fatal(err)
		}
		settle()
		for _, call := range l.calls() {
			if strings.Contains(call, "Snapshot") {
				t.Errorf("a run with the snapshot off: %s", call)
			}
		}
		if r := runOf(t, c, "101"); r.GetState() != jobpb.State_STATE_DONE || r.GetSteps()[0].GetState() != jobpb.State_STATE_SKIPPED {
			t.Errorf("run = %v", r)
		}
	})
}

func TestStorageWithoutSnapshotsUpdatesOnlyWithTheSnapshotOff(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		l := newLab()
		l.noSnap[102] = true
		c, _ := startAll(t, l, newAgents(l))

		_, err := c.RunUpdate(actionCtx(t), &proxmoxpb.RunUpdateRequest{Workload: lxc(102)})
		if status.Code(err) != codes.FailedPrecondition || !strings.Contains(err.Error(), "snapshots not supported") {
			t.Fatalf("with the snapshot on: %v", err)
		}
		off := false
		if _, err := c.RunUpdate(actionCtx(t), &proxmoxpb.RunUpdateRequest{Workload: lxc(102), Snapshot: &off}); err != nil {
			t.Fatal(err)
		}
		settle()
		if r := runOf(t, c, "102"); r.GetState() != jobpb.State_STATE_DONE {
			t.Errorf("run = %v", r)
		}
	})
}

// --- Update all running ---

func TestUpdateAllSkipsWithReasonsAndStopsAtTheFirstFailure(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		l := newLab()
		a := newAgents(l)
		c, _ := startAll(t, l, a)

		plan, err := c.PlanUpdateAll(actionCtx(t), &proxmoxpb.PlanUpdateAllRequest{})
		if err != nil {
			t.Fatal(err)
		}
		var planned, skipped []string
		for _, w := range plan.GetWorkloads() {
			planned = append(planned, w.GetLabel())
		}
		for _, s := range plan.GetSkipped() {
			skipped = append(skipped, fmt.Sprintf("%s %v", s.GetLabel(), s.GetReason()))
		}
		if diff := cmp.Diff([]string{"LXC 102 media-stack", "VM 110 home-assistant"}, planned); diff != "" {
			t.Errorf("planned (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff([]string{"LXC 101 nginx SKIP_REASON_PROTECTED", "LXC 103 ddash-smoke SKIP_REASON_STOPPED"}, skipped); diff != "" {
			t.Errorf("skipped (-want +got):\n%s", diff)
		}

		// A Plan that no longer holds is refused.
		if _, err := c.RunUpdateAll(actionCtx(t), &proxmoxpb.RunUpdateAllRequest{}); status.Code(err) != codes.FailedPrecondition {
			t.Errorf("an empty Plan: %v", err)
		}
		if _, err := c.RunUpdateAll(actionCtx(t), &proxmoxpb.RunUpdateAllRequest{Workloads: refs(plan)[1:]}); status.Code(err) != codes.Aborted {
			t.Errorf("a Plan that changed: %v", err)
		}

		a.failNext[102] = "E: Sub-process /usr/bin/dpkg returned an error code (1)"
		res, err := c.RunUpdateAll(actionCtx(t), &proxmoxpb.RunUpdateAllRequest{Workloads: refs(plan)})
		if err != nil {
			t.Fatal(err)
		}
		if res.GetBulk().GetState() != jobpb.State_STATE_RUNNING {
			t.Fatalf("bulk = %v", res.GetBulk())
		}
		settle()

		updates, _ := c.ListUpdates(actionCtx(t), &proxmoxpb.ListUpdatesRequest{})
		b := updates.GetBulk()
		var items []string
		for _, it := range b.GetItems() {
			items = append(items, fmt.Sprintf("%s %v", it.GetLabel(), it.GetState()))
		}
		if diff := cmp.Diff([]string{"LXC 102 media-stack STATE_FAILED", "VM 110 home-assistant STATE_SKIPPED"}, items); diff != "" {
			t.Errorf("items (-want +got):\n%s", diff)
		}
		if b.GetState() != jobpb.State_STATE_FAILED || !strings.Contains(b.GetError(), "stopped at LXC 102 media-stack") || !strings.Contains(b.GetError(), "dpkg") {
			t.Errorf("bulk = %v", b)
		}
		for _, call := range l.calls() {
			if strings.Contains(call, " 110") {
				t.Errorf("110 was touched after 102 failed: %s", call)
			}
		}
		var notes []string
		for _, call := range l.calls() {
			if strings.HasPrefix(call, "notify ") {
				notes = append(notes, call)
			}
		}
		if len(notes) != 1 || !strings.HasPrefix(notes[0], "notify Update all running stopped: 0 of 2 Workloads updated") {
			t.Errorf("Notifications = %v, want one for the whole run", notes)
		}
	})
}

func refs(p *proxmoxpb.PlanUpdateAllResponse) []*workloadpb.WorkloadRef {
	var out []*workloadpb.WorkloadRef
	for _, w := range p.GetWorkloads() {
		out = append(out, w.GetWorkload())
	}
	return out
}

// --- SSH Keys ---

func TestAGeneratedKeysPrivateHalfIsReturnedOnceAndNeverStored(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		l := newLab()
		c, h := startAll(t, l, newAgents(l))

		gen, err := c.GenerateSSHKey(actionCtx(t), &proxmoxpb.GenerateSSHKeyRequest{Name: "laptop"})
		if err != nil {
			t.Fatal(err)
		}
		priv := gen.GetPrivateKey()
		if !strings.HasPrefix(priv, "-----BEGIN OPENSSH PRIVATE KEY-----") || gen.GetKey().GetType() != "ssh-ed25519" || !strings.HasPrefix(gen.GetKey().GetFingerprint(), "SHA256:") {
			t.Fatalf("GenerateSSHKey = %v", gen)
		}
		list, err := c.ListSSHKeys(actionCtx(t), &proxmoxpb.ListSSHKeysRequest{})
		if err != nil {
			t.Fatal(err)
		}
		if len(list.GetKeys()) != 1 || !list.GetKeys()[0].GetGenerated() || list.GetKeys()[0].GetPublicKey() != gen.GetKey().GetPublicKey() {
			t.Fatalf("ListSSHKeys = %v", list)
		}
		// The private key's body is nowhere in the DB.
		body := strings.Split(priv, "\n")[2]
		for _, f := range []string{h.DBPath, h.DBPath + "-wal"} {
			b, _ := os.ReadFile(f)
			if strings.Contains(string(b), body) {
				t.Errorf("%s holds the private key", f)
			}
		}

		// Pasting the same key again, or a private key, is refused.
		if _, err := c.AddSSHKey(actionCtx(t), &proxmoxpb.AddSSHKeyRequest{Name: "again", PublicKey: gen.GetKey().GetPublicKey()}); status.Code(err) != codes.AlreadyExists {
			t.Errorf("the same key again: %v", err)
		}
		if _, err := c.AddSSHKey(actionCtx(t), &proxmoxpb.AddSSHKeyRequest{Name: "oops", PublicKey: priv}); status.Code(err) != codes.InvalidArgument {
			t.Errorf("a private key: %v", err)
		}
		pasted, err := c.AddSSHKey(actionCtx(t), &proxmoxpb.AddSSHKeyRequest{Name: "desktop", PublicKey: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIGq0JZ6bC2b3m9Hcm6bE0u8bC4S0kq6yXH2b2Z4vX7aL me@desktop"})
		if err != nil {
			t.Fatal(err)
		}
		if pasted.GetKey().GetGenerated() || !strings.HasSuffix(pasted.GetKey().GetPublicKey(), " me@desktop") {
			t.Errorf("pasted = %v", pasted)
		}

		inst, err := c.InstallSSHKey(actionCtx(t), &proxmoxpb.InstallSSHKeyRequest{Workload: lxc(102), Names: []string{"laptop", "desktop"}, User: "ops"})
		if err != nil {
			t.Fatal(err)
		}
		if len(inst.GetAlready()) != 0 {
			t.Errorf("already = %v", inst.GetAlready())
		}
		inst, _ = c.InstallSSHKey(actionCtx(t), &proxmoxpb.InstallSSHKeyRequest{Workload: lxc(102), Names: []string{"laptop"}, User: "ops"})
		if diff := cmp.Diff([]string{"laptop"}, inst.GetAlready()); diff != "" {
			t.Errorf("already (-want +got):\n%s", diff)
		}
		if _, err := c.DeleteSSHKey(actionCtx(t), &proxmoxpb.DeleteSSHKeyRequest{Name: "laptop"}); err != nil {
			t.Fatal(err)
		}
	})
}

// --- Backups ---

func TestABackupIsFollowedToItsEnd(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		l := newLab()
		c, _ := startAll(t, l, newAgents(l))
		res, err := c.Backup(actionCtx(t), &proxmoxpb.BackupRequest{Workload: lxc(102), Storage: "pbs"})
		if err != nil {
			t.Fatal(err)
		}
		if res.GetBackup().GetState() != jobpb.State_STATE_RUNNING {
			t.Fatalf("backup = %v", res.GetBackup())
		}
		settle()
		list, _ := c.ListBackups(actionCtx(t), &proxmoxpb.ListBackupsRequest{})
		if b := list.GetBackups(); len(b) != 1 || b[0].GetState() != jobpb.State_STATE_DONE || b[0].GetEnded() == nil {
			t.Fatalf("backups = %v", b)
		}
		want := []string{"proxmox-api Backup 102 to pbs BACKUP_MODE_SNAPSHOT", "notify Backed up LXC 102 media-stack: to pbs (CATEGORY_ACTION_OUTCOMES) " + sdktest.PublicURL + "/proxmox/workloads/102"}
		if diff := cmp.Diff(want, l.calls()); diff != "" {
			t.Errorf("calls (-want +got):\n%s", diff)
		}
	})
}
