package proxmox_test

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/google/go-cmp/cmp"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/itsKontra/ddash/components/checks/checkspb"
	"github.com/itsKontra/ddash/components/proxmox"
	"github.com/itsKontra/ddash/components/proxmox-api/proxmoxapipb"
	"github.com/itsKontra/ddash/components/proxmox/proxmoxpb"
	workloadpb "github.com/itsKontra/ddash/proto/ddash/workload"
	"github.com/itsKontra/ddash/sdk/sdktest"
)

// lab fakes proxmox-api and checks for proxmox, logging the calls that
// change something to both in one order.
type lab struct {
	mu sync.Mutex
	// status by VMID; every Workload is on node pve2 (it moved).
	status    map[uint32]string
	kinds     map[uint32]proxmoxapipb.Kind
	protected map[uint32]bool
	log       []string
	// hold keeps power tasks running until release.
	hold     bool
	released chan struct{}
	active   map[uint32]bool
}

func newLab() *lab {
	return &lab{
		status: map[uint32]string{101: "running", 102: "running", 103: "stopped", 110: "running"},
		kinds: map[uint32]proxmoxapipb.Kind{
			101: proxmoxapipb.Kind_KIND_LXC, 102: proxmoxapipb.Kind_KIND_LXC,
			103: proxmoxapipb.Kind_KIND_LXC, 110: proxmoxapipb.Kind_KIND_VM,
		},
		protected: map[uint32]bool{101: true},
		released:  make(chan struct{}),
		active:    map[uint32]bool{},
	}
}

func (l *lab) record(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.log = append(l.log, fmt.Sprintf(format, args...))
}

func (l *lab) calls() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.Clone(l.log)
}

// holdTasks keeps the power tasks started from now on running until
// release.
func (l *lab) holdTasks() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.hold = true
}

// release ends the held tasks.
func (l *lab) release() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.hold = false
	close(l.released)
	l.released = make(chan struct{})
}

func (l *lab) statusOf(vmid uint32) string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.status[vmid]
}

type proxmoxAPI struct {
	proxmoxapipb.ProxmoxApiServiceClient
	l *lab
}

func (p proxmoxAPI) ListWorkloads(context.Context, *proxmoxapipb.ListWorkloadsRequest, ...grpc.CallOption) (*proxmoxapipb.ListWorkloadsResponse, error) {
	p.l.mu.Lock()
	defer p.l.mu.Unlock()
	out := &proxmoxapipb.ListWorkloadsResponse{}
	for _, id := range []uint32{101, 102, 103, 110} {
		out.Workloads = append(out.Workloads, &proxmoxapipb.Workload{
			Kind: p.l.kinds[id], Vmid: id, Node: "pve2", Name: names[id], Status: p.l.status[id],
		})
	}
	return out, nil
}

var names = map[uint32]string{101: "nginx", 102: "media-stack", 103: "ddash-smoke", 110: "home-assistant"}

func (p proxmoxAPI) ListPermissions(context.Context, *proxmoxapipb.ListPermissionsRequest, ...grpc.CallOption) (*proxmoxapipb.ListPermissionsResponse, error) {
	p.l.mu.Lock()
	defer p.l.mu.Unlock()
	out := &proxmoxapipb.ListPermissionsResponse{}
	for _, id := range []uint32{101, 102, 103, 110} {
		privs := []string{"VM.Audit", "VM.GuestAgent.Audit", "VM.PowerMgmt"}
		if p.l.protected[id] {
			privs = privs[:2]
		}
		out.Workloads = append(out.Workloads, &proxmoxapipb.WorkloadPermissions{Vmid: id, Privileges: privs})
	}
	return out, nil
}

// power runs a task that ends at once, or on release while held.
func (p proxmoxAPI) power(ctx context.Context, t *proxmoxapipb.Target, call, after string) error {
	p.l.mu.Lock()
	p.l.log = append(p.l.log, fmt.Sprintf("proxmox-api %s %v/%s/%d", call, t.GetKind(), t.GetNode(), t.GetVmid()))
	hold, released := p.l.hold, p.l.released
	if hold {
		p.l.active[t.GetVmid()] = true
	}
	p.l.mu.Unlock()
	end := func() {
		p.l.mu.Lock()
		defer p.l.mu.Unlock()
		p.l.status[t.GetVmid()] = after
		delete(p.l.active, t.GetVmid())
	}
	if !hold {
		end()
		return nil
	}
	go func() {
		<-released
		end()
	}()
	select {
	case <-released:
		return nil
	case <-ctx.Done():
		return status.Error(codes.DeadlineExceeded, "Proxmox is still running task UPID:pve2:1")
	}
}

func (p proxmoxAPI) StartWorkload(ctx context.Context, req *proxmoxapipb.StartWorkloadRequest, _ ...grpc.CallOption) (*proxmoxapipb.StartWorkloadResponse, error) {
	return &proxmoxapipb.StartWorkloadResponse{}, p.power(ctx, req.GetWorkload(), "StartWorkload", "running")
}

func (p proxmoxAPI) ShutdownWorkload(ctx context.Context, req *proxmoxapipb.ShutdownWorkloadRequest, _ ...grpc.CallOption) (*proxmoxapipb.ShutdownWorkloadResponse, error) {
	call := fmt.Sprintf("ShutdownWorkload timeout=%v force=%v", req.GetTimeout().AsDuration(), req.GetForceStop())
	return &proxmoxapipb.ShutdownWorkloadResponse{}, p.power(ctx, req.GetWorkload(), call, "stopped")
}

func (p proxmoxAPI) RebootWorkload(ctx context.Context, req *proxmoxapipb.RebootWorkloadRequest, _ ...grpc.CallOption) (*proxmoxapipb.RebootWorkloadResponse, error) {
	return &proxmoxapipb.RebootWorkloadResponse{}, p.power(ctx, req.GetWorkload(), "RebootWorkload", "running")
}

func (p proxmoxAPI) ListActiveTasks(_ context.Context, req *proxmoxapipb.ListActiveTasksRequest, _ ...grpc.CallOption) (*proxmoxapipb.ListActiveTasksResponse, error) {
	p.l.mu.Lock()
	defer p.l.mu.Unlock()
	out := &proxmoxapipb.ListActiveTasksResponse{}
	if req.GetNode() == "pve2" && p.l.active[req.GetVmid()] {
		out.Tasks = append(out.Tasks, &proxmoxapipb.Task{Upid: "UPID:pve2:1", Type: "vzshutdown", Started: timestamppb.Now()})
	}
	return out, nil
}

type checks struct {
	checkspb.ChecksServiceClient
	l *lab
}

func scopeText(s *checkspb.MaintenanceScope) string {
	w := s.GetWorkload()
	return fmt.Sprintf("%v/%s/%s", w.GetKind(), w.GetHost(), w.GetId())
}

func (c checks) StartMaintenance(_ context.Context, req *checkspb.StartMaintenanceRequest, _ ...grpc.CallOption) (*checkspb.StartMaintenanceResponse, error) {
	text := fmt.Sprintf("checks StartMaintenance %s", scopeText(req.GetScope()))
	if d := req.GetDuration(); d != nil {
		text += " for " + d.AsDuration().String()
	}
	if req.GetUntilUp() {
		text += " until up"
	}
	c.l.record("%s", text)
	return &checkspb.StartMaintenanceResponse{}, nil
}

func (c checks) EndMaintenance(_ context.Context, req *checkspb.EndMaintenanceRequest, _ ...grpc.CallOption) (*checkspb.EndMaintenanceResponse, error) {
	c.l.record("checks EndMaintenance %s", scopeText(req.GetScope()))
	return &checkspb.EndMaintenanceResponse{}, nil
}

func start(t *testing.T, l *lab) proxmoxpb.ProxmoxServiceClient {
	t.Helper()
	h := sdktest.New(t, proxmox.Definition(),
		sdktest.WithClient[proxmoxapipb.ProxmoxApiServiceClient](proxmoxAPI{l: l}),
		sdktest.WithClient[checkspb.ChecksServiceClient](checks{l: l}))
	h.MustStart(&proxmoxpb.ProxmoxConfig{})
	return proxmoxpb.NewProxmoxServiceClient(h.Conn)
}

func lxc(vmid int) *workloadpb.WorkloadRef {
	// The Page's reference names the node it last saw.
	return &workloadpb.WorkloadRef{Kind: workloadpb.Kind_KIND_LXC, Host: "pve", Id: strconv.Itoa(vmid)}
}

// actionCtx bounds a call like the host's Action deadline.
func actionCtx(t *testing.T) context.Context {
	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func TestStopShutsDownCleanlyWithinTheDeadlineAndStaysInMaintenance(t *testing.T) {
	l := newLab()
	c := start(t, l)

	if _, err := c.StopWorkload(actionCtx(t), &proxmoxpb.StopWorkloadRequest{Workload: lxc(102)}); err != nil {
		t.Fatal(err)
	}

	if got := l.statusOf(102); got != "stopped" {
		t.Errorf("102 is %s, want stopped", got)
	}
	want := []string{
		// Covers the shutdown itself, until ended.
		"checks StartMaintenance KIND_LXC/pve2/102",
		"proxmox-api ShutdownWorkload timeout=45s force=true KIND_LXC/pve2/102",
		// After the task: until ddash starts it or its Checks are up.
		"checks StartMaintenance KIND_LXC/pve2/102 until up",
	}
	if diff := cmp.Diff(want, l.calls()); diff != "" {
		t.Errorf("calls (-want +got):\n%s", diff)
	}
	if proxmox.ShutdownTimeout+10*time.Second > 60*time.Second {
		t.Errorf("ShutdownTimeout %v leaves no room to force-stop inside the 60 s Action deadline", proxmox.ShutdownTimeout)
	}
}

func TestRestartRebootsAndIsInMaintenanceUntilUpOrFiveMinutes(t *testing.T) {
	l := newLab()
	c := start(t, l)

	vm := &workloadpb.WorkloadRef{Kind: workloadpb.Kind_KIND_VM, Host: "pve", Id: "110"}
	if _, err := c.RestartWorkload(actionCtx(t), &proxmoxpb.RestartWorkloadRequest{Workload: vm}); err != nil {
		t.Fatal(err)
	}

	want := []string{
		"checks StartMaintenance KIND_VM/pve2/110 for 5m0s",
		"proxmox-api RebootWorkload KIND_VM/pve2/110",
		"checks StartMaintenance KIND_VM/pve2/110 for 5m0s until up",
	}
	if diff := cmp.Diff(want, l.calls()); diff != "" {
		t.Errorf("calls (-want +got):\n%s", diff)
	}
}

// A Restart's Maintenance lasts 5 min from the Restart, however long the
// reboot task took: the call after the task keeps the end time, adding
// only until up, and none follows once the 5 min are over.
func TestARestartsMaintenanceEndsFiveMinutesAfterTheRestart(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		l := newLab()
		c := start(t, l)
		l.holdTasks()

		done := make(chan error, 1)
		go func() {
			_, err := c.RestartWorkload(actionCtx(t), &proxmoxpb.RestartWorkloadRequest{Workload: lxc(102)})
			done <- err
		}()
		time.Sleep(40 * time.Second)
		l.release()
		if err := <-done; err != nil {
			t.Fatal(err)
		}
		want := []string{
			"checks StartMaintenance KIND_LXC/pve2/102 for 5m0s",
			"proxmox-api RebootWorkload KIND_LXC/pve2/102",
			"checks StartMaintenance KIND_LXC/pve2/102 for 4m20s until up",
		}
		if diff := cmp.Diff(want, l.calls()); diff != "" {
			t.Errorf("calls after a 40 s reboot (-want +got):\n%s", diff)
		}

		// A reboot outlasting the deadline and then the 5 min.
		l = newLab()
		c = start(t, l)
		l.holdTasks()
		if _, err := c.RestartWorkload(actionCtx(t), &proxmoxpb.RestartWorkloadRequest{Workload: lxc(102)}); status.Code(err) != codes.DeadlineExceeded {
			t.Fatalf("Restart: %v, want DeadlineExceeded", err)
		}
		time.Sleep(6 * time.Minute)
		l.release()
		time.Sleep(proxmox.FollowInterval * 2)
		synctest.Wait()
		want = []string{
			"checks StartMaintenance KIND_LXC/pve2/102 for 5m0s",
			"proxmox-api RebootWorkload KIND_LXC/pve2/102",
		}
		if diff := cmp.Diff(want, l.calls()); diff != "" {
			t.Errorf("calls after a 6 min reboot (-want +got):\n%s", diff)
		}
		if got := powerTasks(t, c); len(got) != 0 {
			t.Errorf("power tasks after = %v, want none", got)
		}
	})
}

func TestStartStartsAndEndsTheMaintenanceOfAStop(t *testing.T) {
	l := newLab()
	c := start(t, l)

	if _, err := c.StartWorkload(actionCtx(t), &proxmoxpb.StartWorkloadRequest{Workload: lxc(103)}); err != nil {
		t.Fatal(err)
	}

	if got := l.statusOf(103); got != "running" {
		t.Errorf("103 is %s, want running", got)
	}
	want := []string{
		"proxmox-api StartWorkload KIND_LXC/pve2/103",
		"checks EndMaintenance KIND_LXC/pve2/103",
	}
	if diff := cmp.Diff(want, l.calls()); diff != "" {
		t.Errorf("calls (-want +got):\n%s", diff)
	}
}

func TestAProtectedWorkloadIsRefusedWithoutAPowerCall(t *testing.T) {
	l := newLab()
	c := start(t, l)

	for name, call := range map[string]func() error{
		"Start": func() error {
			_, err := c.StartWorkload(actionCtx(t), &proxmoxpb.StartWorkloadRequest{Workload: lxc(101)})
			return err
		},
		"Stop": func() error {
			_, err := c.StopWorkload(actionCtx(t), &proxmoxpb.StopWorkloadRequest{Workload: lxc(101)})
			return err
		},
		"Restart": func() error {
			_, err := c.RestartWorkload(actionCtx(t), &proxmoxpb.RestartWorkloadRequest{Workload: lxc(101)})
			return err
		},
	} {
		err := call()
		if status.Code(err) != codes.PermissionDenied || status.Convert(err).Message() != "LXC 101 nginx is protected in Proxmox: ddash doesn't hold VM.PowerMgmt on it" {
			t.Errorf("%s: error = %v, want PermissionDenied: protected in Proxmox", name, err)
		}
	}
	if got := l.calls(); len(got) != 0 {
		t.Errorf("calls = %v, want none", got)
	}
	if got := l.statusOf(101); got != "running" {
		t.Errorf("101 is %s, want running still", got)
	}
}

func TestAnActionOnAnUnknownOrWrongStateWorkloadIsRefused(t *testing.T) {
	l := newLab()
	c := start(t, l)

	_, err := c.StartWorkload(actionCtx(t), &proxmoxpb.StartWorkloadRequest{Workload: lxc(999)})
	if status.Code(err) != codes.NotFound {
		t.Errorf("Start 999: %v, want NotFound", err)
	}
	// 110 is a VM, not an LXC.
	_, err = c.StartWorkload(actionCtx(t), &proxmoxpb.StartWorkloadRequest{Workload: lxc(110)})
	if status.Code(err) != codes.NotFound {
		t.Errorf("Start LXC 110: %v, want NotFound", err)
	}
	_, err = c.StartWorkload(actionCtx(t), &proxmoxpb.StartWorkloadRequest{Workload: lxc(102)})
	if status.Code(err) != codes.FailedPrecondition || status.Convert(err).Message() != "LXC 102 media-stack is already running" {
		t.Errorf("Start running 102: %v, want FailedPrecondition already running", err)
	}
	_, err = c.StopWorkload(actionCtx(t), &proxmoxpb.StopWorkloadRequest{Workload: lxc(103)})
	if status.Code(err) != codes.FailedPrecondition || status.Convert(err).Message() != "LXC 103 ddash-smoke isn't running" {
		t.Errorf("Stop stopped 103: %v, want FailedPrecondition isn't running", err)
	}
	_, err = c.StopWorkload(actionCtx(t), &proxmoxpb.StopWorkloadRequest{Workload: &workloadpb.WorkloadRef{Kind: workloadpb.Kind_KIND_DOCKER, Host: "media", Id: "plex"}})
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("Stop a container: %v, want InvalidArgument", err)
	}
	if got := l.calls(); len(got) != 0 {
		t.Errorf("calls = %v, want none", got)
	}
}

func powerTasks(t *testing.T, c proxmoxpb.ProxmoxServiceClient) []string {
	t.Helper()
	res, err := c.ListPowerTasks(t.Context(), &proxmoxpb.ListPowerTasksRequest{})
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, task := range res.GetTasks() {
		out = append(out, fmt.Sprintf("%v %v/%s", task.GetAction(), task.GetWorkload().GetKind(), task.GetWorkload().GetId()))
	}
	return out
}

func TestOnePowerActionPerWorkloadAtATime(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		l := newLab()
		c := start(t, l)
		l.holdTasks()

		done := make(chan error, 2)
		go func() {
			_, err := c.StopWorkload(actionCtx(t), &proxmoxpb.StopWorkloadRequest{Workload: lxc(102)})
			done <- err
		}()
		synctest.Wait()

		if diff := cmp.Diff([]string{"POWER_ACTION_STOP KIND_LXC/102"}, powerTasks(t, c)); diff != "" {
			t.Errorf("power tasks while stopping (-want +got):\n%s", diff)
		}
		_, err := c.RestartWorkload(actionCtx(t), &proxmoxpb.RestartWorkloadRequest{Workload: lxc(102)})
		if status.Code(err) != codes.FailedPrecondition || status.Convert(err).Message() != "A Stop is already running on LXC 102 media-stack" {
			t.Errorf("second Action: %v, want FailedPrecondition: a Stop is already running", err)
		}
		// Another Workload isn't held up.
		go func() {
			_, err := c.StartWorkload(actionCtx(t), &proxmoxpb.StartWorkloadRequest{Workload: lxc(103)})
			done <- err
		}()
		synctest.Wait()
		want := []string{"POWER_ACTION_STOP KIND_LXC/102", "POWER_ACTION_START KIND_LXC/103"}
		if diff := cmp.Diff(want, powerTasks(t, c)); diff != "" {
			t.Errorf("power tasks while 102 stops and 103 starts (-want +got):\n%s", diff)
		}

		l.release()
		for range 2 {
			if err := <-done; err != nil {
				t.Fatal(err)
			}
		}
		if got := powerTasks(t, c); len(got) != 0 {
			t.Errorf("power tasks after = %v, want none", got)
		}
	})
}

func TestATaskOutlastingTheDeadlineStaysInFlightUntilItEnds(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		l := newLab()
		c := start(t, l)
		l.holdTasks()

		_, err := c.StopWorkload(actionCtx(t), &proxmoxpb.StopWorkloadRequest{Workload: lxc(102)})
		if status.Code(err) != codes.DeadlineExceeded {
			t.Fatalf("Stop: %v, want DeadlineExceeded", err)
		}

		time.Sleep(5 * time.Minute)
		synctest.Wait()
		if diff := cmp.Diff([]string{"POWER_ACTION_STOP KIND_LXC/102"}, powerTasks(t, c)); diff != "" {
			t.Errorf("power tasks while the task runs on (-want +got):\n%s", diff)
		}
		if _, err := c.StopWorkload(actionCtx(t), &proxmoxpb.StopWorkloadRequest{Workload: lxc(102)}); status.Code(err) != codes.FailedPrecondition {
			t.Errorf("second Stop: %v, want FailedPrecondition", err)
		}

		l.release()
		time.Sleep(proxmox.FollowInterval)
		synctest.Wait()

		if got := powerTasks(t, c); len(got) != 0 {
			t.Errorf("power tasks after the task ended = %v, want none", got)
		}
		want := []string{
			"checks StartMaintenance KIND_LXC/pve2/102",
			"proxmox-api ShutdownWorkload timeout=45s force=true KIND_LXC/pve2/102",
			"checks StartMaintenance KIND_LXC/pve2/102 until up",
		}
		if diff := cmp.Diff(want, l.calls()); diff != "" {
			t.Errorf("calls (-want +got):\n%s", diff)
		}
	})
}
