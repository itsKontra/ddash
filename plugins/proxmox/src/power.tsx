import { useAction, useQuery, type Action } from "@ddash/plugin";
import { Kind as RefKind } from "@ddash/proto/ddash/workload/workload_pb";
import { Kind, ProxmoxApiService, type ListPermissionsResponse, type Workload } from "@ddash/proto/proxmox-api/proxmox_api_pb";
import { PowerAction, ProxmoxService, type PowerTask } from "@ddash/proto/proxmox/proxmox_pb";
import { ActionStrip, Button, Notice, Pill, Skeleton } from "@ddash/ui";
import { kindLabel } from "@ddash/display";
import { uptime } from "./format";
import { StartAsked } from "./start-asked";

/** The privilege ddash needs on a Workload to start, stop or restart it. */
export const powerMgmt = "VM.PowerMgmt";

/** "LXC 102 media-stack". */
export const workloadLabel = (w: Workload) => `${kindLabel(w.kind)} ${w.vmid} ${w.name}`;

/** The Workload as the proxmox Component names it: kind, node and VMID. */
export const refOf = (w: Workload) => ({ kind: w.kind === Kind.VM ? RefKind.VM : RefKind.LXC, host: w.node, id: String(w.vmid) });

const running: Record<PowerAction, string> = {
  [PowerAction.UNSPECIFIED]: "busy",
  [PowerAction.START]: "starting",
  [PowerAction.STOP]: "stopping",
  [PowerAction.RESTART]: "restarting",
};

/** What the power task running on a Workload reads as, e.g. "restarting", or undefined while none runs. */
export function taskOf(tasks: readonly PowerTask[] | undefined, w: Workload): string | undefined {
  const task = tasks?.find((t) => t.workload?.id === String(w.vmid));
  return task && running[task.action];
}

/** One power Action offered on a Workload. */
export interface PowerItem {
  label: "Start" | "Stop" | "Restart";
  danger: boolean;
  run: () => void;
}

/** The three power Actions, as useAction gives them. */
export interface PowerActions {
  start: Action<typeof ProxmoxService.method.startWorkload>;
  stop: Action<typeof ProxmoxService.method.stopWorkload>;
  restart: Action<typeof ProxmoxService.method.restartWorkload>;
}

/** The power Actions, for the Page's buttons and the command palette alike. */
export function usePowerActions(): PowerActions {
  return {
    start: useAction(ProxmoxService.method.startWorkload),
    stop: useAction(ProxmoxService.method.stopWorkload),
    restart: useAction(ProxmoxService.method.restartWorkload),
  };
}

/**
 * The power Actions a Workload's status allows, with the confirm each
 * asks: Restart and Stop while it runs, Start while it is stopped, none
 * for a template. Whether ddash may (Protected) is the caller's to say.
 */
export function powerItems(w: Workload, { start, stop, restart }: PowerActions): PowerItem[] {
  const label = workloadLabel(w);
  const request = { workload: refOf(w) };
  if (w.template) return [];
  if (w.status === "running") {
    return [
      { label: "Restart", danger: true, run: () => void restart.run(request, { confirm: { text: `Restart ${label}?`, action: "Restart" } }) },
      {
        label: "Stop",
        danger: true,
        run: () =>
          void stop.run(request, { confirm: { text: `Stop ${label}? Proxmox shuts it down cleanly and turns it off after 45 s if it hasn't stopped.`, action: "Stop" } }),
      },
    ];
  }
  if (w.status === "stopped") return [{ label: "Start", danger: false, run: () => void start.run(request) }];
  return [];
}

/** Whether ddash may change a Workload's power: undefined until its permissions are known, false where it is Protected. */
export function mayPower(perms: ListPermissionsResponse | undefined, w: Workload): boolean | undefined {
  const privileges = perms?.workloads.find((p) => p.vmid === w.vmid)?.privileges;
  return privileges && privileges.includes(powerMgmt);
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
  const actions = usePowerActions();
  const { start, stop, restart } = actions;

  const may = mayPower(perms.data, w);
  const task = taskOf(tasks.data?.tasks, w);
  const label = workloadLabel(w);
  const items = powerItems(w, actions);
  return {
    known: may !== undefined,
    protected: may === false,
    items,
    label,
    busy: task !== undefined || start.pending || stop.pending || restart.pending,
    task,
    error: start.error ?? stop.error ?? restart.error,
  };
}

const lockedWhy = "ddash doesn't hold VM.PowerMgmt on it. Start, stop or restart it in Proxmox.";

/** The lock a Protected Workload shows in place of its power Actions. */
export function ProtectedLock() {
  return (
    <span title={lockedWhy}>
      <Pill>protected in Proxmox</Pill>
    </span>
  );
}

function Failure({ message }: { message?: string }) {
  return message ? <Notice tone="down">{message}</Notice> : null;
}

/** "No power Actions for a paused Workload." */
const noActions = (w: Workload) => `No power Actions for a ${w.template ? "template" : w.status} Workload.`;

/**
 * A Workloads row's action strip: its status ("running, up 3 d"), or the
 * task while one runs ("Proxmox is restarting it"), with its power Actions
 * as Buttons and why the last one failed; or the lock and why.
 */
export function PowerStrip({ workload: w }: { workload: Workload }) {
  const p = usePower(w);
  if (!p.known) return <Skeleton height="sm" />;
  if (p.protected)
    return (
      <ActionStrip summary={lockedWhy}>
        <ProtectedLock />
      </ActionStrip>
    );
  const summary = p.task ? `Proxmox is ${p.task} it` : w.status === "running" ? `running, up ${uptime(w.uptimeSeconds)}` : w.status;
  return (
    <ActionStrip summary={summary} notice={<Failure message={p.error?.rawMessage} />}>
      {p.items.length > 0 ? (
        p.items.map((i) => (
          <Button key={i.label} size="sm" variant={i.danger ? "danger" : "default"} disabled={p.busy} onClick={i.run}>
            {i.label}
          </Button>
        ))
      ) : (
        <span className="text-xs text-muted-foreground">{noActions(w)}</span>
      )}
    </ActionStrip>
  );
}

/** A Workload's detail: its power Actions as buttons, or the lock; ?start asks to start it. */
export function PowerButtons({ workload: w }: { workload: Workload }) {
  const p = usePower(w);
  return (
    <>
      <Buttons workload={w} power={p} />
      <StartAsked label={p.label} start={p.protected ? undefined : p.items.find((i) => i.label === "Start")?.run} known={p.known} />
    </>
  );
}

function Buttons({ workload: w, power: p }: { workload: Workload; power: ReturnType<typeof usePower> }) {
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
        {p.items.length === 0 && <span className="text-sm text-muted-foreground">{noActions(w)}</span>}
        {p.task && <Pill tone="maint">{p.task}</Pill>}
      </div>
      <Failure message={p.error?.rawMessage} />
    </div>
  );
}
