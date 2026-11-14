import type { MessageInitShape } from "@bufbuild/protobuf";
import { useQuery, useStreamingAction } from "@ddash/plugin";
import {
  CloudflareTunnelService,
  PlanSchema,
  RunActionResponseSchema,
  StepState as RunStep,
  System,
  type Plan,
  type RunActionResponse,
  type TunnelActionSchema,
} from "@ddash/proto/cloudflare-tunnel/cloudflare_tunnel_pb";
import { Button, Dialog, FormActions, Notice, Skeleton, StatusList, type StatusItem, type Tone } from "@ddash/ui";
import { useState } from "react";

// Tunnel Actions' Plans on screen: their steps with each one's progress,
// and running one, in a Plan Dialog. The look follows the apps Plugin's
// Plan Dialogs (Plugins can't share code).

export type ActionInit = MessageInitShape<typeof TunnelActionSchema>;

const systemName: Record<System, string> = {
  [System.UNSPECIFIED]: "",
  [System.DDASH]: "ddash",
  [System.CLOUDFLARE_TUNNEL]: "Cloudflare Tunnel",
  [System.CLOUDFLARE_DNS]: "Cloudflare DNS",
};

/** Where a step is: before the run, running, or how it ended. */
export type StepState = "planned" | "running" | "done" | "failed" | "not run";

const look: Record<StepState, { tone: Tone; aside?: string }> = {
  planned: { tone: "idle" },
  running: { tone: "maint", aside: "running…" },
  done: { tone: "up", aside: "done" },
  failed: { tone: "down", aside: "failed" },
  "not run": { tone: "idle", aside: "not run" },
};

/** A Plan's steps in order, each with its system, the values it expects and writes, and its state. */
export function PlanSteps({ plan, state }: { plan: Plan; state: (i: number) => StepState }) {
  const items: StatusItem[] = plan.steps.map((s, i) => ({
    ...look[state(i)],
    title: s.title,
    meta: systemName[s.system],
    body:
      s.values.length > 0 ? (
        <ul className="flex flex-col gap-0.5 text-xs break-words text-muted-foreground">
          {s.values.map((v, j) => (
            <li key={j}>
              {v.label}: {v.before && v.before !== v.after ? `${v.before} → ` : ""}
              {v.after || "removed"}
            </li>
          ))}
        </ul>
      ) : undefined,
  }));
  return <StatusList timeline wrap items={items} />;
}

/** How a run ended: done, refused with a new Plan, stopped at a step, or failed otherwise. */
export type Outcome =
  | { kind: "done"; result: RunActionResponse }
  | { kind: "changed"; plan: Plan }
  | { kind: "stopped"; done: number; message: string }
  | { kind: "failed"; message: string };

const runStep: Record<RunStep, StepState> = {
  [RunStep.UNSPECIFIED]: "running",
  [RunStep.PENDING]: "planned",
  [RunStep.RUNNING]: "running",
  [RunStep.DONE]: "done",
  [RunStep.FAILED]: "failed",
};

/**
 * Runs a Tunnel Action with the Plan confirmed, and keeps how it went: the
 * Plan it ran (or the new one it was refused with), and each step's state
 * as RunAction streams it.
 */
export function useRunAction() {
  const action = useStreamingAction(CloudflareTunnelService.method.runAction);
  // A run that stopped half-way changed the Public Hostnames all the same.
  const { refetch } = useQuery(CloudflareTunnelService.method.listPublicHostnames, {});
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

  const state = (i: number): StepState => {
    if (!ran || outcome?.kind === "changed" || outcome?.kind === "failed") return "planned";
    if (action.pending) return progress.length > i ? runStep[progress[i]] : "running";
    if (outcome?.kind === "done") return "done";
    if (outcome?.kind === "stopped") return i < outcome.done ? "done" : i === outcome.done ? "failed" : "not run";
    return "planned";
  };

  return {
    run: async (request: ActionInit, plan: Plan) => {
      setRan({ plan });
      const result = await action.run({ action: request, plan });
      if (result) setRan({ plan, result });
      void refetch();
      return result;
    },
    pending: action.pending,
    outcome,
    /** The Plan to show: the one refused with when Cloudflare changed, else the one run; undefined before a run. */
    plan: outcome?.kind === "changed" ? outcome.plan : ran?.plan,
    state,
  };
}

/**
 * A Tunnel Action's Plan in a Dialog: the server's Plan for action, what
 * keeps it from running and what else to know before confirming, then
 * each step's progress once confirmed. A run stops at the first failed
 * step, saying so, nothing rolled back; one refused because Cloudflare
 * changed shows the new Plan, which confirming again runs.
 */
export function PlanDialog({
  title,
  action,
  confirm,
  danger,
  onClose,
}: {
  title: string;
  action: ActionInit;
  /** The confirming button's label, e.g. "Disable". */
  confirm: string;
  danger?: boolean;
  onClose: () => void;
}) {
  const planQuery = useQuery(CloudflareTunnelService.method.planAction, { action });
  const run = useRunAction();
  const plan = run.plan ?? planQuery.data?.plan;
  const outcome = run.outcome;
  const blocked = plan?.blocked ?? [];
  const ended = outcome?.kind === "done" || outcome?.kind === "stopped" || outcome?.kind === "failed";
  const can = !!plan && blocked.length === 0 && !run.pending && !ended;

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
            <Button onClick={onClose}>Done</Button>
          ) : (
            <Button variant={danger ? "danger" : "default"} disabled={!can} onClick={() => plan && void run.run(action, plan)}>
              {confirm}
            </Button>
          )}
        </FormActions>
      }
    >
      <div className="flex flex-col gap-4">
        {!plan && planQuery.error && <Notice tone="down">{planQuery.error.rawMessage}</Notice>}
        {!plan && !planQuery.error && <Skeleton height="lg" />}
        {blocked.length > 0 && (
          <Notice tone="warn">
            {blocked.map((b) => (
              <p key={b}>{b}</p>
            ))}
          </Notice>
        )}
        {plan && plan.notes.length > 0 && (
          <Notice tone="info">
            {plan.notes.map((n) => (
              <p key={n}>{n}</p>
            ))}
          </Notice>
        )}
        {outcome?.kind === "changed" && <Notice tone="warn">Changed since you looked: ddash will now do what&apos;s below. Check it, then confirm again.</Notice>}
        {(outcome?.kind === "stopped" || outcome?.kind === "failed") && <Notice tone="down">{outcome.message}</Notice>}
        {plan && <PlanSteps plan={plan} state={run.state} />}
      </div>
    </Dialog>
  );
}
