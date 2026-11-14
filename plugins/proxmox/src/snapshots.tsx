import { useAction, useQuery } from "@ddash/plugin";
import { State } from "@ddash/proto/ddash/job/job_pb";
import { ProxmoxApiService, type Workload } from "@ddash/proto/proxmox-api/proxmox_api_pb";
import { ProxmoxService, type BackupTask } from "@ddash/proto/proxmox/proxmox_pb";
import { Button, Facts, Notice, Pill, Select, Skeleton, StatusList, type StatusItem } from "@ddash/ui";
import { ago } from "@ddash/display";
import { useNow } from "./format";
import { useEffect, useState } from "react";
import { refOf } from "./power";
import { stateTone, stateWord } from "./steps";

// A Workload's ddash Snapshot (the one ddash keeps, with its age), the
// snapshots taken by hand that ddash never touches, Take snapshot, and
// backups to a storage the Operator picks.

/** The "Snapshots & backups" Panel's body. */
export function SnapshotsAndBackups({ workload: w }: { workload: Workload }) {
  return (
    <div className="flex flex-col gap-6">
      <Snapshots workload={w} />
      <Backups workload={w} />
    </div>
  );
}

function Snapshots({ workload: w }: { workload: Workload }) {
  const { data, error, refetch } = useQuery(ProxmoxService.method.getSnapshots, { workload: refOf(w) });
  const take = useAction(ProxmoxService.method.takeSnapshot);
  // An update run takes and prunes ddash Snapshots on its own: read them again once it ended.
  const updates = useQuery(ProxmoxService.method.listUpdates, {});
  const runEnded = updates.data?.runs.find((r) => r.workload?.id === String(w.vmid))?.ended?.seconds.toString();
  useEffect(() => {
    if (runEnded) void refetch();
  }, [runEnded, refetch]);
  const [deleted, setDeleted] = useState<string[]>();
  const now = useNow();
  if (error && !data) return <Notice tone="down">{error.rawMessage}</Notice>;
  if (!data) return <Skeleton height="sm" />;
  const d = data.ddashSnapshot;
  const facts = [
    {
      label: "ddash Snapshot",
      value: !data.supported ? (
        <Pill tone="warn">snapshots not supported</Pill>
      ) : d ? (
        <span>
          <span className="font-mono text-xs">{d.name}</span> <span className="text-muted-foreground">· {ago(d.created, now)}</span>
        </span>
      ) : (
        <span className="text-muted-foreground">none yet</span>
      ),
    },
    {
      label: "Taken by hand",
      value: data.others.length ? (
        <span className="flex flex-col">
          {data.others.map((o) => (
            <span key={o.name}>
              <span className="font-mono text-xs">{o.name}</span> <span className="text-muted-foreground">· {ago(o.created, now)}</span>
            </span>
          ))}
        </span>
      ) : (
        <span className="text-muted-foreground">none</span>
      ),
    },
  ];
  return (
    <div className="flex flex-col gap-3">
      <Facts items={facts} />
      <p className="text-xs text-muted-foreground">
        ddash keeps one ddash Snapshot per Workload: it deletes the older one only after a new one succeeded. Snapshots taken by hand are never touched.
      </p>
      {!data.supported && <p className="text-xs text-muted-foreground">This Workload&apos;s storage can&apos;t take a snapshot: updates run without one.</p>}
      {take.error && <Notice tone="down">{take.error.rawMessage}</Notice>}
      {deleted && deleted.length > 0 && <Notice tone="info">Deleted the older ddash Snapshot {deleted.join(", ")}.</Notice>}
      <div>
        <Button
          variant="outline"
          disabled={!data.supported || take.pending}
          onClick={async () => {
            const res = await take.run({ workload: refOf(w) });
            if (res) setDeleted(res.deleted);
          }}
        >
          {take.pending ? "Taking snapshot…" : "Take snapshot"}
        </Button>
      </div>
    </div>
  );
}

function Backups({ workload: w }: { workload: Workload }) {
  const storages = useQuery(ProxmoxApiService.method.listBackupStorages, { node: w.node });
  const backups = useQuery(ProxmoxService.method.listBackups, {});
  const backup = useAction(ProxmoxService.method.backup);
  const [storage, setStorage] = useState("");
  const list = storages.data?.storages ?? [];
  const picked = storage || list[0]?.name || "";
  const mine = (backups.data?.backups ?? []).filter((b) => b.workload?.id === String(w.vmid));
  const running = mine.some((b) => b.state === State.RUNNING || b.state === State.PENDING);
  return (
    <div className="flex flex-col gap-3">
      <p className="text-sm font-semibold">Backup</p>
      <p className="text-xs text-muted-foreground">A vzdump in snapshot mode: the Workload keeps running while Proxmox copies it.</p>
      {storages.error && <Notice tone="down">{storages.error.rawMessage}</Notice>}
      <div className="flex flex-wrap items-end gap-2">
        <Select label="Backup storage" value={picked} onChange={(e) => setStorage(e.target.value)} disabled={!list.length}>
          {!list.length && <option value="">{storages.data ? "No storage holds backups" : "Loading…"}</option>}
          {list.map((s) => (
            <option key={s.name} value={s.name}>
              {s.name} ({s.type})
            </option>
          ))}
        </Select>
        <Button variant="outline" disabled={!picked || backup.pending || running} onClick={() => void backup.run({ workload: refOf(w), storage: picked })}>
          Back up
        </Button>
      </div>
      {backup.error && <Notice tone="down">{backup.error.rawMessage}</Notice>}
      {mine.length > 0 && <BackupList backups={mine} />}
    </div>
  );
}

function BackupList({ backups }: { backups: readonly BackupTask[] }) {
  const now = useNow();
  const items: StatusItem[] = backups.map((b) => ({
    tone: stateTone(b.state),
    title: `Backup to ${b.storage}`,
    aside: b.state === State.RUNNING ? "running…" : stateWord[b.state],
    meta: `started ${ago(b.started, now)}${b.ended ? ` · ended ${ago(b.ended, now)}` : ""}${b.error ? ` · ${b.error}` : ""}`,
  }));
  return <StatusList items={items} />;
}
