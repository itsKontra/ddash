package proxmoxapi

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"time"

	proxmox "github.com/luthermonson/go-proxmox"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	pb "github.com/itsKontra/ddash/components/proxmox-api/proxmoxapipb"
)

// taskPollInterval is how often a power Action reads its task's status.
var taskPollInterval = time.Second

func (c *component) StartWorkload(ctx context.Context, req *pb.StartWorkloadRequest) (*pb.StartWorkloadResponse, error) {
	upid, err := c.power(ctx, req.GetWorkload(), "start", nil)
	if err != nil {
		return nil, err
	}
	return &pb.StartWorkloadResponse{Upid: upid}, nil
}

func (c *component) ShutdownWorkload(ctx context.Context, req *pb.ShutdownWorkloadRequest) (*pb.ShutdownWorkloadResponse, error) {
	params := map[string]any{}
	if t := req.GetTimeout(); t != nil {
		params["timeout"] = int(t.AsDuration().Round(time.Second) / time.Second)
	}
	if req.GetForceStop() {
		params["forceStop"] = 1
	}
	upid, err := c.power(ctx, req.GetWorkload(), "shutdown", params)
	if err != nil {
		return nil, err
	}
	return &pb.ShutdownWorkloadResponse{Upid: upid}, nil
}

func (c *component) RebootWorkload(ctx context.Context, req *pb.RebootWorkloadRequest) (*pb.RebootWorkloadResponse, error) {
	upid, err := c.power(ctx, req.GetWorkload(), "reboot", nil)
	if err != nil {
		return nil, err
	}
	return &pb.RebootWorkloadResponse{Upid: upid}, nil
}

// power asks for status/<action> on target and follows the task Proxmox
// starts until it ends, returning its UPID. A task still running when ctx
// ends carries on in Proxmox; ListActiveTasks lists it.
func (c *component) power(ctx context.Context, target *pb.Target, action string, params map[string]any) (string, error) {
	kind, label, err := kindOf(target)
	if err != nil {
		return "", err
	}
	// Whatever happens, the status read before may be stale now.
	defer c.workloads.Invalidate()
	var upid string
	path := fmt.Sprintf("/nodes/%s/%s/%d/status/%s", url.PathEscape(target.GetNode()), kind, target.GetVmid(), action)
	if err := c.client.Post(ctx, path, params, &upid); err != nil {
		if ctx.Err() != nil {
			return "", status.FromContextError(ctx.Err()).Err()
		}
		if proxmox.IsNotAuthorized(err) {
			return "", status.Errorf(codes.PermissionDenied, "Proxmox refused to %s %s: ddash doesn't hold VM.PowerMgmt on it", action, label)
		}
		return "", status.Errorf(codes.Unavailable, "Proxmox refused to %s %s: %v", action, label, err)
	}
	return upid, c.follow(ctx, target.GetNode(), upid)
}

// follow reads a task's status until it stopped: nil when it ended OK.
func (c *component) follow(ctx context.Context, node, upid string) error {
	for {
		var task struct {
			Type       string `json:"type"`
			Status     string `json:"status"`
			ExitStatus string `json:"exitstatus"`
		}
		err := c.client.Get(ctx, fmt.Sprintf("/nodes/%s/tasks/%s/status", url.PathEscape(node), url.PathEscape(upid)), &task)
		switch {
		case ctx.Err() != nil:
			return taskRunning(ctx, upid)
		case err != nil:
			// A failed read doesn't end the task: try again.
		case task.Status == "stopped" && task.ExitStatus == "OK":
			return nil
		case task.Status == "stopped":
			return status.Errorf(codes.FailedPrecondition, "Proxmox's %s task failed: %s", task.Type, task.ExitStatus)
		}
		select {
		case <-ctx.Done():
			return taskRunning(ctx, upid)
		case <-time.After(taskPollInterval):
		}
	}
}

// taskRunning is the error for a task still running when ctx ended.
func taskRunning(ctx context.Context, upid string) error {
	code := codes.DeadlineExceeded
	if errors.Is(ctx.Err(), context.Canceled) {
		code = codes.Canceled
	}
	return status.Errorf(code, "Proxmox is still running task %s", upid)
}

// kindOf is the API's path segment and a label ("LXC 101") for target.
func kindOf(target *pb.Target) (kind, label string, err error) {
	if target.GetNode() == "" || target.GetVmid() == 0 {
		return "", "", status.Error(codes.InvalidArgument, "workload needs its node and VMID")
	}
	switch target.GetKind() {
	case pb.Kind_KIND_LXC:
		return "lxc", fmt.Sprintf("LXC %d", target.GetVmid()), nil
	case pb.Kind_KIND_VM:
		return "qemu", fmt.Sprintf("VM %d", target.GetVmid()), nil
	}
	return "", "", status.Error(codes.InvalidArgument, "workload kind must be LXC or VM")
}

// ListActiveTasks reads the node's running tasks on the Workload: the
// token sees the tasks it started.
func (c *component) ListActiveTasks(ctx context.Context, req *pb.ListActiveTasksRequest) (*pb.ListActiveTasksResponse, error) {
	if req.GetNode() == "" || req.GetVmid() == 0 {
		return nil, status.Error(codes.InvalidArgument, "node and vmid are required")
	}
	var tasks []struct {
		UPID      string `json:"upid"`
		Type      string `json:"type"`
		StartTime int64  `json:"starttime"`
	}
	path := fmt.Sprintf("/nodes/%s/tasks?source=active&vmid=%d", url.PathEscape(req.GetNode()), req.GetVmid())
	if err := c.client.Get(ctx, path, &tasks); err != nil {
		return nil, unavailable(err)
	}
	out := &pb.ListActiveTasksResponse{}
	for _, t := range tasks {
		out.Tasks = append(out.Tasks, &pb.Task{Upid: t.UPID, Type: t.Type, Started: timestamppb.New(time.Unix(t.StartTime, 0))})
	}
	slices.SortFunc(out.Tasks, func(a, b *pb.Task) int { return strings.Compare(a.GetUpid(), b.GetUpid()) })
	return out, nil
}
