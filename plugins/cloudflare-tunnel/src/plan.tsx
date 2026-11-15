import type { MessageInitShape } from "@bufbuild/protobuf";
import { planEnded, PlanNotices, PlanSteps, stepState, useQuery, useStreamingAction, type PlanOutcome, type PlanStep, type StepState } from "@ddash/plugin";
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
import { Button, Dialog, FormActions, Notice, Skeleton } from "@ddash/ui";
import { useState } from "react";

// Tunnel Actions' Plans on screen: their steps with each one's progress,
// and running one, in a Plan Dialog, drawn with @ddash/plugin's Plan parts.

export type ActionInit = MessageInitShape<typeof TunnelActionSchema>;

/** A Plan's steps as PlanSteps draws them. */
export const steps = (plan: Plan): PlanStep[] => plan.steps.map((s) => ({ title: s.title, system: systemName[s.system], values: s.values }));

const systemName: Record<System, string> = {
  [System.UNSPECIFIED]: "",
  [System.DDASH]: "ddash",
  [System.CLOUDFLARE_TUNNEL]: "Cloudflare Tunnel",
  [System.CLOUDFLARE_DNS]: "Cloudflare DNS",
};

/** How a run ended: see PlanOutcome. */
export type Outcome = PlanOutcome<Plan, RunActionResponse>;

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

  const state = (i: number) => stepState(i, { ran: !!ran, pending: action.pending, progress: progress.map((p) => runStep[p]), outcome });

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
  const ended = planEnded(outcome);
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
        <PlanNotices blocked={blocked} notes={plan?.notes ?? []} outcome={outcome} />
        {plan && <PlanSteps steps={steps(plan)} state={run.state} />}
      </div>
    </Dialog>
  );
}
