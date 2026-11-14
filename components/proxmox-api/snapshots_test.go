package proxmoxapi_test

import (
	"strings"
	"testing"
	"testing/synctest"

	"github.com/google/go-cmp/cmp"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pb "github.com/itsKontra/ddash/components/proxmox-api/proxmoxapipb"
)

func snapshotNames(res *pb.ListSnapshotsResponse) []string {
	var out []string
	for _, s := range res.GetSnapshots() {
		out = append(out, s.GetName())
	}
	return out
}

func TestListSnapshotsLeavesOutCurrentAndSaysWhetherTheStorageCanSnapshot(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		_, c := node(t, "baseline")
		res, err := c.ListSnapshots(t.Context(), &pb.ListSnapshotsRequest{Workload: lxc(201)})
		if err != nil {
			t.Fatal(err)
		}
		if diff := cmp.Diff([]string{"before-docker-27", "ddash-20261020-031500"}, snapshotNames(res)); diff != "" || !res.GetSupported() {
			t.Fatalf("201's snapshots (-want +got):\n%s supported=%v", diff, res.GetSupported())
		}
		if d := res.GetSnapshots()[1].GetDescription(); d != "Created by ddash before an update" {
			t.Errorf("description = %q, want it without Proxmox's trailing newline", d)
		}
		// telegraf's rootfs is on storage without the snapshot feature.
		res, err = c.ListSnapshots(t.Context(), &pb.ListSnapshotsRequest{Workload: lxc(207)})
		if err != nil {
			t.Fatal(err)
		}
		if res.GetSupported() || len(res.GetSnapshots()) != 0 {
			t.Fatalf("207 = %v, want unsupported and none", res)
		}
	})
}

func TestCreateAndDeleteSnapshotFollowTheirTasks(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		fake, c := node(t, "baseline")
		created, err := c.CreateSnapshot(t.Context(), &pb.CreateSnapshotRequest{Workload: lxc(201), Name: "ddash-20261114-101500", Description: "Created by ddash before an update"})
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(created.GetUpid(), ":vzsnapshot:201:") {
			t.Errorf("upid = %s", created.GetUpid())
		}
		if _, err := c.DeleteSnapshot(t.Context(), &pb.DeleteSnapshotRequest{Workload: lxc(201), Name: "ddash-20261020-031500"}); err != nil {
			t.Fatal(err)
		}
		var names []string
		for _, s := range fake.Snapshots(201) {
			names = append(names, s.Name)
		}
		if diff := cmp.Diff([]string{"before-docker-27", "ddash-20261114-101500"}, names); diff != "" {
			t.Errorf("snapshots (-want +got):\n%s", diff)
		}
		want := []string{
			"POST /api2/json/nodes/pve/lxc/201/snapshot description=Created by ddash before an update snapname=ddash-20261114-101500",
			"GET /api2/json/nodes/pve/tasks/" + created.GetUpid() + "/status",
			"GET /api2/json/nodes/pve/tasks/" + created.GetUpid() + "/status",
		}
		if diff := cmp.Diff(want, powerRequests(fake)[:3]); diff != "" {
			t.Errorf("requests (-want +got):\n%s", diff)
		}
	})
}

func TestASnapshotThatFailsSaysWhy(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		fake, c := node(t, "baseline")
		_, err := c.CreateSnapshot(t.Context(), &pb.CreateSnapshotRequest{Workload: lxc(207), Name: "ddash-20261114-101500"})
		if status.Code(err) != codes.FailedPrecondition || !strings.HasPrefix(status.Convert(err).Message(), "snapshots not supported") {
			t.Fatalf("a snapshot on storage that can't: %v", err)
		}
		fake.FailNextTask("unable to create snapshot: zfs error: out of space")
		_, err = c.CreateSnapshot(t.Context(), &pb.CreateSnapshotRequest{Workload: lxc(201), Name: "ddash-20261114-101500"})
		if status.Code(err) != codes.FailedPrecondition || !strings.Contains(err.Error(), "out of space") {
			t.Fatalf("a failed snapshot task: %v", err)
		}
		if n := len(fake.Snapshots(201)); n != 2 {
			t.Errorf("201 has %d snapshots after a failed one, want 2", n)
		}
		if _, err := c.CreateSnapshot(t.Context(), &pb.CreateSnapshotRequest{Workload: lxc(201), Name: "2-bad"}); status.Code(err) != codes.InvalidArgument {
			t.Errorf("a bad name: %v", err)
		}
	})
}

func TestBackupStartsAVzdumpGetTaskFollows(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		fake, c := node(t, "baseline")
		storages, err := c.ListBackupStorages(t.Context(), &pb.ListBackupStoragesRequest{Node: "pve"})
		if err != nil {
			t.Fatal(err)
		}
		if len(storages.GetStorages()) != 2 || storages.GetStorages()[1].GetName() != "pbs" || !storages.GetStorages()[1].GetShared() {
			t.Fatalf("storages = %v", storages)
		}
		res, err := c.Backup(t.Context(), &pb.BackupRequest{Workload: lxc(201), Storage: "pbs"})
		if err != nil {
			t.Fatal(err)
		}
		if b := fake.Backups(); len(b) != 1 || b[0].Storage != "pbs" || b[0].Mode != "snapshot" || b[0].VMID != 201 {
			t.Fatalf("backups = %v", b)
		}
		var task *pb.TaskStatus
		for range 3 {
			got, err := c.ListTasks(t.Context(), &pb.ListTasksRequest{Node: "pve", Vmid: 201})
			if err != nil {
				t.Fatal(err)
			}
			for _, x := range got.GetTasks() {
				if x.GetUpid() == res.GetUpid() {
					task = x
				}
			}
		}
		if !task.GetStopped() || task.GetExitStatus() != "OK" || task.GetType() != "vzdump" || task.GetEnded() == nil {
			t.Fatalf("task = %v", task)
		}
	})
}
