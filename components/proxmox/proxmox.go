// Package proxmox is the proxmox Component, the logic behind the Proxmox
// Page's power Actions. It starts, stops and restarts LXCs and VMs through
// proxmox-api, refuses a Workload ddash holds no VM.PowerMgmt on
// (Protected), runs one power Action per Workload at a time, and puts a
// Workload it stops or restarts into Maintenance through checks, in the
// Action's own Audit Log chain.
package proxmox

import (
	"context"
	_ "embed"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"strconv"
	"sync"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/itsKontra/ddash/components/checks/checkspb"
	"github.com/itsKontra/ddash/components/proxmox-api/proxmoxapipb"
	"github.com/itsKontra/ddash/components/proxmox/proxmoxpb"
	workloadpb "github.com/itsKontra/ddash/proto/ddash/workload"
	"github.com/itsKontra/ddash/sdk"
)

//go:embed component.yaml
var manifest []byte

const (
	// ShutdownTimeout is how long Stop waits for a clean shutdown before
	// Proxmox stops the Workload hard: it leaves the force-stop room inside
	// the host's 60 s Action deadline.
	ShutdownTimeout = 45 * time.Second
	// RestartMaintenance is how long a restarted Workload stays in
	// Maintenance at most, unless its Checks are up before.
	RestartMaintenance = 5 * time.Minute
	// FollowInterval is how often proxmox asks whether a task that
	// outlasted its Action's deadline still runs.
	FollowInterval = 5 * time.Second
	// FollowAtMost is how long proxmox follows such a task before it
	// frees the Workload anyway.
	FollowAtMost = 15 * time.Minute
	// callTimeout bounds a read or a Maintenance call made outside an
	// Action's deadline.
	callTimeout = 10 * time.Second
)

// PowerMgmt is the Proxmox privilege a power Action needs.
const PowerMgmt = "VM.PowerMgmt"

// Definition is proxmox, ready for sdk.Serve.
func Definition() sdk.Definition {
	return sdk.Define(sdk.Spec[*proxmoxpb.ProxmoxConfig]{
		Manifest: manifest,
		Service:  &proxmoxpb.ProxmoxService_ServiceDesc,
		New: func() sdk.Component[*proxmoxpb.ProxmoxConfig] {
			return &component{inFlight: map[uint32]*proxmoxpb.PowerTask{}}
		},
	})
}

type component struct {
	proxmoxpb.UnimplementedProxmoxServiceServer
	api    proxmoxapipb.ProxmoxApiServiceClient
	checks checkspb.ChecksServiceClient
	log    *slog.Logger
	// life ends when the Component stops; a task followed past its
	// Action's deadline is followed until then at most.
	life context.Context

	mu sync.Mutex
	// inFlight is the power Action running on each Workload, by VMID.
	inFlight map[uint32]*proxmoxpb.PowerTask
}

func (c *component) Start(ctx context.Context, env *sdk.Env, _ *proxmoxpb.ProxmoxConfig) error {
	c.api = sdk.Require(env, proxmoxapipb.NewProxmoxApiServiceClient)
	c.checks = sdk.Require(env, checkspb.NewChecksServiceClient)
	c.log = env.Log
	c.life = ctx
	return nil
}

// Health is always ok: proxmox keeps nothing that can fail.
func (c *component) Health(context.Context) sdk.Health {
	return sdk.Health{State: sdk.OK}
}

// ListPowerTasks answers from memory, by VMID.
func (c *component) ListPowerTasks(context.Context, *proxmoxpb.ListPowerTasksRequest) (*proxmoxpb.ListPowerTasksResponse, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := &proxmoxpb.ListPowerTasksResponse{}
	for _, vmid := range slices.Sorted(maps.Keys(c.inFlight)) {
		out.Tasks = append(out.Tasks, c.inFlight[vmid])
	}
	return out, nil
}

func (c *component) StartWorkload(ctx context.Context, req *proxmoxpb.StartWorkloadRequest) (*proxmoxpb.StartWorkloadResponse, error) {
	return &proxmoxpb.StartWorkloadResponse{}, c.run(ctx, req.GetWorkload(), proxmoxpb.PowerAction_POWER_ACTION_START)
}

func (c *component) StopWorkload(ctx context.Context, req *proxmoxpb.StopWorkloadRequest) (*proxmoxpb.StopWorkloadResponse, error) {
	return &proxmoxpb.StopWorkloadResponse{}, c.run(ctx, req.GetWorkload(), proxmoxpb.PowerAction_POWER_ACTION_STOP)
}

func (c *component) RestartWorkload(ctx context.Context, req *proxmoxpb.RestartWorkloadRequest) (*proxmoxpb.RestartWorkloadResponse, error) {
	return &proxmoxpb.RestartWorkloadResponse{}, c.run(ctx, req.GetWorkload(), proxmoxpb.PowerAction_POWER_ACTION_RESTART)
}

// workload is the Workload a power Action acts on, as proxmox-api lists it.
type workload struct {
	*proxmoxapipb.Workload
	// ref is it for checks: the node it is on now and its VMID.
	ref *workloadpb.WorkloadRef
}

// label is "LXC 102 media-stack".
func (w workload) label() string {
	kind := "LXC"
	if w.GetKind() == proxmoxapipb.Kind_KIND_VM {
		kind = "VM"
	}
	return fmt.Sprintf("%s %d %s", kind, w.GetVmid(), w.GetName())
}

func (w workload) target() *proxmoxapipb.Target {
	return &proxmoxapipb.Target{Kind: w.GetKind(), Node: w.GetNode(), Vmid: w.GetVmid()}
}

var actionNames = map[proxmoxpb.PowerAction]string{
	proxmoxpb.PowerAction_POWER_ACTION_START:   "Start",
	proxmoxpb.PowerAction_POWER_ACTION_STOP:    "Stop",
	proxmoxpb.PowerAction_POWER_ACTION_RESTART: "Restart",
}

// run checks and claims the Workload, then runs the power Action with its
// Maintenance calls.
func (c *component) run(ctx context.Context, ref *workloadpb.WorkloadRef, action proxmoxpb.PowerAction) error {
	w, err := c.resolve(ctx, ref)
	if err != nil {
		return err
	}
	if err := c.claim(w, action); err != nil {
		return err
	}
	running := w.GetStatus() == "running"
	switch {
	case action == proxmoxpb.PowerAction_POWER_ACTION_START && running:
		c.free(w)
		return status.Errorf(codes.FailedPrecondition, "%s is already running", w.label())
	case action != proxmoxpb.PowerAction_POWER_ACTION_START && !running:
		c.free(w)
		return status.Errorf(codes.FailedPrecondition, "%s isn't running", w.label())
	}

	switch action {
	case proxmoxpb.PowerAction_POWER_ACTION_STOP:
		// Covers the shutdown itself; replaced once the task ended.
		c.maintain(ctx, w, nil, false)
	case proxmoxpb.PowerAction_POWER_ACTION_RESTART:
		c.maintain(ctx, w, durationpb.New(RestartMaintenance), false)
	}
	err = c.power(ctx, w, action)
	if code := status.Code(err); code == codes.DeadlineExceeded || code == codes.Canceled || ctx.Err() != nil {
		// Proxmox carries on: keep the Workload claimed until its task
		// ended, then finish as if the Action had waited.
		go c.follow(ctx, w, action)
		return status.Errorf(codes.DeadlineExceeded, "Proxmox is still running the %s of %s; ddash waits for it to end", actionNames[action], w.label())
	}
	c.finish(ctx, w, action, err == nil)
	return err
}

// resolve finds the Workload ref names among proxmox-api's (by kind and
// VMID: the node may have changed) and refuses one ddash holds no
// VM.PowerMgmt on.
func (c *component) resolve(ctx context.Context, ref *workloadpb.WorkloadRef) (workload, error) {
	var kind proxmoxapipb.Kind
	switch ref.GetKind() {
	case workloadpb.Kind_KIND_LXC:
		kind = proxmoxapipb.Kind_KIND_LXC
	case workloadpb.Kind_KIND_VM:
		kind = proxmoxapipb.Kind_KIND_VM
	default:
		return workload{}, status.Error(codes.InvalidArgument, "workload must be an LXC or a VM")
	}
	vmid, err := strconv.ParseUint(ref.GetId(), 10, 32)
	if err != nil || vmid == 0 {
		return workload{}, status.Errorf(codes.InvalidArgument, "workload id %q isn't a VMID", ref.GetId())
	}
	list, err := c.api.ListWorkloads(ctx, &proxmoxapipb.ListWorkloadsRequest{})
	if err != nil {
		return workload{}, err
	}
	var w workload
	for _, l := range list.GetWorkloads() {
		if l.GetKind() == kind && uint64(l.GetVmid()) == vmid {
			w.Workload = l
		}
	}
	if w.Workload == nil {
		return workload{}, status.Errorf(codes.NotFound, "Proxmox lists no %s %d", map[proxmoxapipb.Kind]string{proxmoxapipb.Kind_KIND_LXC: "LXC", proxmoxapipb.Kind_KIND_VM: "VM"}[kind], vmid)
	}
	w.ref = &workloadpb.WorkloadRef{Kind: ref.GetKind(), Host: w.GetNode(), Id: strconv.FormatUint(vmid, 10)}
	perms, err := c.api.ListPermissions(ctx, &proxmoxapipb.ListPermissionsRequest{})
	if err != nil {
		return workload{}, err
	}
	for _, p := range perms.GetWorkloads() {
		if p.GetVmid() == w.GetVmid() && slices.Contains(p.GetPrivileges(), PowerMgmt) {
			return w, nil
		}
	}
	return workload{}, status.Errorf(codes.PermissionDenied, "%s is protected in Proxmox: ddash doesn't hold %s on it", w.label(), PowerMgmt)
}

// claim marks the Workload in flight, refusing a second power Action.
func (c *component) claim(w workload, action proxmoxpb.PowerAction) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if t, ok := c.inFlight[w.GetVmid()]; ok {
		return status.Errorf(codes.FailedPrecondition, "A %s is already running on %s", actionNames[t.GetAction()], w.label())
	}
	c.inFlight[w.GetVmid()] = &proxmoxpb.PowerTask{Workload: w.ref, Action: action, Started: timestamppb.Now()}
	return nil
}

func (c *component) free(w workload) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.inFlight, w.GetVmid())
}

// power asks proxmox-api for the Action's task and waits for it.
func (c *component) power(ctx context.Context, w workload, action proxmoxpb.PowerAction) error {
	var err error
	switch action {
	case proxmoxpb.PowerAction_POWER_ACTION_START:
		_, err = c.api.StartWorkload(ctx, &proxmoxapipb.StartWorkloadRequest{Workload: w.target()})
	case proxmoxpb.PowerAction_POWER_ACTION_STOP:
		_, err = c.api.ShutdownWorkload(ctx, &proxmoxapipb.ShutdownWorkloadRequest{
			Workload: w.target(), Timeout: durationpb.New(ShutdownTimeout), ForceStop: true,
		})
	case proxmoxpb.PowerAction_POWER_ACTION_RESTART:
		_, err = c.api.RebootWorkload(ctx, &proxmoxapipb.RebootWorkloadRequest{Workload: w.target()})
	}
	return err
}

// finish makes the Maintenance call that follows a power task and frees
// the Workload: a started Workload's Maintenance ends; a stopped or
// restarted one's lasts until its Checks are up, a restart's at most until
// 5 min after the Restart (none follows once those are over). A stop or
// restart that failed gets the same call: the Workload is likely up, so
// its next good beats end it.
func (c *component) finish(ctx context.Context, w workload, action proxmoxpb.PowerAction, ok bool) {
	defer c.free(w)
	switch action {
	case proxmoxpb.PowerAction_POWER_ACTION_START:
		if !ok {
			return
		}
		scope := &checkspb.MaintenanceScope{Scope: &checkspb.MaintenanceScope_Workload{Workload: w.ref}}
		if _, err := c.checks.EndMaintenance(ctx, &checkspb.EndMaintenanceRequest{Scope: scope}); err != nil {
			c.log.Warn("can't end the Maintenance of a started Workload", "workload", w.label(), "err", err)
		}
	case proxmoxpb.PowerAction_POWER_ACTION_STOP:
		c.maintain(ctx, w, nil, true)
	case proxmoxpb.PowerAction_POWER_ACTION_RESTART:
		left := (RestartMaintenance - time.Since(c.started(w))).Round(time.Second)
		if left > 0 {
			c.maintain(ctx, w, durationpb.New(left), true)
		}
	}
}

// started is when the Workload's power Action began.
func (c *component) started(w workload) time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.inFlight[w.GetVmid()].GetStarted().AsTime()
}

// maintain puts the Workload into Maintenance for d (nil: until ended). A
// failure doesn't stop the power Action: the Operator may get paged, but
// the Workload still gets what they asked for.
func (c *component) maintain(ctx context.Context, w workload, d *durationpb.Duration, untilUp bool) {
	scope := &checkspb.MaintenanceScope{Scope: &checkspb.MaintenanceScope_Workload{Workload: w.ref}}
	if _, err := c.checks.StartMaintenance(ctx, &checkspb.StartMaintenanceRequest{Scope: scope, Duration: d, UntilUp: untilUp}); err != nil {
		c.log.Warn("can't put a Workload into Maintenance", "workload", w.label(), "err", err)
	}
}

// follow waits, past the Action's deadline, for the Workload's task to
// end, then finishes the Action in its own Audit Log chain: the detached
// ctx keeps the call's actor and chain.
func (c *component) follow(callCtx context.Context, w workload, action proxmoxpb.PowerAction) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(callCtx), FollowAtMost)
	defer cancel()
	stop := context.AfterFunc(c.life, cancel)
	defer stop()
	for c.taskRunning(ctx, w) {
		select {
		case <-ctx.Done():
			c.log.Warn("stopped following a power task", "workload", w.label(), "err", ctx.Err())
			c.free(w)
			return
		case <-time.After(FollowInterval):
		}
	}
	callCtx, cancelCall := context.WithTimeout(ctx, callTimeout)
	defer cancelCall()
	c.finish(callCtx, w, action, true)
}

// taskRunning reports whether Proxmox still runs a task ddash started on
// the Workload; a failed read counts as running.
func (c *component) taskRunning(ctx context.Context, w workload) bool {
	ctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	res, err := c.api.ListActiveTasks(ctx, &proxmoxapipb.ListActiveTasksRequest{Node: w.GetNode(), Vmid: w.GetVmid()})
	if err != nil {
		return true
	}
	return len(res.GetTasks()) > 0
}
