import { useAction, useQuery } from "@ddash/plugin";
import { State } from "@ddash/proto/ddash/job/job_pb";
import { ProxmoxService, SkipReason, type BulkUpdate, type SkippedWorkload } from "@ddash/proto/proxmox/proxmox_pb";
import { Button, Dialog, FormActions, Notice, Skeleton, StatusList, type StatusItem } from "@ddash/ui";
import { useState } from "react";
import { ended, stateTone, stateWord } from "./steps";
import { updatesText, useUpdates } from "./updates";

// "Update all running": one Plan listing every Workload it updates, in
// order, and those it skips with why; it runs one Workload at a time,
// each with a ddash Snapshot first unless its setting says otherwise, and
// stops at the first failure.

/** Why "Update all running" skips a Workload. */
export const skipWhy: Record<SkipReason, string> = {
  [SkipReason.UNSPECIFIED]: "skipped",
  [SkipReason.PROTECTED]: "Protected: ddash doesn't hold VM.PowerMgmt on it",
  [SkipReason.NO_HOST_AGENT]: "no Host Agent",
  [SkipReason.STOPPED]: "not running",
  [SkipReason.AGENT_NOT_READY]: "its Host Agent isn't ready",
  [SkipReason.SNAPSHOTS_NOT_SUPPORTED]: "snapshots not supported, and its setting asks for one",
  [SkipReason.UP_TO_DATE]: "up to date",
  [SkipReason.BUSY]: "an update already runs on it",
};

const skippedItem = (s: SkippedWorkload): StatusItem => ({ tone: "idle", title: s.label, meta: s.detail ? `${skipWhy[s.reason]}: ${s.detail}` : skipWhy[s.reason] });

/** The Workloads Page's button, its Plan Dialog, and the last run's progress under the title. */
export function UpdateAll() {
  const [open, setOpen] = useState(false);
  const bulk = useUpdates()?.bulk;
  const running = bulk !== undefined && !ended(bulk.state);
  return (
    <>
      <Button variant="outline" icon="refresh-cw" disabled={running} onClick={() => setOpen(true)}>
        {running ? "Updating all running…" : "Update all running"}
      </Button>
      {open && <UpdateAllDialog onClose={() => setOpen(false)} />}
    </>
  );
}

/** The last "Update all running": each Workload's state in order, and where it stopped. */
export function BulkProgress({ bulk }: { bulk: BulkUpdate }) {
  const items: StatusItem[] = bulk.items.map((i) => ({
    tone: stateTone(i.state),
    title: i.label,
    aside: i.state === State.SKIPPED ? "not run" : stateWord[i.state],
  }));
  return (
    <div className="flex flex-col gap-3">
      <StatusList timeline wrap items={items} />
      {bulk.state === State.FAILED && bulk.error && <Notice tone="down">Stopped: {bulk.error}</Notice>}
    </div>
  );
}

/** The "Update all running" Plan, confirmed in a Dialog, then its progress. */
export function UpdateAllDialog({ onClose }: { onClose: () => void }) {
  const planQuery = useQuery(ProxmoxService.method.planUpdateAll, {});
  const run = useAction(ProxmoxService.method.runUpdateAll);
  const bulk = useUpdates()?.bulk;
  const [started, setStarted] = useState<string>();
  const plan = planQuery.data;
  const mine = started && bulk?.id === started ? bulk : undefined;
  const can = !!plan && plan.workloads.length > 0 && !run.pending && !started;

  const go = async () => {
    if (!plan) return;
    // The Plan Dialog is the confirmation.
    const res = await run.run({ workloads: plan.workloads.flatMap((p) => (p.workload ? [p.workload] : [])) });
    if (res?.bulk) setStarted(res.bulk.id);
  };

  const eligible: StatusItem[] = (plan?.workloads ?? []).map((p) => ({
    tone: "idle",
    title: p.label,
    meta: `${p.packages.length ? updatesText(p.packages.length) : "nothing pending"} · ${p.snapshot ? "ddash Snapshot first" : "no snapshot"}`,
  }));
  return (
    <Dialog
      title="Update all running"
      subtitle="ddash updates these one at a time, each in Maintenance, and stops at the first that fails. It never reboots them."
      onClose={onClose}
      footer={
        <FormActions>
          <Button variant="ghost" onClick={onClose} disabled={run.pending}>
            {started ? "Close" : "Cancel"}
          </Button>
          {!started && (
            <Button disabled={!can} onClick={() => void go()}>
              Update all
            </Button>
          )}
        </FormActions>
      }
    >
      <div className="flex flex-col gap-4">
        {!plan && planQuery.error && <Notice tone="down">{planQuery.error.rawMessage}</Notice>}
        {!plan && !planQuery.error && <Skeleton height="lg" />}
        {mine ? (
          <BulkProgress bulk={mine} />
        ) : (
          plan && (
            <>
              <div className="flex flex-col gap-2">
                <p className="text-sm font-semibold">Updates, in this order</p>
                {eligible.length ? <StatusList timeline wrap items={eligible} /> : <p className="text-sm text-muted-foreground">No running Workload is ready to update.</p>}
              </div>
              {plan.skipped.length > 0 && (
                <div className="flex flex-col gap-2">
                  <p className="text-sm font-semibold">Skipped</p>
                  <StatusList wrap items={plan.skipped.map(skippedItem)} />
                </div>
              )}
            </>
          )
        )}
        {run.error && <Notice tone="down">{run.error.rawMessage}</Notice>}
      </div>
    </Dialog>
  );
}
