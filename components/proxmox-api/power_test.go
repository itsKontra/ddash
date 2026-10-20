package proxmoxapi_test

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/durationpb"

	proxmoxapi "github.com/itsKontra/ddash/components/proxmox-api"
	pb "github.com/itsKontra/ddash/components/proxmox-api/proxmoxapipb"
	"github.com/itsKontra/ddash/components/proxmox-api/proxmoxfake"
)

// node serves the stateful Proxmox fake on the baseline, polled for tasks
// every millisecond, and starts proxmox-api against it.
func node(t *testing.T) (*proxmoxfake.Proxmox, pb.ProxmoxApiServiceClient) {
	t.Helper()
	proxmoxapi.PollTasksEvery(t, time.Millisecond)
	fake, err := proxmoxfake.New("testdata/baseline", proxmoxfake.Options{
		Miss: func(request string, err error) { t.Errorf("unexpected request %s: %v", request, err) },
	})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(fake)
	t.Cleanup(srv.Close)
	_, c := start(t, &pb.ProxmoxApiConfig{Url: srv.URL, TokenId: "ddash@pve!dashboard", TokenSecret: "4c1f9a3e-0000-4000-8000-000000000000"})
	return fake, c
}

func lxc(vmid uint32) *pb.Target { return &pb.Target{Kind: pb.Kind_KIND_LXC, Node: "pve", Vmid: vmid} }

// powerRequests is the fake's log without the reads of Workloads and
// their addresses: the power requests and task reads.
func powerRequests(fake *proxmoxfake.Proxmox) []string {
	var out []string
	for _, r := range fake.Log() {
		if strings.HasPrefix(r, "POST ") || strings.Contains(r, "/tasks") {
			out = append(out, r)
		}
	}
	return out
}

func TestStartReturnsOnceTheTaskEndedOK(t *testing.T) {
	fake, c := node(t)

	res, err := c.StartWorkload(t.Context(), &pb.StartWorkloadRequest{Workload: lxc(103)})
	if err != nil {
		t.Fatal(err)
	}

	if got := fake.Status(103); got != "running" {
		t.Errorf("103 is %s, want running", got)
	}
	upid := res.GetUpid()
	want := []string{
		"POST /api2/json/nodes/pve/lxc/103/status/start",
		"GET /api2/json/nodes/pve/tasks/" + upid + "/status",
		"GET /api2/json/nodes/pve/tasks/" + upid + "/status",
	}
	if diff := cmp.Diff(want, powerRequests(fake)); diff != "" {
		t.Errorf("requests (-want +got):\n%s", diff)
	}
	if w := workloads(t, c)[103]; w.GetStatus() != "running" {
		t.Errorf("ListWorkloads has 103 %s after the Action, want running (the cache dropped)", w.GetStatus())
	}
}

func TestShutdownAsksForACleanShutdownForcedAfterTheTimeout(t *testing.T) {
	fake, c := node(t)

	_, err := c.ShutdownWorkload(t.Context(), &pb.ShutdownWorkloadRequest{
		Workload: lxc(102), Timeout: durationpb.New(45 * time.Second), ForceStop: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	if got := fake.Status(102); got != "stopped" {
		t.Errorf("102 is %s, want stopped", got)
	}
	if got := powerRequests(fake)[0]; got != "POST /api2/json/nodes/pve/lxc/102/status/shutdown forceStop=1 timeout=45" {
		t.Errorf("first request = %q, want the shutdown with forceStop=1 timeout=45", got)
	}
}

func TestRebootRunsTheRebootTaskOnAVM(t *testing.T) {
	fake, c := node(t)

	if _, err := c.RebootWorkload(t.Context(), &pb.RebootWorkloadRequest{Workload: &pb.Target{Kind: pb.Kind_KIND_VM, Node: "pve", Vmid: 112}}); err != nil {
		t.Fatal(err)
	}

	if got := fake.Status(112); got != "running" {
		t.Errorf("112 is %s, want running again", got)
	}
	if got := powerRequests(fake)[0]; got != "POST /api2/json/nodes/pve/qemu/112/status/reboot" {
		t.Errorf("first request = %q, want qemu/112's reboot", got)
	}
}

func TestProxmoxRefusesPowerOnAProtectedWorkload(t *testing.T) {
	fake, c := node(t)

	// 101 (nginx) holds only VM.Audit and VM.GuestAgent.Audit.
	_, err := c.ShutdownWorkload(t.Context(), &pb.ShutdownWorkloadRequest{Workload: lxc(101), ForceStop: true})

	if status.Code(err) != codes.PermissionDenied {
		t.Errorf("error = %v, want PermissionDenied", err)
	}
	if got := fake.Status(101); got != "running" {
		t.Errorf("101 is %s, want running still", got)
	}
}

func TestAFailedTaskIsAnErrorWithItsExitStatus(t *testing.T) {
	fake, c := node(t)
	fake.FailNextTask("command 'lxc-start -n 103' failed: exit code 1")

	_, err := c.StartWorkload(t.Context(), &pb.StartWorkloadRequest{Workload: lxc(103)})

	if status.Code(err) != codes.FailedPrecondition || status.Convert(err).Message() != "Proxmox's vzstart task failed: command 'lxc-start -n 103' failed: exit code 1" {
		t.Errorf("error = %v, want FailedPrecondition with the exit status", err)
	}
	if got := fake.Status(103); got != "stopped" {
		t.Errorf("103 is %s, want stopped still", got)
	}
}

func TestATaskThatOutlastsTheDeadlineIsListedActiveUntilItEnds(t *testing.T) {
	fake, c := node(t)
	fake.SetTaskPolls(1 << 30)

	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	_, err := c.StartWorkload(ctx, &pb.StartWorkloadRequest{Workload: lxc(103)})
	if status.Code(err) != codes.DeadlineExceeded {
		t.Fatalf("error = %v, want DeadlineExceeded", err)
	}

	active, err := c.ListActiveTasks(t.Context(), &pb.ListActiveTasksRequest{Node: "pve", Vmid: 103})
	if err != nil {
		t.Fatal(err)
	}
	if n := len(active.GetTasks()); n != 1 || active.GetTasks()[0].GetType() != "vzstart" {
		t.Fatalf("active tasks = %v, want the vzstart", active.GetTasks())
	}
	if active.GetTasks()[0].GetStarted() == nil {
		t.Error("the active task has no start time")
	}

	fake.FinishTasks()
	active, err = c.ListActiveTasks(t.Context(), &pb.ListActiveTasksRequest{Node: "pve", Vmid: 103})
	if err != nil {
		t.Fatal(err)
	}
	if n := len(active.GetTasks()); n != 0 {
		t.Errorf("active tasks after it ended = %v, want none", active.GetTasks())
	}
	if got := fake.Status(103); got != "running" {
		t.Errorf("103 is %s, want running once the task ended", got)
	}
}
