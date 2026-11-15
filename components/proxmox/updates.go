package proxmox

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/itsKontra/ddash/components/host-agent-api/hostagentapipb"
	"github.com/itsKontra/ddash/components/proxmox-api/proxmoxapipb"
	"github.com/itsKontra/ddash/components/proxmox/proxmoxpb"
	jobpb "github.com/itsKontra/ddash/proto/ddash/job"
)

const (
	// JobPollInterval is how often a running update reads its Host Agent
	// job's progress.
	JobPollInterval = 2 * time.Second
	// UpdateAtMost bounds one Workload's update.
	UpdateAtMost = 2 * time.Hour
	// UpdateMaintenance is how long an updated Workload stays in
	// Maintenance at most after its update, unless its Checks are up
	// before: the update may have restarted its services.
	UpdateMaintenance = 5 * time.Minute
)

// The steps an update starts with, before its Host Agent job's.
const (
	stepSnapshot  = "Take a ddash Snapshot"
	stepRetention = "Delete the older ddash Snapshots"
)

// tracked is a Workload's update as kept: its proto and the Host Agent
// job it waits on.
type tracked struct {
	run *proxmoxpb.UpdateRun
	job string
}

// --- Settings ---

func (c *component) settings(ctx context.Context) (map[uint32]bool, error) {
	rows, err := c.db.QueryContext(ctx, `SELECT vmid, snapshot FROM update_setting ORDER BY vmid`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[uint32]bool{}
	for rows.Next() {
		var vmid uint32
		var snap bool
		if err := rows.Scan(&vmid, &snap); err != nil {
			return nil, err
		}
		out[vmid] = snap
	}
	return out, rows.Err()
}

// snapshotSetting is whether vmid takes a ddash Snapshot before each
// update: yes unless set otherwise.
func snapshotSetting(settings map[uint32]bool, vmid uint32) bool {
	snap, ok := settings[vmid]
	return !ok || snap
}

func (c *component) ListUpdateSettings(ctx context.Context, _ *proxmoxpb.ListUpdateSettingsRequest) (*proxmoxpb.ListUpdateSettingsResponse, error) {
	s, err := c.settings(ctx)
	if err != nil {
		return nil, err
	}
	out := &proxmoxpb.ListUpdateSettingsResponse{}
	for _, vmid := range slices.Sorted(maps.Keys(s)) {
		out.Settings = append(out.Settings, &proxmoxpb.UpdateSettings{Vmid: vmid, Snapshot: s[vmid]})
	}
	return out, nil
}

func (c *component) SetUpdateSettings(ctx context.Context, req *proxmoxpb.SetUpdateSettingsRequest) (*proxmoxpb.SetUpdateSettingsResponse, error) {
	s := req.GetSettings()
	if s.GetVmid() == 0 {
		return nil, status.Error(codes.InvalidArgument, "settings need the Workload's VMID")
	}
	var err error
	if s.GetSnapshot() {
		_, err = c.db.ExecContext(ctx, `DELETE FROM update_setting WHERE vmid = ?`, s.GetVmid())
	} else {
		_, err = c.db.ExecContext(ctx, `INSERT INTO update_setting (vmid, snapshot) VALUES (?, 0) ON CONFLICT (vmid) DO UPDATE SET snapshot = 0`, s.GetVmid())
	}
	if err != nil {
		return nil, err
	}
	return &proxmoxpb.SetUpdateSettingsResponse{}, nil
}

// --- Plans ---

// lab is what a Plan reads: proxmox-api's Workloads and permissions, the
// Host Agents with their packages, and the update settings.
type lab struct {
	workloads []workload
	protected map[uint32]bool
	agents    map[uint32]*hostagentapipb.Agent
	packages  map[uint32]*hostagentapipb.HostPackages
	settings  map[uint32]bool
}

func (c *component) readLab(ctx context.Context) (*lab, error) {
	// Four independent reads of two other Components, made together; the
	// first that fails, in this order, is the error.
	var (
		list   *proxmoxapipb.ListWorkloadsResponse
		perms  *proxmoxapipb.ListPermissionsResponse
		agents *hostagentapipb.ListAgentsResponse
		pkgs   *hostagentapipb.ListPackagesResponse
		errs   [4]error
		reads  sync.WaitGroup
	)
	reads.Go(func() { list, errs[0] = c.api.ListWorkloads(ctx, &proxmoxapipb.ListWorkloadsRequest{}) })
	reads.Go(func() { perms, errs[1] = c.api.ListPermissions(ctx, &proxmoxapipb.ListPermissionsRequest{}) })
	reads.Go(func() { agents, errs[2] = c.agents.ListAgents(ctx, &hostagentapipb.ListAgentsRequest{}) })
	reads.Go(func() { pkgs, errs[3] = c.agents.ListPackages(ctx, &hostagentapipb.ListPackagesRequest{}) })
	reads.Wait()
	for _, err := range errs {
		if err != nil {
			return nil, err
		}
	}
	settings, err := c.settings(ctx)
	if err != nil {
		return nil, err
	}
	l := &lab{protected: map[uint32]bool{}, agents: map[uint32]*hostagentapipb.Agent{}, packages: map[uint32]*hostagentapipb.HostPackages{}, settings: settings}
	for _, w := range list.GetWorkloads() {
		l.workloads = append(l.workloads, workloadOf(w))
		l.protected[w.GetVmid()] = true
	}
	slices.SortFunc(l.workloads, func(a, b workload) int { return cmp.Compare(a.GetVmid(), b.GetVmid()) })
	for _, p := range perms.GetWorkloads() {
		if slices.Contains(p.GetPrivileges(), PowerMgmt) {
			l.protected[p.GetVmid()] = false
		}
	}
	for _, a := range agents.GetAgents() {
		l.agents[a.GetVmid()] = a
	}
	for _, h := range pkgs.GetHosts() {
		l.packages[h.GetVmid()] = h
	}
	return l, nil
}

// skip is why w can't be updated now, or SKIP_REASON_UNSPECIFIED.
func (c *component) skip(l *lab, w workload) (proxmoxpb.SkipReason, string) {
	a := l.agents[w.GetVmid()]
	switch {
	case a == nil:
		return proxmoxpb.SkipReason_SKIP_REASON_NO_HOST_AGENT, "install a Host Agent on its Page"
	case w.GetStatus() != "running":
		return proxmoxpb.SkipReason_SKIP_REASON_STOPPED, "it is " + w.GetStatus()
	case a.GetState() != hostagentapipb.AgentState_AGENT_STATE_PAIRED:
		return proxmoxpb.SkipReason_SKIP_REASON_AGENT_NOT_READY, cmp.Or(a.GetMessage(), "its Host Agent isn't paired yet")
	}
	if what := c.busyWith(w); what != "" {
		return proxmoxpb.SkipReason_SKIP_REASON_BUSY, what + " is running on it"
	}
	return proxmoxpb.SkipReason_SKIP_REASON_UNSPECIFIED, ""
}

// busyWith is what runs on w now, or "".
func (c *component) busyWith(w workload) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if what := c.busy[w.GetVmid()]; what != "" {
		return what
	}
	if t, ok := c.inFlight[w.GetVmid()]; ok {
		return "a " + strings.ToLower(actionNames[t.GetAction()])
	}
	if c.bulk.GetState() == jobpb.State_STATE_RUNNING {
		for _, it := range c.bulk.GetItems() {
			if it.GetWorkload().GetId() == w.ref.GetId() && it.GetState() == jobpb.State_STATE_PENDING {
				return "Update all running"
			}
		}
	}
	return ""
}

var skipText = map[proxmoxpb.SkipReason]string{
	proxmoxpb.SkipReason_SKIP_REASON_NO_HOST_AGENT:   "no Host Agent",
	proxmoxpb.SkipReason_SKIP_REASON_STOPPED:         "it isn't running",
	proxmoxpb.SkipReason_SKIP_REASON_AGENT_NOT_READY: "its Host Agent isn't ready",
	proxmoxpb.SkipReason_SKIP_REASON_BUSY:            "busy",
}

// plan is w's update Plan from l; it reads w's snapshots.
func (c *component) plan(ctx context.Context, l *lab, w workload) *proxmoxpb.UpdatePlan {
	p := &proxmoxpb.UpdatePlan{
		Workload: w.ref, Label: w.label(), Protected: l.protected[w.GetVmid()],
		Snapshot: snapshotSetting(l.settings, w.GetVmid()),
	}
	if reason, detail := c.skip(l, w); reason != proxmoxpb.SkipReason_SKIP_REASON_UNSPECIFIED {
		p.Blocked = skipText[reason] + ": " + detail
	}
	if h := l.packages[w.GetVmid()]; h != nil {
		p.PackagesRead, p.RebootRequired = h.GetRead(), h.GetRebootRequired()
		for _, pkg := range h.GetPackages() {
			p.Packages = append(p.Packages, &proxmoxpb.PackageUpdate{Name: pkg.GetName(), Current: pkg.GetCurrent(), New: pkg.GetNew()})
		}
	}
	snaps, err := c.api.ListSnapshots(ctx, &proxmoxapipb.ListSnapshotsRequest{Workload: w.target()})
	if err != nil {
		if p.Blocked == "" {
			p.Blocked = "its snapshots can't be read: " + status.Convert(err).Message()
		}
		return p
	}
	p.SnapshotSupported = snaps.GetSupported()
	if s := latestDdash(snaps.GetSnapshots()); s != nil {
		p.ReplacesSnapshot = s.GetName()
	}
	return p
}

func (c *component) PlanUpdate(ctx context.Context, req *proxmoxpb.PlanUpdateRequest) (*proxmoxpb.PlanUpdateResponse, error) {
	w, err := c.find(ctx, req.GetWorkload())
	if err != nil {
		return nil, err
	}
	l, err := c.readLab(ctx)
	if err != nil {
		return nil, err
	}
	return &proxmoxpb.PlanUpdateResponse{Plan: c.plan(ctx, l, w)}, nil
}

// planReaders is how many Workloads' Plans are read at once, as
// proxmox-api reads addresses.
const planReaders = 4

// planAll is "Update all running"'s Plan: every Workload that isn't a
// template, in VMID order, as updated or skipped with why.
func (c *component) planAll(ctx context.Context) (*proxmoxpb.PlanUpdateAllResponse, error) {
	l, err := c.readLab(ctx)
	if err != nil {
		return nil, err
	}
	// A Plan reads its Workload's snapshots from Proxmox: those of the
	// Workloads that get one are read planReaders at a time.
	planned := func(w workload) bool {
		r, _ := c.skip(l, w)
		return !w.GetTemplate() && r == proxmoxpb.SkipReason_SKIP_REASON_UNSPECIFIED && !l.protected[w.GetVmid()]
	}
	plans := make([]*proxmoxpb.UpdatePlan, len(l.workloads))
	var readers errgroup.Group
	readers.SetLimit(planReaders)
	for i, w := range l.workloads {
		if planned(w) {
			readers.Go(func() error {
				plans[i] = c.plan(ctx, l, w)
				return nil
			})
		}
	}
	_ = readers.Wait()

	out := &proxmoxpb.PlanUpdateAllResponse{}
	for i, w := range l.workloads {
		if w.GetTemplate() {
			continue
		}
		skipped := func(r proxmoxpb.SkipReason, detail string) {
			out.Skipped = append(out.Skipped, &proxmoxpb.SkippedWorkload{Workload: w.ref, Label: w.label(), Reason: r, Detail: detail})
		}
		if r, detail := c.skip(l, w); r != proxmoxpb.SkipReason_SKIP_REASON_UNSPECIFIED {
			skipped(r, detail)
			continue
		}
		if l.protected[w.GetVmid()] {
			skipped(proxmoxpb.SkipReason_SKIP_REASON_PROTECTED, "ddash holds no "+PowerMgmt+" on it: update it on its own Page")
			continue
		}
		p := plans[i]
		switch {
		case p.GetBlocked() != "":
			skipped(proxmoxpb.SkipReason_SKIP_REASON_AGENT_NOT_READY, p.GetBlocked())
		case p.GetSnapshot() && !p.GetSnapshotSupported():
			skipped(proxmoxpb.SkipReason_SKIP_REASON_SNAPSHOTS_NOT_SUPPORTED, "its storage can't snapshot: turn its snapshot off to include it")
		case len(p.GetPackages()) == 0:
			skipped(proxmoxpb.SkipReason_SKIP_REASON_UP_TO_DATE, "nothing pending")
		default:
			out.Workloads = append(out.Workloads, p)
		}
	}
	return out, nil
}

func (c *component) PlanUpdateAll(ctx context.Context, _ *proxmoxpb.PlanUpdateAllRequest) (*proxmoxpb.PlanUpdateAllResponse, error) {
	return c.planAll(ctx)
}

// --- Runs ---

func (c *component) RunUpdate(ctx context.Context, req *proxmoxpb.RunUpdateRequest) (*proxmoxpb.RunUpdateResponse, error) {
	w, err := c.find(ctx, req.GetWorkload())
	if err != nil {
		return nil, err
	}
	l, err := c.readLab(ctx)
	if err != nil {
		return nil, err
	}
	p := c.plan(ctx, l, w)
	snapshot := p.GetSnapshot()
	if req.Snapshot != nil {
		snapshot = req.GetSnapshot()
	}
	switch {
	case p.GetBlocked() != "":
		return nil, status.Errorf(codes.FailedPrecondition, "%s can't be updated now: %s", w.label(), p.GetBlocked())
	case p.GetProtected() && !req.GetOverrideProtected():
		return nil, status.Errorf(codes.PermissionDenied, "%s is protected in Proxmox: ddash doesn't hold %s on it; confirm the override to update it anyway", w.label(), PowerMgmt)
	case snapshot && !p.GetSnapshotSupported():
		return nil, status.Errorf(codes.FailedPrecondition, "snapshots not supported on %s: turn the snapshot off to update it without one", w.label())
	}
	run, err := c.begin(w, snapshot, "")
	if err != nil {
		return nil, err
	}
	bg, done := c.background(ctx, UpdateAtMost)
	go func() {
		defer done()
		err := c.execute(bg, w, run)
		c.notifyRun(bg, w, err)
	}()
	return &proxmoxpb.RunUpdateResponse{Run: run}, nil
}

// begin claims w for an update and records its run; it returns a copy.
func (c *component) begin(w workload, snapshot bool, bulk string) (*proxmoxpb.UpdateRun, error) {
	if err := c.occupy(w, "an update"); err != nil {
		return nil, err
	}
	run := &proxmoxpb.UpdateRun{
		Id: fmt.Sprintf("%d-%d", w.GetVmid(), time.Now().UnixMilli()), Workload: w.ref, Label: w.label(),
		State: jobpb.State_STATE_RUNNING, Started: timestamppb.Now(), BulkId: bulk,
		Steps: []*jobpb.Step{{Title: stepSnapshot, State: jobpb.State_STATE_PENDING}, {Title: stepRetention, State: jobpb.State_STATE_PENDING}},
	}
	if !snapshot {
		run.Steps[0].State, run.Steps[0].Detail = jobpb.State_STATE_SKIPPED, "turned off"
		run.Steps[1].State = jobpb.State_STATE_SKIPPED
	}
	c.mu.Lock()
	c.runs[w.GetVmid()] = &tracked{run: run}
	out := proto.CloneOf(run)
	c.mu.Unlock()
	c.persist(w.GetVmid())
	return out, nil
}

// change applies f to vmid's run and keeps it.
func (c *component) change(vmid uint32, f func(r *proxmoxpb.UpdateRun, t *tracked)) {
	c.mu.Lock()
	t := c.runs[vmid]
	if t != nil {
		f(t.run, t)
	}
	c.mu.Unlock()
	c.persist(vmid)
}

func (c *component) persist(vmid uint32) {
	c.mu.Lock()
	t := c.runs[vmid]
	var b []byte
	var job string
	if t != nil {
		b, _ = protojson.Marshal(t.run)
		job = t.job
	}
	c.mu.Unlock()
	if t == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), callTimeout)
	defer cancel()
	if _, err := c.db.ExecContext(ctx, `INSERT INTO update_run (vmid, run, job) VALUES (?, ?, ?) ON CONFLICT (vmid) DO UPDATE SET run = excluded.run, job = excluded.job`, vmid, string(b), job); err != nil {
		c.log.Warn("can't keep an update's progress", "vmid", vmid, "err", err)
	}
}

// execute runs w's update, its run already begun: Maintenance, the ddash
// Snapshot and its retention, the Host Agent's package update. It never
// reboots. It frees w and ends the run however it ends.
func (c *component) execute(ctx context.Context, w workload, run *proxmoxpb.UpdateRun) (err error) {
	vmid := w.GetVmid()
	defer c.release(w)
	defer func() { c.end(ctx, w, err) }()
	c.maintain(ctx, w, nil, false)

	if run.GetSteps()[0].GetState() != jobpb.State_STATE_SKIPPED {
		c.change(vmid, func(r *proxmoxpb.UpdateRun, _ *tracked) { r.Steps[0].State = jobpb.State_STATE_RUNNING })
		snap, deleted, err := c.snapshot(ctx, w, "an update")
		var kept retentionError
		switch {
		case errors.As(err, &kept):
			c.change(vmid, func(r *proxmoxpb.UpdateRun, _ *tracked) {
				r.Snapshot = snap.GetName()
				r.Steps[0].State, r.Steps[0].Detail = jobpb.State_STATE_DONE, snap.GetName()
				r.Steps[1].State, r.Steps[1].Detail = jobpb.State_STATE_FAILED, kept.Error()
			})
		case err != nil:
			c.change(vmid, func(r *proxmoxpb.UpdateRun, _ *tracked) {
				r.Steps[0].State, r.Steps[0].Detail = jobpb.State_STATE_FAILED, status.Convert(err).Message()
				r.Steps[1].State, r.Steps[1].Detail = jobpb.State_STATE_SKIPPED, "nothing deleted"
			})
			return fmt.Errorf("the ddash Snapshot failed, so nothing was updated: %s", status.Convert(err).Message())
		default:
			detail := "none older"
			if len(deleted) > 0 {
				detail = strings.Join(deleted, ", ")
			}
			c.change(vmid, func(r *proxmoxpb.UpdateRun, _ *tracked) {
				r.Snapshot = snap.GetName()
				r.Steps[0].State, r.Steps[0].Detail = jobpb.State_STATE_DONE, snap.GetName()
				r.Steps[1].State, r.Steps[1].Detail = jobpb.State_STATE_DONE, detail
			})
		}
	}

	callCtx, cancel := context.WithTimeout(ctx, callTimeout)
	started, err := c.agents.UpgradePackages(callCtx, &hostagentapipb.UpgradePackagesRequest{Vmid: vmid})
	cancel()
	if err != nil {
		return fmt.Errorf("the Host Agent didn't start the update: %s", status.Convert(err).Message())
	}
	c.change(vmid, func(r *proxmoxpb.UpdateRun, t *tracked) {
		t.job = started.GetJob().GetId()
		r.Steps = append(r.Steps[:2], started.GetJob().GetSteps()...)
	})
	return c.collect(ctx, vmid, started.GetJob().GetId())
}

// collect follows vmid's Host Agent job into its run until it ended, then
// collects it: in ctx's Audit Log chain, failing as the job failed.
func (c *component) collect(ctx context.Context, vmid uint32, id string) error {
	job, err := c.followJob(ctx, vmid, id, func(j *jobpb.Job) {
		c.change(vmid, func(r *proxmoxpb.UpdateRun, _ *tracked) {
			if len(r.Steps) >= 2 {
				r.Steps = append(r.Steps[:2], j.GetSteps()...)
			}
		})
	})
	if job != nil {
		c.change(vmid, func(r *proxmoxpb.UpdateRun, _ *tracked) {
			n, _ := strconv.Atoi(job.GetResult()["updated"])
			r.Updated = uint32(n)
			r.RebootRequired = job.GetResult()["reboot_required"] == "true"
		})
	}
	return err
}

// followJob reads vmid's job id every JobPollInterval, telling progress,
// until it ended; then it collects it. It returns the job as last read.
func (c *component) followJob(ctx context.Context, vmid uint32, id string, progress func(*jobpb.Job)) (*jobpb.Job, error) {
	var last *jobpb.Job
	for {
		select {
		case <-ctx.Done():
			return last, errors.New("ddash stopped waiting for the Host Agent's job; it may still be running there")
		case <-time.After(JobPollInterval):
		}
		callCtx, cancel := context.WithTimeout(ctx, callTimeout)
		res, err := c.agents.GetJob(callCtx, &hostagentapipb.GetJobRequest{Vmid: vmid, Id: id})
		cancel()
		if status.Code(err) == codes.NotFound {
			return last, errors.New("the Host Agent no longer has the job: it restarted while the update ran")
		}
		if err != nil {
			continue // unreachable for now: the job goes on there
		}
		job := res.GetJob()
		last = job
		if progress != nil {
			progress(job)
		}
		if job.GetEnd() == nil {
			continue
		}
		callCtx, cancel = context.WithTimeout(ctx, callTimeout)
		_, err = c.agents.CollectJob(callCtx, &hostagentapipb.CollectJobRequest{Vmid: vmid, Id: id})
		cancel()
		if err != nil {
			return last, errors.New(status.Convert(err).Message())
		}
		return last, nil
	}
}

// end ends w's run as err says and ends its Maintenance once its Checks
// are up (at most UpdateMaintenance from now).
func (c *component) end(ctx context.Context, w workload, err error) {
	c.change(w.GetVmid(), func(r *proxmoxpb.UpdateRun, t *tracked) {
		r.Ended, t.job = timestamppb.Now(), ""
		if err != nil {
			r.State, r.Error = jobpb.State_STATE_FAILED, err.Error()
			for _, s := range r.Steps {
				if s.GetState() == jobpb.State_STATE_RUNNING {
					s.State = jobpb.State_STATE_FAILED
				}
			}
			return
		}
		r.State = jobpb.State_STATE_DONE
	})
	callCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), callTimeout)
	defer cancel()
	c.maintain(callCtx, w, durationpb.New(UpdateMaintenance), true)
}

// notifyRun tells the Operator how a single update ended.
func (c *component) notifyRun(ctx context.Context, w workload, err error) {
	if err != nil {
		c.send(ctx, false, w, "Update of "+w.label()+" failed", err.Error())
		return
	}
	run := c.runOf(w.GetVmid())
	msg := fmt.Sprintf("%d packages updated", run.GetUpdated())
	if run.GetRebootRequired() {
		msg += "; reboot required: restart it when it suits"
	}
	c.send(ctx, true, w, "Updated "+w.label(), msg)
}

func (c *component) runOf(vmid uint32) *proxmoxpb.UpdateRun {
	c.mu.Lock()
	defer c.mu.Unlock()
	if t := c.runs[vmid]; t != nil {
		return proto.CloneOf(t.run)
	}
	return nil
}

func (c *component) RunUpdateAll(ctx context.Context, req *proxmoxpb.RunUpdateAllRequest) (*proxmoxpb.RunUpdateAllResponse, error) {
	c.mu.Lock()
	if c.bulk != nil && c.bulk.GetState() == jobpb.State_STATE_RUNNING {
		c.mu.Unlock()
		return nil, status.Error(codes.FailedPrecondition, "Update all running is already running")
	}
	c.mu.Unlock()
	p, err := c.planAll(ctx)
	if err != nil {
		return nil, err
	}
	var planned, asked []string
	for _, w := range p.GetWorkloads() {
		planned = append(planned, w.GetWorkload().GetId())
	}
	for _, w := range req.GetWorkloads() {
		asked = append(asked, w.GetId())
	}
	if len(asked) == 0 {
		return nil, status.Error(codes.FailedPrecondition, "the Plan lists no Workload to update")
	}
	if !slices.Equal(planned, asked) {
		return nil, status.Errorf(codes.Aborted, "the Plan changed since it was read (now %s): open it again", cmp.Or(strings.Join(planned, ", "), "none"))
	}
	bulk := &proxmoxpb.BulkUpdate{
		Id: fmt.Sprintf("all-%d", time.Now().UnixMilli()), State: jobpb.State_STATE_RUNNING,
		Started: timestamppb.Now(), Skipped: p.GetSkipped(),
	}
	for _, w := range p.GetWorkloads() {
		bulk.Items = append(bulk.Items, &proxmoxpb.BulkItem{Workload: w.GetWorkload(), Label: w.GetLabel(), State: jobpb.State_STATE_PENDING})
	}
	c.mu.Lock()
	c.bulk = bulk
	out := proto.CloneOf(bulk)
	c.mu.Unlock()
	c.persistBulk()
	bg, done := c.background(ctx, time.Duration(len(bulk.GetItems()))*UpdateAtMost)
	go func() {
		defer done()
		c.runAll(bg, p.GetWorkloads())
	}()
	return &proxmoxpb.RunUpdateAllResponse{Bulk: out}, nil
}

// runAll updates each planned Workload in turn, stopping at the first
// failure, and notifies once at the end.
func (c *component) runAll(ctx context.Context, plans []*proxmoxpb.UpdatePlan) {
	var failed error
	var failedAt string
	updated, reboot := 0, 0
	for i, p := range plans {
		item := func(f func(b *proxmoxpb.BulkItem)) {
			c.mu.Lock()
			f(c.bulk.GetItems()[i])
			c.mu.Unlock()
			c.persistBulk()
		}
		if failed != nil {
			item(func(b *proxmoxpb.BulkItem) { b.State = jobpb.State_STATE_SKIPPED })
			continue
		}
		w, err := c.find(ctx, p.GetWorkload())
		var run *proxmoxpb.UpdateRun
		if err == nil {
			run, err = c.begin(w, p.GetSnapshot(), c.bulkID())
		}
		if err != nil {
			failed, failedAt = err, p.GetLabel()
			item(func(b *proxmoxpb.BulkItem) { b.State = jobpb.State_STATE_FAILED })
			continue
		}
		item(func(b *proxmoxpb.BulkItem) { b.State, b.RunId = jobpb.State_STATE_RUNNING, run.GetId() })
		if err := c.execute(ctx, w, run); err != nil {
			failed, failedAt = err, p.GetLabel()
			item(func(b *proxmoxpb.BulkItem) { b.State = jobpb.State_STATE_FAILED })
			continue
		}
		updated++
		if c.runOf(w.GetVmid()).GetRebootRequired() {
			reboot++
		}
		item(func(b *proxmoxpb.BulkItem) { b.State = jobpb.State_STATE_DONE })
	}
	c.mu.Lock()
	c.bulk.Ended = timestamppb.Now()
	if failed != nil {
		c.bulk.State, c.bulk.Error = jobpb.State_STATE_FAILED, fmt.Sprintf("stopped at %s: %v", failedAt, status.Convert(failed).Message())
	} else {
		c.bulk.State = jobpb.State_STATE_DONE
	}
	c.mu.Unlock()
	c.persistBulk()

	msg := fmt.Sprintf("%d of %d Workloads updated", updated, len(plans))
	if reboot > 0 {
		msg += fmt.Sprintf("; %d need a reboot", reboot)
	}
	if failed != nil {
		c.sendTo(ctx, false, "Update all running stopped", fmt.Sprintf("%s: stopped at %s: %v", msg, failedAt, status.Convert(failed).Message()), "/proxmox")
		return
	}
	c.sendTo(ctx, true, "Updated every running Workload", msg, "/proxmox")
}

func (c *component) bulkID() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.bulk.GetId()
}

func (c *component) persistBulk() {
	c.mu.Lock()
	b, _ := protojson.Marshal(c.bulk)
	c.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), callTimeout)
	defer cancel()
	if _, err := c.db.ExecContext(ctx, `INSERT INTO bulk_update (id, bulk) VALUES (1, ?) ON CONFLICT (id) DO UPDATE SET bulk = excluded.bulk`, string(b)); err != nil {
		c.log.Warn("can't keep Update all running's progress", "err", err)
	}
}

func (c *component) ListUpdates(context.Context, *proxmoxpb.ListUpdatesRequest) (*proxmoxpb.ListUpdatesResponse, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := &proxmoxpb.ListUpdatesResponse{}
	for _, vmid := range slices.Sorted(maps.Keys(c.runs)) {
		out.Runs = append(out.Runs, proto.CloneOf(c.runs[vmid].run))
	}
	if c.bulk != nil {
		out.Bulk = proto.CloneOf(c.bulk)
	}
	return out, nil
}

// interrupted is why a run that was running when ddash stopped ended.
const interrupted = "ddash restarted while it ran"

// loadRuns reads the kept runs back. A run whose Host Agent job was
// running is followed again and collected, in a chain of its own; one
// that was elsewhere (its snapshot) ended with ddash.
func (c *component) loadRuns(ctx context.Context) error {
	rows, err := c.db.QueryContext(ctx, `SELECT vmid, run, job FROM update_run`)
	if err != nil {
		return err
	}
	defer rows.Close()
	var resume []uint32
	for rows.Next() {
		var vmid uint32
		var text, job string
		if err := rows.Scan(&vmid, &text, &job); err != nil {
			return err
		}
		run := &proxmoxpb.UpdateRun{}
		if err := protojson.Unmarshal([]byte(text), run); err != nil {
			continue
		}
		c.runs[vmid] = &tracked{run: run, job: job}
		if run.GetState() != jobpb.State_STATE_RUNNING {
			continue
		}
		if job != "" {
			c.busy[vmid] = "an update"
			resume = append(resume, vmid)
			continue
		}
		run.State, run.Error, run.Ended = jobpb.State_STATE_FAILED, interrupted, timestamppb.Now()
	}
	if err := rows.Err(); err != nil {
		return err
	}
	var bulk string
	if err := c.db.QueryRowContext(ctx, `SELECT bulk FROM bulk_update WHERE id = 1`).Scan(&bulk); err == nil {
		b := &proxmoxpb.BulkUpdate{}
		if protojson.Unmarshal([]byte(bulk), b) == nil {
			if b.GetState() == jobpb.State_STATE_RUNNING {
				b.State, b.Error, b.Ended = jobpb.State_STATE_FAILED, "stopped: "+interrupted, timestamppb.Now()
			}
			c.bulk = b
		}
	}
	for _, vmid := range resume {
		go c.resume(vmid)
	}
	return nil
}

// resume follows a run's Host Agent job after a restart and collects it.
func (c *component) resume(vmid uint32) {
	t := c.runOf(vmid)
	c.mu.Lock()
	job := c.runs[vmid].job
	c.mu.Unlock()
	ctx, cancel := context.WithTimeout(c.life, UpdateAtMost)
	defer cancel()
	w := workload{Workload: &proxmoxapipb.Workload{Vmid: vmid, Name: t.GetLabel()}, ref: t.GetWorkload()}
	if l, err := c.find(ctx, t.GetWorkload()); err == nil {
		w = l
	}
	err := c.collect(ctx, vmid, job)
	c.release(w)
	c.end(ctx, w, err)
	c.notifyRun(ctx, w, err)
}
