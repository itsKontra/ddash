import { Notice, StatusList, type StatusItem, type Tone } from "@ddash/ui";

// A Plan on screen, the same in every Plugin: its steps with each one's
// progress, how a run of it ended, and what the Operator reads before and
// after confirming. A Plugin brings its own Component's Plan and wires
// its own RunAction to these.

/** Where a Plan's step is: before the run, running, or how it ended. */
export type StepState = "planned" | "running" | "done" | "failed" | "not run";

const look: Record<StepState, { tone: Tone; aside?: string }> = {
  planned: { tone: "idle" },
  running: { tone: "maint", aside: "running…" },
  done: { tone: "up", aside: "done" },
  failed: { tone: "down", aside: "failed" },
  "not run": { tone: "idle", aside: "not run" },
};

/** One step of a Plan as PlanSteps draws it. */
export interface PlanStep {
  title: string;
  /** The system the step writes to, by the name the Operator knows. */
  system: string;
  /** The values it expects to find (before) and writes (after); no after reads "removed". */
  values: readonly { label: string; before: string; after: string }[];
}

/**
 * A Plan's steps in order, each with its system, the values it expects
 * and writes, and its state. The titles wrap.
 */
export function PlanSteps({ steps, state }: { steps: readonly PlanStep[]; state: (i: number) => StepState }) {
  const items: StatusItem[] = steps.map((s, i) => ({
    ...look[state(i)],
    title: s.title,
    meta: s.system,
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

/**
 * How a run of a Plan ended: done (with what RunAction answered), refused
 * with a new Plan because the systems changed, stopped at a step (done
 * counts the steps before it), or failed otherwise.
 */
export type PlanOutcome<P, R> =
  | { kind: "done"; result: R }
  | { kind: "changed"; plan: P }
  | { kind: "stopped"; done: number; message: string }
  | { kind: "failed"; message: string };

/**
 * A step's state from where the run is: planned until a Plan was
 * confirmed (and again once it was refused or failed before any step),
 * as RunAction streams it while it runs, and after it as the outcome says.
 */
export function stepState(
  i: number,
  run: { ran: boolean; pending: boolean; progress: readonly StepState[]; outcome?: PlanOutcome<unknown, unknown> },
): StepState {
  const { outcome } = run;
  if (!run.ran || outcome?.kind === "changed" || outcome?.kind === "failed") return "planned";
  if (run.pending) return run.progress[i] ?? "running";
  if (outcome?.kind === "done") return "done";
  if (outcome?.kind === "stopped") return i < outcome.done ? "done" : i === outcome.done ? "failed" : "not run";
  return "planned";
}

/** Whether a run ended for good: confirming again does nothing. */
export const planEnded = (outcome?: PlanOutcome<unknown, unknown>) =>
  outcome?.kind === "done" || outcome?.kind === "stopped" || outcome?.kind === "failed";

/**
 * What a Plan Dialog says above its steps: what keeps the Plan from
 * running, what else to know before confirming, that the Plan changed
 * since the Operator looked, and why a run stopped or failed.
 */
export function PlanNotices({ blocked, notes, outcome }: { blocked: readonly string[]; notes: readonly string[]; outcome?: PlanOutcome<unknown, unknown> }) {
  return (
    <>
      {blocked.length > 0 && (
        <Notice tone="warn">
          {blocked.map((b) => (
            <p key={b}>{b}</p>
          ))}
        </Notice>
      )}
      {notes.length > 0 && (
        <Notice tone="info">
          {notes.map((n) => (
            <p key={n}>{n}</p>
          ))}
        </Notice>
      )}
      {outcome?.kind === "changed" && <Notice tone="warn">Changed since you looked: ddash will now do what&apos;s below. Check it, then confirm again.</Notice>}
      {(outcome?.kind === "stopped" || outcome?.kind === "failed") && <Notice tone="down">{outcome.message}</Notice>}
    </>
  );
}
