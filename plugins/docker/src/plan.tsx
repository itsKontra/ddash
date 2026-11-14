import type { MessageInitShape } from "@bufbuild/protobuf";
import { useQuery, useStreamingAction } from "@ddash/plugin";
import {
  ComposeService,
  PlanSchema,
  StepState,
  System,
  type ComposeActionSchema,
  type Plan,
  type Run,
} from "@ddash/proto/compose/compose_pb";
import { State as JobState } from "@ddash/proto/ddash/job/job_pb";
import { Button, Checkbox, CodeBlock, Dialog, FormActions, Notice, Skeleton, StatusList, type StatusItem, type Tone } from "@ddash/ui";
import { useState, type ReactNode } from "react";

// Compose Project Actions on screen: their Plan (steps, the Compose file's
// diff, what blocks it, the Protected override), and their run, which goes
// on after the Dialog closes: its steps' progress and the Agent's output
// come from RunAction's stream, then from ListRuns.

export type ActionInit = MessageInitShape<typeof ComposeActionSchema>;

const systemName: Record<System, string> = {
  [System.UNSPECIFIED]: "",
  [System.DOCKER_AGENT]: "Docker Agent",
  [System.DDASH]: "ddash",
};

const look: Record<StepState, { tone: Tone; aside?: string }> = {
  [StepState.UNSPECIFIED]: { tone: "idle" },
  [StepState.PENDING]: { tone: "idle" },
  [StepState.RUNNING]: { tone: "maint", aside: "running…" },
  [StepState.DONE]: { tone: "up", aside: "done" },
  [StepState.FAILED]: { tone: "down", aside: "failed" },
  [StepState.SKIPPED]: { tone: "idle", aside: "skipped" },
};

/** A Plan's steps, each with its system, values and (once running) its state and detail. */
export function PlanSteps({ plan, run }: { plan: Plan; run?: Run }) {
  const steps = run?.steps.length ? run.steps : plan.steps;
  const items: StatusItem[] = steps.map((s, i) => {
    const state = run?.states[i] ?? StepState.PENDING;
    const detail = run?.details[i];
    return {
      ...look[state],
      title: s.title,
      meta: [systemName[s.system], detail].filter(Boolean).join(" · "),
      body:
        s.values.length > 0 ? (
          <ul className="flex flex-col gap-0.5 text-xs break-words text-muted-foreground">
            {s.values.map((v, j) => (
              <li key={j}>
                <span className="text-foreground">{v.label}</span>: {v.before && v.before !== v.after ? <span className="font-mono">{v.before} → </span> : ""}
                <span className={v.before ? "font-mono" : undefined}>{v.after || "removed"}</span>
              </li>
            ))}
          </ul>
        ) : undefined,
    };
  });
  return <StatusList timeline wrap items={items} />;
}

/** The Agent's last output lines for the step running now, while a run shows them. */
export function JobLog({ run }: { run: Run }) {
  const job = run.job;
  if (!job) return null;
  const step = job.steps.find((s) => s.state === JobState.RUNNING) ?? [...job.steps].reverse().find((s) => s.log.length > 0);
  if (!step || step.log.length === 0) return null;
  return (
    <CodeBlock label={`Output of ${step.title}`}>
      {step.log.slice(-8).join("\n")}
    </CodeBlock>
  );
}

/** A unified diff with its added and removed lines in their tones. */
export function Diff({ diff }: { diff: string }) {
  const lines = diff.replace(/\n$/, "").split("\n");
  return (
    <CodeBlock label="The change">
      {lines.map((l, i) => (
        <span
          key={i}
          className={
            l.startsWith("+") && !l.startsWith("+++")
              ? "text-up-text"
              : l.startsWith("-") && !l.startsWith("---")
                ? "text-down-text"
                : l.startsWith("@@")
                  ? "text-info-text"
                  : undefined
          }
        >
          {l}
          {"\n"}
        </span>
      ))}
    </CodeBlock>
  );
}

/** The run in flight (or just ended) with id, as ListRuns has it. */
export function useRun(id: string | undefined): Run | undefined {
  const runs = useQuery(ComposeService.method.listRuns, {});
  return id ? runs.data?.runs.find((r) => r.id === id) : undefined;
}

/** How a run ended, as one line, or undefined while it runs. */
export function ending(run: Run | undefined): { tone: Tone; text: string } | undefined {
  if (!run?.ended) return undefined;
  if (run.error) return { tone: "down", text: run.error };
  return { tone: "up", text: run.outcome === "done" ? "Done." : `Done: ${run.outcome}.` };
}

/**
 * A Compose Project Action's Plan in a Dialog: its steps, the change (for
 * a save), what blocks it, the Protected override, then the run's
 * progress, streamed while the Dialog is open and kept in ListRuns after:
 * closing it leaves the run going.
 */
export function PlanDialog({
  title,
  action,
  confirm,
  danger,
  onClose,
  onDone,
  extra,
}: {
  title: string;
  action: ActionInit;
  /** The confirming button's label, e.g. "Update". */
  confirm: string;
  danger?: boolean;
  onClose: () => void;
  /** Called once the run ended well, e.g. to navigate to a new Compose Project. */
  onDone?: (run: Run) => void;
  /** Above the steps, e.g. a save's recreate switch. */
  extra?: ReactNode;
}) {
  const [override, setOverride] = useState(false);
  const full: ActionInit = { ...action, overrideProtected: override };
  const planQuery = useQuery(ComposeService.method.planAction, { action: full });
  const stream = useStreamingAction(ComposeService.method.runAction);
  const [started, setStarted] = useState(false);
  const streamed = stream.last?.run;
  // Once RunAction returned, ListRuns carries the run on.
  const followed = useRun(streamed?.id);
  const run = followed ?? streamed;
  // Refused because the systems changed: the new Plan, to confirm again.
  const refused = stream.error?.findDetails(PlanSchema)[0];
  const plan = refused ?? planQuery.data?.plan;
  const failed = stream.error && !refused && !run ? stream.error.rawMessage : undefined;
  const ended = ending(run);
  const running = started && !ended && !failed && !refused;
  const blocked = plan && !run ? plan.blocked : [];
  const needsOverride = !!plan && plan.protected.length > 0;
  const can = !!plan && blocked.filter((b) => !(override && b.includes("Protected"))).length === 0 && !running && !ended;

  const go = async () => {
    if (!plan) return;
    setStarted(true);
    const last = await stream.run({ action: full, plan });
    if (!last) setStarted(false);
    if (last?.run?.ended && !last.run.error) onDone?.(last.run);
  };

  return (
    <Dialog
      title={title}
      subtitle="ddash does these steps in order and stops at the first that fails. Closing this leaves a started run going."
      onClose={onClose}
      footer={
        <FormActions>
          <Button variant="ghost" onClick={onClose}>
            {running ? "Close, keep it running" : ended ? "Close" : "Cancel"}
          </Button>
          {!ended && (
            <Button variant={danger ? "danger" : "default"} disabled={!can} onClick={() => void go()}>
              {running ? "Running…" : confirm}
            </Button>
          )}
        </FormActions>
      }
    >
      <div className="flex flex-col gap-4">
        {!plan && planQuery.error && <Notice tone="down">{planQuery.error.rawMessage}</Notice>}
        {!plan && !planQuery.error && <Skeleton height="lg" />}
        {extra}
        {plan && needsOverride && !run && (
          <Notice tone="warn" title="Protected">
            <p>
              {plan.protected.join(", ")} {plan.protected.length === 1 ? "is" : "are"} Protected: reaching ddash or the network may depend on{" "}
              {plan.protected.length === 1 ? "it" : "them"}.
            </p>
            <div className="mt-2">
              <Checkbox label="Override: do it anyway" checked={override} onChange={(e) => setOverride(e.target.checked)} disabled={running} />
            </div>
          </Notice>
        )}
        {plan && blocked.filter((b) => !(needsOverride && b.includes("Protected"))).length > 0 && (
          <Notice tone="warn">
            {blocked
              .filter((b) => !(needsOverride && b.includes("Protected")))
              .map((b) => (
                <p key={b}>{b}</p>
              ))}
          </Notice>
        )}
        {plan && plan.notes.length > 0 && !run && (
          <Notice tone="info">
            {plan.notes.map((n) => (
              <p key={n}>{n}</p>
            ))}
          </Notice>
        )}
        {refused && <Notice tone="warn">Changed since you looked: ddash will now do what&apos;s below. Check it, then confirm again.</Notice>}
        {failed && <Notice tone="down">{failed}</Notice>}
        {ended && <Notice tone={ended.tone}>{ended.text}</Notice>}
        {plan?.diff && !run && <Diff diff={plan.diff} />}
        {plan && <PlanSteps plan={plan} run={run} />}
        {run && !run.ended && <JobLog run={run} />}
      </div>
    </Dialog>
  );
}
