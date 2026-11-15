import type { MessageInitShape } from "@bufbuild/protobuf";
import { planEnded, PlanNotices, PlanSteps, stepState, usePluginLink, useQuery, useStreamingAction, type PlanOutcome, type PlanStep, type StepState } from "@ddash/plugin";
import {
  AppsService,
  NameNoteKind,
  PlanSchema,
  RunActionResponseSchema,
  StepState as RunStep,
  System,
  type AppActionSchema,
  type NameNote,
  type Plan,
  type RunActionResponse,
} from "@ddash/proto/apps/apps_pb";
import { Button, Dialog, FormActions, Notice, Skeleton } from "@ddash/ui";
import { useState, type ReactNode } from "react";
import { Link } from "react-router";

// App Actions' Plans on screen: their steps with each one's progress, the
// "name is free" notes, and running one. Add App shows them beside its
// form; the Plan Dialogs of the Panels' external writes reuse them.

export type ActionInit = MessageInitShape<typeof AppActionSchema>;

/** Why a Plan can't run: its blocks and its taken names. */
export const blocking = (plan: Plan): string[] => [...plan.blocked, ...plan.names.filter((n) => n.kind !== NameNoteKind.REATTACH).map((n) => n.text)];

const systemName: Record<System, string> = {
  [System.UNSPECIFIED]: "",
  [System.DDASH]: "ddash",
  [System.NGINX_PROXY_MANAGER]: "Nginx Proxy Manager",
  [System.PFSENSE]: "pfSense",
  [System.DOCKER]: "Docker",
};

/** A Plan's steps as PlanSteps draws them. */
export const steps = (plan: Plan): PlanStep[] => plan.steps.map((s) => ({ title: s.title, system: systemName[s.system], values: s.values }));

/** The Device holding a name, linked to it on the Network Page when that Plugin is installed. */
function DeviceName({ mac, name }: { mac: string; name: string }) {
  const href = usePluginLink("network", `devices/${mac}`);
  return href ? <Link to={href}>{name}</Link> : <>{name}</>;
}

const appLink = (n: NameNote) => <Link to={`?app=${encodeURIComponent(n.appId)}`}>{n.name}</Link>;

/** One "name is free" note, linking the App or Device it names. */
function Note({ note: n }: { note: NameNote }) {
  switch (n.kind) {
    case NameNoteKind.APP:
      return (
        <>
          {n.hostname} is taken by {appLink(n)}
          {n.hidden ? " (Hidden)" : ""}.
        </>
      );
    case NameNoteKind.DEVICE:
      return (
        <>
          {n.hostname} is the Device <DeviceName mac={n.deviceMac} name={n.name} />
          &apos;s name. To link to it as it is, choose Link only.
        </>
      );
    case NameNoteKind.REATTACH:
      return <>Settings were kept for {appLink(n)}: adding it reattaches them, with its Check history.</>;
    case NameNoteKind.ORPHAN:
      return (
        <>
          {n.hostname} is kept for the orphaned App settings {appLink(n)}: reattach or delete them first.
        </>
      );
    default:
      return <>{n.text}</>;
  }
}

/** A Plan's "name is free" notes: a taken name in the down tone, a reattach note quietly. */
export function NameNotes({ names }: { names: readonly NameNote[] }) {
  return (
    <>
      {names.map((n, i) => (
        <span key={i} className={n.kind === NameNoteKind.REATTACH ? "block text-muted-foreground" : "block font-medium text-down-text"}>
          <Note note={n} />
        </span>
      ))}
    </>
  );
}

/** How a run ended: see PlanOutcome. */
export type Outcome = PlanOutcome<Plan, RunActionResponse>;

/** A step's state as RunAction streams it, on screen; unspecified reads as running. */
const runStep: Record<RunStep, StepState> = {
  [RunStep.UNSPECIFIED]: "running",
  [RunStep.PENDING]: "planned",
  [RunStep.RUNNING]: "running",
  [RunStep.DONE]: "done",
  [RunStep.FAILED]: "failed",
};

/**
 * Runs an App Action with the Plan confirmed, and keeps how it went: the
 * Plan it ran (or the new one it was refused with), and each step's state
 * as RunAction streams it.
 */
export function useRunAction() {
  const action = useStreamingAction(AppsService.method.runAction);
  // A run that stopped half-way changed the Apps all the same.
  const { refetch } = useQuery(AppsService.method.listApps, {});
  const [ran, setRan] = useState<{ plan: Plan; result?: RunActionResponse }>();
  const progress = action.last?.steps ?? [];

  let outcome: Outcome | undefined;
  if (ran && !action.pending) {
    const e = action.error;
    const changed = e?.findDetails(PlanSchema)[0];
    const stopped = e?.findDetails(RunActionResponseSchema)[0];
    if (ran.result) outcome = { kind: "done", result: ran.result };
    else if (changed) outcome = { kind: "changed", plan: changed };
    else if (stopped) outcome = { kind: "stopped", done: stopped.done, message: e!.rawMessage };
    else if (e) outcome = { kind: "failed", message: e.rawMessage };
  }

  const state = (i: number) => stepState(i, { ran: !!ran, pending: action.pending, progress: progress.map((p) => runStep[p]), outcome });

  return {
    /** Runs action, confirming plan; resolves to the answer, or undefined when it didn't succeed. */
    run: async (request: ActionInit, plan: Plan) => {
      setRan({ plan });
      const result = await action.run({ action: request, plan });
      if (result) setRan({ plan, result });
      else void refetch();
      return result;
    },
    pending: action.pending,
    outcome,
    /** The Plan to show: the one refused with when the systems changed, else the one run; undefined before a run. */
    plan: outcome?.kind === "changed" ? outcome.plan : ran?.plan,
    state,
    /** Forgets the run, e.g. once the Action is edited. */
    reset: () => setRan(undefined),
  };
}

/**
 * The pane's Dialog slot: one Dialog at a time over the pane, such as
 * Reattach, and the Plan Dialogs of external writes. open puts one in it,
 * close empties it.
 */
export interface DialogSlot {
  open: (dialog: ReactNode) => void;
  close: () => void;
}

/**
 * An external write's Plan in a Dialog: the server's Plan for action, its
 * "name is free" notes, what keeps it from running and what else to know
 * before confirming, then each step's
 * progress once confirmed. A run stops at the first failed step, saying
 * so, nothing rolled back; one refused because the systems changed shows
 * the new Plan, which confirming again runs. onDone gets RunAction's
 * answer once it ran through and the Apps were re-read, or when the
 * Operator closes it after.
 */
export function PlanDialog({
  title,
  action,
  confirm,
  danger,
  onClose,
  onDone,
}: {
  title: string;
  action: ActionInit;
  /** The confirming button's label, e.g. "Delete". */
  confirm: string;
  danger?: boolean;
  onClose: () => void;
  onDone: (result: RunActionResponse) => void;
}) {
  const planQuery = useQuery(AppsService.method.planAction, { action });
  const run = useRunAction();
  const plan = run.plan ?? planQuery.data?.plan;
  const outcome = run.outcome;
  const blocked = plan ? blocking(plan) : [];
  const ended = planEnded(outcome);
  const can = !!plan && blocked.length === 0 && !run.pending && !ended;

  const go = async () => {
    if (!plan) return;
    const done = await run.run(action, plan);
    if (done && !done.reread) onDone(done);
  };

  return (
    <Dialog
      title={title}
      subtitle="ddash does these steps in order, and stops at the first that fails."
      onClose={onClose}
      footer={
        <FormActions>
          <Button variant="ghost" onClick={onClose} disabled={run.pending}>
            {ended ? "Close" : "Cancel"}
          </Button>
          {outcome?.kind === "done" ? (
            <Button onClick={() => onDone(outcome.result)}>Done</Button>
          ) : (
            <Button variant={danger ? "danger" : "default"} disabled={!can} onClick={() => void go()}>
              {confirm}
            </Button>
          )}
        </FormActions>
      }
    >
      <div className="flex flex-col gap-4">
        {!plan && planQuery.error && <Notice tone="down">{planQuery.error.rawMessage}</Notice>}
        {!plan && !planQuery.error && <Skeleton height="lg" />}
        {plan && plan.names.length > 0 && (
          <div className="text-sm">
            <NameNotes names={plan.names} />
          </div>
        )}
        <PlanNotices blocked={plan?.blocked ?? []} notes={plan?.notes ?? []} outcome={outcome} />
        {outcome?.kind === "done" && <Notice tone="warn">Done. {outcome.result.reread}: the list catches up at the next sync.</Notice>}
        {plan && <PlanSteps steps={steps(plan)} state={run.state} />}
      </div>
    </Dialog>
  );
}
