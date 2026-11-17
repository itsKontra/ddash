package proxmox

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	notificationpb "github.com/itsKontra/ddash/proto/ddash/notification"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/itsKontra/ddash/components/proxmox-api/proxmoxapipb"
	"github.com/itsKontra/ddash/components/proxmox/proxmoxpb"
	jobpb "github.com/itsKontra/ddash/proto/ddash/job"
)

// SnapshotPrefix starts every ddash Snapshot's name: retention deletes
// only snapshots named so.
const SnapshotPrefix = "ddash-"

// snapshotTime is the UTC time in a ddash Snapshot's name.
const snapshotTime = "20060102-150405"

// BackupAtMost is how long a backup is followed before ddash stops
// waiting for it (Proxmox carries on).
const BackupAtMost = 12 * time.Hour

// isDdash reports whether a snapshot is a ddash Snapshot.
func isDdash(name string) bool { return strings.HasPrefix(name, SnapshotPrefix) }

func snapshotProto(s *proxmoxapipb.Snapshot) *proxmoxpb.Snapshot {
	return &proxmoxpb.Snapshot{Name: s.GetName(), Description: s.GetDescription(), Created: s.GetCreated()}
}

// latestDdash is the newest ddash Snapshot of snaps (oldest first), or nil.
func latestDdash(snaps []*proxmoxapipb.Snapshot) *proxmoxapipb.Snapshot {
	for _, s := range slices.Backward(snaps) {
		if isDdash(s.GetName()) {
			return s
		}
	}
	return nil
}

func (c *component) GetSnapshots(ctx context.Context, req *proxmoxpb.GetSnapshotsRequest) (*proxmoxpb.GetSnapshotsResponse, error) {
	w, err := c.find(ctx, req.GetWorkload())
	if err != nil {
		return nil, err
	}
	list, err := c.api.ListSnapshots(ctx, &proxmoxapipb.ListSnapshotsRequest{Workload: w.target()})
	if err != nil {
		return nil, err
	}
	out := &proxmoxpb.GetSnapshotsResponse{Supported: list.GetSupported()}
	if s := latestDdash(list.GetSnapshots()); s != nil {
		out.DdashSnapshot = snapshotProto(s)
	}
	for _, s := range list.GetSnapshots() {
		if !isDdash(s.GetName()) {
			out.Others = append(out.Others, snapshotProto(s))
		}
	}
	return out, nil
}

func (c *component) TakeSnapshot(ctx context.Context, req *proxmoxpb.TakeSnapshotRequest) (*proxmoxpb.TakeSnapshotResponse, error) {
	w, err := c.find(ctx, req.GetWorkload())
	if err != nil {
		return nil, err
	}
	if err := c.occupy(w, "a snapshot"); err != nil {
		return nil, err
	}
	defer c.release(w)
	snap, deleted, err := c.snapshot(ctx, w, "a change by hand")
	var kept retentionError
	if errors.As(err, &kept) {
		return nil, status.Errorf(codes.Aborted, "took the ddash Snapshot %s, but %s", snap.GetName(), kept)
	}
	if err != nil {
		return nil, err
	}
	return &proxmoxpb.TakeSnapshotResponse{Snapshot: snap, Deleted: deleted}, nil
}

// occupy marks w busy with what, refusing while anything else runs on it.
func (c *component) occupy(w workload, what string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if other := c.busy[w.GetVmid()]; other != "" {
		return status.Errorf(codes.FailedPrecondition, "%s is running on %s: wait for it to end", other, w.label())
	}
	if t, ok := c.inFlight[w.GetVmid()]; ok {
		return status.Errorf(codes.FailedPrecondition, "A %s is running on %s: wait for it to end", actionNames[t.GetAction()], w.label())
	}
	c.busy[w.GetVmid()] = what
	return nil
}

func (c *component) release(w workload) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.busy, w.GetVmid())
}

// retentionError is a ddash Snapshot that was taken while an older one
// couldn't be deleted.
type retentionError struct{ err error }

func (e retentionError) Error() string { return e.err.Error() }

// snapshot takes a ddash Snapshot of w, "Created by ddash before
// <reason>", and once its task succeeded deletes every older ddash
// Snapshot. When taking it fails nothing is deleted; when a delete fails
// the new snapshot is returned with a retentionError naming it.
func (c *component) snapshot(ctx context.Context, w workload, reason string) (*proxmoxpb.Snapshot, []string, error) {
	name := SnapshotPrefix + time.Now().UTC().Format(snapshotTime)
	desc := "Created by ddash before " + reason
	_, err := c.api.CreateSnapshot(ctx, &proxmoxapipb.CreateSnapshotRequest{Workload: w.target(), Name: name, Description: desc})
	if code := status.Code(err); code == codes.DeadlineExceeded || code == codes.Canceled {
		// Proxmox carries on: wait for the task, then look for the
		// snapshot it should have made.
		err = c.waitTasks(ctx, w)
	}
	if err != nil {
		return nil, nil, err
	}
	list, err := c.api.ListSnapshots(ctx, &proxmoxapipb.ListSnapshotsRequest{Workload: w.target()})
	if err != nil {
		return nil, nil, status.Errorf(codes.Unavailable, "the ddash Snapshot %s was taken, but the snapshots can't be read, so no older one was deleted: %s", name, status.Convert(err).Message())
	}
	var taken *proxmoxapipb.Snapshot
	for _, s := range list.GetSnapshots() {
		if s.GetName() == name {
			taken = s
		}
	}
	if taken == nil {
		return nil, nil, status.Errorf(codes.FailedPrecondition, "Proxmox's task ended, but %s lists no snapshot %s: nothing was deleted", w.label(), name)
	}
	snap := snapshotProto(taken)
	if snap.Created == nil {
		snap.Created = timestamppb.Now()
	}
	var deleted []string
	for _, s := range list.GetSnapshots() {
		if !isDdash(s.GetName()) || s.GetName() == name {
			continue
		}
		if _, err := c.api.DeleteSnapshot(ctx, &proxmoxapipb.DeleteSnapshotRequest{Workload: w.target(), Name: s.GetName()}); err != nil {
			return snap, deleted, retentionError{fmt.Errorf("the older ddash Snapshot %s couldn't be deleted: %s", s.GetName(), status.Convert(err).Message())}
		}
		deleted = append(deleted, s.GetName())
	}
	return snap, deleted, nil
}

// waitTasks waits until Proxmox runs no task ddash started on w.
func (c *component) waitTasks(ctx context.Context, w workload) error {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), FollowAtMost)
	defer cancel()
	stop := context.AfterFunc(c.life, cancel)
	defer stop()
	for c.taskRunning(ctx, w) {
		select {
		case <-ctx.Done():
			return status.Errorf(codes.DeadlineExceeded, "Proxmox still runs a task on %s", w.label())
		case <-time.After(FollowInterval):
		}
	}
	return nil
}

func (c *component) Backup(ctx context.Context, req *proxmoxpb.BackupRequest) (*proxmoxpb.BackupResponse, error) {
	w, err := c.find(ctx, req.GetWorkload())
	if err != nil {
		return nil, err
	}
	if req.GetStorage() == "" {
		return nil, status.Error(codes.InvalidArgument, "pick a storage for the backup")
	}
	c.mu.Lock()
	for _, b := range c.backups {
		if b.GetWorkload().GetId() == w.ref.GetId() && b.GetState() == jobpb.State_STATE_RUNNING {
			c.mu.Unlock()
			return nil, status.Errorf(codes.FailedPrecondition, "a backup of %s is already running", w.label())
		}
	}
	c.mu.Unlock()
	res, err := c.api.Backup(ctx, &proxmoxapipb.BackupRequest{Workload: w.target(), Storage: req.GetStorage(), Mode: proxmoxapipb.BackupMode_BACKUP_MODE_SNAPSHOT})
	if err != nil {
		return nil, err
	}
	b := &proxmoxpb.BackupTask{
		Workload: w.ref, Label: w.label(), Storage: req.GetStorage(), Upid: res.GetUpid(),
		State: jobpb.State_STATE_RUNNING, Started: timestamppb.Now(),
	}
	c.mu.Lock()
	c.backups = append([]*proxmoxpb.BackupTask{b}, c.backups...)
	if len(c.backups) > 50 {
		c.backups = c.backups[:50]
	}
	out := cloneBackup(b)
	c.mu.Unlock()
	bg, done := c.background(ctx, BackupAtMost)
	go c.followBackup(bg, done, w, b)
	return &proxmoxpb.BackupResponse{Backup: out}, nil
}

func cloneBackup(b *proxmoxpb.BackupTask) *proxmoxpb.BackupTask {
	return &proxmoxpb.BackupTask{
		Workload: b.GetWorkload(), Label: b.GetLabel(), Storage: b.GetStorage(), Upid: b.GetUpid(),
		State: b.GetState(), Started: b.GetStarted(), Ended: b.GetEnded(), Error: b.GetError(),
	}
}

// background is the call's chain and actor without its cancellation,
// ending after at most or when the Component stops; call done once
// finished with it.
func (c *component) background(callCtx context.Context, at time.Duration) (ctx context.Context, done func()) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(callCtx), at)
	stop := context.AfterFunc(c.life, cancel)
	return ctx, func() { stop(); cancel() }
}

// followBackup reads the backup's task until it ended, then notifies.
func (c *component) followBackup(ctx context.Context, done func(), w workload, b *proxmoxpb.BackupTask) {
	defer done()
	for {
		select {
		case <-ctx.Done():
			c.endBackup(b, jobpb.State_STATE_FAILED, "ddash stopped following it; Proxmox may still be running it")
			return
		case <-time.After(FollowInterval):
		}
		callCtx, cancel := context.WithTimeout(ctx, callTimeout)
		res, err := c.api.ListTasks(callCtx, &proxmoxapipb.ListTasksRequest{Node: w.GetNode(), Vmid: w.GetVmid()})
		cancel()
		if err != nil {
			continue
		}
		for _, t := range res.GetTasks() {
			if t.GetUpid() != b.GetUpid() || !t.GetStopped() {
				continue
			}
			if t.GetExitStatus() == "OK" {
				c.endBackup(b, jobpb.State_STATE_DONE, "")
				c.send(ctx, true, w, "Backed up "+w.label(), "to "+b.GetStorage())
			} else {
				c.endBackup(b, jobpb.State_STATE_FAILED, t.GetExitStatus())
				c.send(ctx, false, w, "Backup of "+w.label()+" failed", t.GetExitStatus())
			}
			return
		}
	}
}

func (c *component) endBackup(b *proxmoxpb.BackupTask, state jobpb.State, why string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	b.State, b.Error, b.Ended = state, why, timestamppb.Now()
}

func (c *component) ListBackups(context.Context, *proxmoxpb.ListBackupsRequest) (*proxmoxpb.ListBackupsResponse, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := &proxmoxpb.ListBackupsResponse{}
	for _, b := range c.backups {
		out.Backups = append(out.Backups, cloneBackup(b))
	}
	return out, nil
}

// send notifies the Operator of an Action's outcome on w, opening its
// Page; a failure is only logged.
func (c *component) send(ctx context.Context, ok bool, w workload, title, message string) {
	c.sendTo(ctx, ok, title, message, fmt.Sprintf("/proxmox/workloads/%d", w.GetVmid()))
}

// sendTo notifies the Operator of an outcome, opening ddash's path.
func (c *component) sendTo(ctx context.Context, ok bool, title, message, path string) {
	n := &notificationpb.Notification{
		Title: title, Message: message,
		Tags: []string{"white_check_mark"},
	}
	if !ok {
		n.Priority, n.Tags = notificationpb.Priority_PRIORITY_HIGH, []string{"warning"}
	}
	if c.publicURL != "" {
		n.Click = c.publicURL + path
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), callTimeout)
	defer cancel()
	if _, err := c.notify.Send(ctx, &notificationpb.SendRequest{Notification: n, Category: notificationpb.Category_CATEGORY_ACTION_OUTCOMES}); err != nil {
		c.log.Warn("can't send a Notification", "title", title, "err", err)
	}
}
