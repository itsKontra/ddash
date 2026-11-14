package proxmoxapi

import (
	"cmp"
	"context"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"

	proxmox "github.com/luthermonson/go-proxmox"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	pb "github.com/itsKontra/ddash/components/proxmox-api/proxmoxapipb"
)

// snapName is Proxmox's pve-snapshot-name.
var snapName = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9_-]{0,39}$`)

// noSnapshotFeature is how a snapshot task fails on storage that can't
// take one (LVM, a raw file on a dir storage): "snapshot feature is not
// available".
const noSnapshotFeature = "snapshot feature is not available"

// workloadPath is /nodes/{node}/{lxc|qemu}/{vmid}.
func workloadPath(t *pb.Target) (path, label string, err error) {
	kind, label, err := kindOf(t)
	if err != nil {
		return "", "", err
	}
	return fmt.Sprintf("/nodes/%s/%s/%d", url.PathEscape(t.GetNode()), kind, t.GetVmid()), label, nil
}

func (c *component) ListSnapshots(ctx context.Context, req *pb.ListSnapshotsRequest) (*pb.ListSnapshotsResponse, error) {
	path, _, err := workloadPath(req.GetWorkload())
	if err != nil {
		return nil, err
	}
	var snaps []struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		SnapTime    int64  `json:"snaptime"`
		Parent      string `json:"parent"`
	}
	if err := c.client.Get(ctx, path+"/snapshot", &snaps); err != nil {
		return nil, unavailable(err)
	}
	var feature struct {
		HasFeature proxmox.IntOrBool `json:"hasFeature"`
	}
	if err := c.client.Get(ctx, path+"/feature?feature=snapshot", &feature); err != nil {
		return nil, unavailable(err)
	}
	out := &pb.ListSnapshotsResponse{Supported: bool(feature.HasFeature)}
	for _, s := range snaps {
		// "current" is Proxmox's "You are here!", not a snapshot.
		if s.Name == "current" {
			continue
		}
		snap := &pb.Snapshot{Name: s.Name, Description: strings.TrimSpace(s.Description), Parent: s.Parent}
		if s.SnapTime > 0 {
			snap.Created = timestamppb.New(time.Unix(s.SnapTime, 0))
		}
		out.Snapshots = append(out.Snapshots, snap)
	}
	slices.SortStableFunc(out.Snapshots, func(a, b *pb.Snapshot) int {
		return cmp.Compare(a.GetCreated().AsTime().Unix(), b.GetCreated().AsTime().Unix())
	})
	return out, nil
}

func (c *component) CreateSnapshot(ctx context.Context, req *pb.CreateSnapshotRequest) (*pb.CreateSnapshotResponse, error) {
	path, label, err := workloadPath(req.GetWorkload())
	if err != nil {
		return nil, err
	}
	if !snapName.MatchString(req.GetName()) {
		return nil, status.Error(codes.InvalidArgument, "a snapshot's name is letters, digits, - and _, starting with a letter, at most 40")
	}
	params := map[string]any{"snapname": req.GetName()}
	if d := req.GetDescription(); d != "" {
		params["description"] = d
	}
	upid, err := c.task(ctx, req.GetWorkload(), path+"/snapshot", params, "VM.Snapshot", "snapshot "+label)
	if err != nil {
		return nil, snapshotErr(err)
	}
	return &pb.CreateSnapshotResponse{Upid: upid}, nil
}

func (c *component) DeleteSnapshot(ctx context.Context, req *pb.DeleteSnapshotRequest) (*pb.DeleteSnapshotResponse, error) {
	path, label, err := workloadPath(req.GetWorkload())
	if err != nil {
		return nil, err
	}
	if !snapName.MatchString(req.GetName()) {
		return nil, status.Error(codes.InvalidArgument, "no such snapshot name")
	}
	var upid string
	if err := c.client.Delete(ctx, path+"/snapshot/"+url.PathEscape(req.GetName()), &upid); err != nil {
		return nil, refused(ctx, err, "delete snapshot "+req.GetName()+" of "+label, "VM.Snapshot")
	}
	if err := c.follow(ctx, req.GetWorkload().GetNode(), upid); err != nil {
		return nil, err
	}
	return &pb.DeleteSnapshotResponse{Upid: upid}, nil
}

// task POSTs params to path, then follows the task Proxmox starts until
// it ends, returning its UPID.
func (c *component) task(ctx context.Context, t *pb.Target, path string, params map[string]any, priv, what string) (string, error) {
	var upid string
	if err := c.client.Post(ctx, path, params, &upid); err != nil {
		return "", refused(ctx, err, what, priv)
	}
	return upid, c.follow(ctx, t.GetNode(), upid)
}

// refused is a write Proxmox refused as a gRPC error.
func refused(ctx context.Context, err error, what, priv string) error {
	if ctx.Err() != nil {
		return status.FromContextError(ctx.Err()).Err()
	}
	if proxmox.IsNotAuthorized(err) {
		return status.Errorf(codes.PermissionDenied, "Proxmox refused to %s: ddash doesn't hold %s on it", what, priv)
	}
	if strings.Contains(err.Error(), noSnapshotFeature) {
		return status.Errorf(codes.FailedPrecondition, "snapshots not supported: %v", err)
	}
	return status.Errorf(codes.Unavailable, "Proxmox refused to %s: %v", what, err)
}

// snapshotErr names a snapshot task that failed for lack of the storage
// feature.
func snapshotErr(err error) error {
	if s := status.Convert(err); s.Code() == codes.FailedPrecondition && strings.Contains(s.Message(), noSnapshotFeature) && !strings.HasPrefix(s.Message(), "snapshots not supported") {
		return status.Errorf(codes.FailedPrecondition, "snapshots not supported: %s", s.Message())
	}
	return err
}

var backupModes = map[pb.BackupMode]string{
	pb.BackupMode_BACKUP_MODE_UNSPECIFIED: "snapshot",
	pb.BackupMode_BACKUP_MODE_SNAPSHOT:    "snapshot",
	pb.BackupMode_BACKUP_MODE_SUSPEND:     "suspend",
	pb.BackupMode_BACKUP_MODE_STOP:        "stop",
}

func (c *component) Backup(ctx context.Context, req *pb.BackupRequest) (*pb.BackupResponse, error) {
	_, label, err := workloadPath(req.GetWorkload())
	if err != nil {
		return nil, err
	}
	if req.GetStorage() == "" {
		return nil, status.Error(codes.InvalidArgument, "storage is required")
	}
	params := map[string]any{
		"vmid": req.GetWorkload().GetVmid(), "storage": req.GetStorage(),
		"mode": backupModes[req.GetMode()], "compress": "zstd",
	}
	var upid string
	if err := c.client.Post(ctx, fmt.Sprintf("/nodes/%s/vzdump", url.PathEscape(req.GetWorkload().GetNode())), params, &upid); err != nil {
		return nil, refused(ctx, err, "back up "+label+" to "+req.GetStorage(), "VM.Backup and Datastore.AllocateSpace")
	}
	return &pb.BackupResponse{Upid: upid}, nil
}

// ListTasks reads the node's recent tasks on the Workload, running and
// archived: an ended task's status is its exit status.
func (c *component) ListTasks(ctx context.Context, req *pb.ListTasksRequest) (*pb.ListTasksResponse, error) {
	if req.GetNode() == "" || req.GetVmid() == 0 {
		return nil, status.Error(codes.InvalidArgument, "node and vmid are required")
	}
	var tasks []struct {
		UPID      string `json:"upid"`
		Type      string `json:"type"`
		Status    string `json:"status"`
		StartTime int64  `json:"starttime"`
		EndTime   int64  `json:"endtime"`
	}
	path := fmt.Sprintf("/nodes/%s/tasks?source=all&limit=50&vmid=%d", url.PathEscape(req.GetNode()), req.GetVmid())
	if err := c.client.Get(ctx, path, &tasks); err != nil {
		return nil, unavailable(err)
	}
	out := &pb.ListTasksResponse{}
	for _, t := range tasks {
		ts := &pb.TaskStatus{Upid: t.UPID, Type: t.Type, Started: timestamppb.New(time.Unix(t.StartTime, 0))}
		if t.EndTime > 0 {
			ts.Stopped, ts.ExitStatus, ts.Ended = true, t.Status, timestamppb.New(time.Unix(t.EndTime, 0))
		}
		out.Tasks = append(out.Tasks, ts)
	}
	slices.SortStableFunc(out.Tasks, func(a, b *pb.TaskStatus) int {
		return cmp.Compare(b.GetStarted().AsTime().Unix(), a.GetStarted().AsTime().Unix())
	})
	return out, nil
}

func (c *component) ListBackupStorages(ctx context.Context, req *pb.ListBackupStoragesRequest) (*pb.ListBackupStoragesResponse, error) {
	if req.GetNode() == "" {
		return nil, status.Error(codes.InvalidArgument, "node is required")
	}
	var storages []struct {
		Storage string            `json:"storage"`
		Type    string            `json:"type"`
		Content string            `json:"content"`
		Active  proxmox.IntOrBool `json:"active"`
		Enabled proxmox.IntOrBool `json:"enabled"`
		Shared  proxmox.IntOrBool `json:"shared"`
		Avail   uint64            `json:"avail"`
		Total   uint64            `json:"total"`
	}
	if err := c.client.Get(ctx, fmt.Sprintf("/nodes/%s/storage?content=backup&enabled=1", url.PathEscape(req.GetNode())), &storages); err != nil {
		return nil, unavailable(err)
	}
	out := &pb.ListBackupStoragesResponse{}
	for _, s := range storages {
		if !slices.Contains(strings.Split(s.Content, ","), "backup") {
			continue
		}
		out.Storages = append(out.Storages, &pb.Storage{Name: s.Storage, Type: s.Type, Active: bool(s.Active), Shared: bool(s.Shared), Avail: s.Avail, Total: s.Total})
	}
	slices.SortFunc(out.Storages, func(a, b *pb.Storage) int { return strings.Compare(a.GetName(), b.GetName()) })
	return out, nil
}
