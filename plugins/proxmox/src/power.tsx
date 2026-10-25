import { useAction, useQuery } from "@ddash/plugin";
import { Kind as RefKind } from "@ddash/proto/ddash/workload/workload_pb";
import { Kind, ProxmoxApiService, type Workload } from "@ddash/proto/proxmox-api/proxmox_api_pb";
import { PowerAction, ProxmoxService, type PowerTask } from "@ddash/proto/proxmox/proxmox_pb";
import { Button, Menu, Notice, Pill, Skeleton } from "@ddash/ui";
import { kindLabel } from "@ddash/display";

/** The privilege ddash needs on a Workload to start, stop or restart it. */
export const powerMgmt = "VM.PowerMgmt";

/** "LXC 102 media-stack". */
export const workloadLabel = (w: Workload) => `${kindLabel(w.kind)} ${w.vmid} ${w.name}`;

/** The Workload as the proxmox Component names it: kind, node and VMID. */
const refOf = (w: Workload) => ({ kind: w.kind === Kind.VM ? RefKind.VM : RefKind.LXC, host: w.node, id: String(w.vmid) });

const running: Record<PowerAction, string> = {
  [PowerAction.UNSPECIFIED]: "busy",
  [PowerAction.START]: "starting",
  [PowerAction.STOP]: "stopping",
  [PowerAction.RESTART]: "restarting",
};

/** One power Action offered on a Workload. */
interface Item {
  label: "Start" | "Stop" | "Restart";
  danger: boolean;
  run: () => void;
}

/**
 * What the Operator may do to a Workload's power: `undefined` until ddash's
 * permissions are known, `protected` where it holds no VM.PowerMgmt, else
 * the Actions its status allows (none for a template), disabled while one
 * runs (busy) and why the last one failed.
 */
export function usePower(w: Workload) {
  const perms = useQuery(ProxmoxApiService.method.listPermissions, {});
  const tasks = useQuery(ProxmoxService.method.listPowerTasks, {});
  const start = useAction(ProxmoxService.method.startWorkload);
  const stop = useAction(ProxmoxService.method.stopWorkload);
  const restart = useAction(ProxmoxService.method.restartWorkload);

  const privileges = perms.data?.workloads.find((p) => p.vmid === w.vmid)?.privileges;
  const task: PowerTask | undefined = tasks.data?.tasks.find((t) => t.workload?.id === String(w.vmid));
  const label = workloadLabel(w);
  const request = { workload: refOf(w) };
  const items: Item[] = [];
  if (!w.template && w.status === "running") {
    items.push(
      { label: "Restart", danger: true, run: () => void restart.run(request, { confirm: { text: `Restart ${label}?`, action: "Restart" } }) },
      {
        label: "Stop",
        danger: true,
        run: () =>
          void stop.run(request, { confirm: { text: `Stop ${label}? Proxmox shuts it down cleanly and turns it off after 45 s if it hasn't stopped.`, action: "Stop" } }),
      },
    );
  } else if (!w.template && w.status === "stopped") {
    items.push({ label: "Start", danger: false, run: () => void start.run(request) });
  }
  return {
    known: privileges !== undefined,
    protected: privileges !== undefined && !privileges.includes(powerMgmt),
    items,
    busy: task !== undefined || start.pending || stop.pending || restart.pending,
    task: task && running[task.action],
    error: start.error ?? stop.error ?? restart.error,
  };
}

/** The lock a Protected Workload shows in place of its power Actions. */
export function ProtectedLock() {
  return (
    <span title="ddash doesn't hold VM.PowerMgmt on it. Start, stop or restart it in Proxmox.">
      <Pill>protected in Proxmox</Pill>
    </span>
  );
}

function Failure({ message }: { message?: string }) {
  return message ? <Notice tone="down">{message}</Notice> : null;
}

/** A row's power Actions: its menu, or the lock. */
export function PowerMenu({ workload: w }: { workload: Workload }) {
  const p = usePower(w);
  if (!p.known) return null;
  if (p.protected) return <ProtectedLock />;
  return (
    <div className="flex flex-col items-end gap-1">
      <div className="flex items-center gap-1.5">
        {p.task && <Pill tone="maint">{p.task}</Pill>}
        {p.items.length > 0 && (
          <Menu label={`Power for ${w.name}`} disabled={p.busy} items={p.items.map((i) => ({ label: i.label, danger: i.danger, onSelect: i.run }))} />
        )}
      </div>
      <Failure message={p.error?.rawMessage} />
    </div>
  );
}

/** A Workload's detail: its power Actions as buttons, or the lock. */
export function PowerButtons({ workload: w }: { workload: Workload }) {
  const p = usePower(w);
  if (!p.known) return <Skeleton height="sm" />;
  if (p.protected) return <ProtectedLock />;
  return (
    <div className="flex flex-col gap-2">
      <div className="flex flex-wrap items-center gap-2">
        {p.items.map((i) => (
          <Button key={i.label} variant={i.danger ? "danger" : "default"} disabled={p.busy} onClick={i.run}>
            {i.label}
          </Button>
        ))}
        {p.items.length === 0 && <span className="text-sm text-muted-foreground">No power Actions for a {w.template ? "template" : w.status} Workload.</span>}
        {p.task && <Pill tone="maint">{p.task}</Pill>}
      </div>
      <Failure message={p.error?.rawMessage} />
    </div>
  );
}
