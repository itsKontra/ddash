import { useAction, useQuery, useShownRuns } from "@ddash/plugin";
import { State } from "@ddash/proto/ddash/job/job_pb";
import { HostAgentApiService, type HostPackages } from "@ddash/proto/host-agent-api/host_agent_api_pb";
import type { Workload } from "@ddash/proto/proxmox-api/proxmox_api_pb";
import { ProxmoxService, type UpdatePlan, type UpdateRun } from "@ddash/proto/proxmox/proxmox_pb";
import { Button, Checkbox, Dialog, FormActions, Notice, Pill, Skeleton, Switch } from "@ddash/ui";
import { ago } from "@ddash/display";
import { useNow } from "./format";
import { useState } from "react";
import { PowerButtons, refOf, workloadLabel } from "./power";
import { ended, JobSteps, stateTone, stateWord } from "./steps";

// A Workload's package updates: what its Host Agent read as pending, the
// Update with its Plan (a ddash Snapshot first, Maintenance while it
// runs, never a reboot), the run's progress, and "reboot required" with
// the Restart beside it.

/** Every paired Host Agent's pending updates, by VMID. */
export function usePackages() {
  const { data } = useQuery(HostAgentApiService.method.listPackages, {});
  return data && new Map(data.hosts.map((h) => [h.vmid, h]));
}

/** The update running or last run on each Workload, and the last "Update all running". */
export function useUpdates() {
  return useQuery(ProxmoxService.method.listUpdates, {}).data;
}

/** Whether a Workload takes a ddash Snapshot before each update: its setting, on unless turned off. */
export function useSnapshotSetting(vmid: number): boolean | undefined {
  const { data } = useQuery(ProxmoxService.method.listUpdateSettings, {});
  if (!data) return undefined;
  return data.settings.find((s) => s.vmid === vmid)?.snapshot ?? true;
}

/** "12 updates", "1 update". */
export const updatesText = (n: number) => `${n} ${n === 1 ? "update" : "updates"}`;

/** The "Updates" Panel's body, for a Workload with a paired Host Agent. */
export function Updates({ workload: w }: { workload: Workload }) {
  const packages = usePackages();
  const updates = useQuery(ProxmoxService.method.listUpdates, {});
  const [planning, setPlanning] = useState(false);
  const refresh = useAction(HostAgentApiService.method.refreshPackages);
  const host = packages?.get(w.vmid);
  const run = updates.data?.runs.find((r) => r.workload?.id === String(w.vmid));
  // The run while it runs, or once it ended with the Page open.
  const [shown] = useShownRuns(updates.data && (run ? [run] : []), updates.isFetchedAfterMount, (r) => ({ id: r.id, ended: ended(r.state) }));
  const running = run !== undefined && !ended(run.state);
  const reboot = host?.rebootRequired || (run?.state === State.DONE && run.rebootRequired);

  if (!packages) return <Skeleton height="sm" />;
  return (
    <div className="flex flex-col gap-4">
      {host ? <Pending host={host} /> : <p className="text-sm text-muted-foreground">The Host Agent hasn&apos;t read the pending updates yet.</p>}
      {refresh.error && <Notice tone="down">{refresh.error.rawMessage}</Notice>}
      <div className="flex flex-wrap items-center gap-2">
        <Button disabled={running || w.status !== "running"} onClick={() => setPlanning(true)}>
          Update
        </Button>
        <Button variant="outline" disabled={refresh.pending || running} onClick={() => void refresh.run({ vmid: w.vmid })}>
          Read again
        </Button>
        <SnapshotSetting vmid={w.vmid} />
      </div>
      {shown && <RunProgress run={shown} />}
      {reboot && !running && (
        <div className="flex flex-col gap-2">
          <p className="flex items-center gap-2 text-sm">
            <Pill tone="warn">reboot required</Pill>
            <span className="text-muted-foreground">ddash never reboots on its own: restart it when it suits you.</span>
          </p>
          <PowerButtons workload={w} />
        </div>
      )}
      {planning && <UpdatePlanDialog workload={w} onClose={() => setPlanning(false)} />}
    </div>
  );
}

/** The pending packages, name and versions, with when they were read. */
function Pending({ host }: { host: HostPackages }) {
  const now = useNow();
  return (
    <div className="flex flex-col gap-2">
      <p className="text-sm">
        <span className="font-semibold">{host.packages.length ? `${updatesText(host.packages.length)} pending` : "Up to date"}</span>
        <span className="text-muted-foreground"> · read {ago(host.read, now)}</span>
      </p>
      {host.error && <Notice tone="warn">{host.error}</Notice>}
      {host.packages.length > 0 && <PackageList packages={host.packages} />}
    </div>
  );
}

/** Packages as "name current → new", one per line. */
function PackageList({ packages }: { packages: readonly { name: string; current: string; new: string }[] }) {
  return (
    <ul aria-label="Pending packages" className="flex max-h-60 flex-col gap-0.5 overflow-y-auto font-mono text-xs">
      {packages.map((p) => (
        <li key={p.name} className="break-words">
          {p.name} <span className="text-muted-foreground">{p.current ? `${p.current} → ` : "→ "}</span>
          {p.new}
        </li>
      ))}
    </ul>
  );
}

/** The Workload's "Snapshot before each update" setting, which acts at once. */
function SnapshotSetting({ vmid }: { vmid: number }) {
  const on = useSnapshotSetting(vmid);
  const set = useAction(ProxmoxService.method.setUpdateSettings);
  if (on === undefined) return null;
  return (
    <>
      <span className="ml-auto inline-flex items-center gap-2 text-sm">
        <Switch label="Snapshot before each update" checked={on} disabled={set.pending} onChange={(snapshot) => void set.run({ settings: { vmid, snapshot } })} />
        Snapshot before each update
      </span>
      {set.error && (
        <div className="basis-full">
          <Notice tone="down">{set.error.rawMessage}</Notice>
        </div>
      )}
    </>
  );
}

/** A run's progress: its steps, then how it ended. */
export function RunProgress({ run }: { run: UpdateRun }) {
  const now = useNow();
  return (
    <section aria-label={`Update of ${run.label}`} className="flex flex-col gap-3 rounded-ui-sm border border-border p-4">
      <p className="flex flex-wrap items-center gap-2 text-sm">
        <Pill tone={stateTone(run.state)} dot>
          {run.state === State.RUNNING ? "updating" : `update ${stateWord[run.state]}`}
        </Pill>
        <span className="text-muted-foreground">
          started {ago(run.started, now)}
          {run.ended && ` · ended ${ago(run.ended, now)}`}
        </span>
      </p>
      <JobSteps steps={run.steps} />
      {run.state === State.FAILED && run.error && <Notice tone="down">{run.error}</Notice>}
      {run.state === State.DONE && (
        <p className="text-sm">
          {run.updated ? `Updated ${run.updated} ${run.updated === 1 ? "package" : "packages"}` : "Nothing to update"}
          {run.snapshot && `, after the ddash Snapshot ${run.snapshot}`}.{run.rebootRequired && " A reboot is required."}
        </p>
      )}
    </section>
  );
}

/** What a Plan says about its snapshot. */
function snapshotNote(plan: UpdatePlan, on: boolean) {
  if (!plan.snapshotSupported) return "Snapshots not supported: this Workload's storage can't take one, so it updates without.";
  if (!on) return "No snapshot this time: there's nothing to roll back to if the update goes wrong.";
  return plan.replacesSnapshot
    ? `ddash takes a ddash Snapshot first, and deletes ${plan.replacesSnapshot} once it succeeded.`
    : "ddash takes a ddash Snapshot first, to roll back to by hand.";
}

/** The update's Plan, confirmed in a Dialog: packages, snapshot (switchable for this run), Maintenance, the Protected override. */
export function UpdatePlanDialog({ workload: w, onClose }: { workload: Workload; onClose: () => void }) {
  const planQuery = useQuery(ProxmoxService.method.planUpdate, { workload: refOf(w) });
  const plan = planQuery.data?.plan;
  const [snapshot, setSnapshot] = useState<boolean>();
  const [override, setOverride] = useState(false);
  const run = useAction(ProxmoxService.method.runUpdate);
  const on = plan ? (snapshot ?? (plan.snapshot && plan.snapshotSupported)) : false;
  const can = !!plan && !plan.blocked && (!plan.protected || override) && (!on || plan.snapshotSupported) && !run.pending;

  const go = async () => {
    // The Plan Dialog is the confirmation.
    const res = await run.run({ workload: refOf(w), snapshot: on, overrideProtected: override });
    if (res) onClose();
  };

  return (
    <Dialog
      title={`Update ${workloadLabel(w)}`}
      subtitle="It runs on in ddash if you close this page, and its progress shows on the Workload."
      onClose={onClose}
      footer={
        <FormActions>
          <Button variant="ghost" onClick={onClose} disabled={run.pending}>
            Cancel
          </Button>
          <Button disabled={!can} onClick={() => void go()}>
            Update
          </Button>
        </FormActions>
      }
    >
      <div className="flex flex-col gap-4">
        {!plan && planQuery.error && <Notice tone="down">{planQuery.error.rawMessage}</Notice>}
        {!plan && !planQuery.error && <Skeleton height="lg" />}
        {plan?.blocked && <Notice tone="warn">{plan.blocked}</Notice>}
        {plan && (
          <>
            <div className="flex flex-col gap-2">
              <p className="text-sm font-semibold">
                {plan.packages.length ? `${updatesText(plan.packages.length)}, as the Host Agent last read them` : "Nothing pending, as the Host Agent last read it"}
              </p>
              {plan.packages.length > 0 && <PackageList packages={plan.packages} />}
              <p className="text-xs text-muted-foreground">It installs whatever is pending when it runs, after reading the package index again.</p>
            </div>
            <div className="flex flex-col gap-2">
              <span className="inline-flex items-center gap-2 text-sm">
                <Switch label="Take a ddash Snapshot first" checked={on} disabled={!plan.snapshotSupported} onChange={setSnapshot} />
                Take a ddash Snapshot first
              </span>
              <p className="text-xs text-muted-foreground">{snapshotNote(plan, on)}</p>
            </div>
            <ul className="flex list-disc flex-col gap-1 pl-5 text-sm">
              <li>The Workload is in Maintenance while it updates, so its Checks don&apos;t page you.</li>
              <li>ddash never reboots it: when a reboot is required, it says so, and you Restart it.</li>
              {plan.rebootRequired && <li>A reboot is already pending.</li>}
            </ul>
            {plan.protected && (
              <Notice tone="warn" title="Protected">
                <p className="mb-2">ddash doesn&apos;t hold VM.PowerMgmt on {workloadLabel(w)}: it is Protected, and ddash leaves it alone unless you say otherwise.</p>
                <Checkbox label="Update anyway: it's Protected" checked={override} onChange={(e) => setOverride(e.target.checked)} />
              </Notice>
            )}
            {run.error && <Notice tone="down">{run.error.rawMessage}</Notice>}
          </>
        )}
      </div>
    </Dialog>
  );
}

