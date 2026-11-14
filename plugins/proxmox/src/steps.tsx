import { State, type Step } from "@ddash/proto/ddash/job/job_pb";
import { CodeBlock, StatusList, type StatusItem, type Tone } from "@ddash/ui";

// A long run's steps on screen (an update's, a job's): each with where it
// is, its detail and its last output lines.

const look: Record<State, { tone: Tone; aside: string }> = {
  [State.UNSPECIFIED]: { tone: "idle", aside: "" },
  [State.PENDING]: { tone: "idle", aside: "waiting" },
  [State.RUNNING]: { tone: "maint", aside: "running…" },
  [State.DONE]: { tone: "up", aside: "done" },
  [State.FAILED]: { tone: "down", aside: "failed" },
  [State.SKIPPED]: { tone: "idle", aside: "skipped" },
};

/** How a run or a step reads in one word: "running", "done", "failed". */
export const stateWord: Record<State, string> = {
  [State.UNSPECIFIED]: "",
  [State.PENDING]: "waiting",
  [State.RUNNING]: "running",
  [State.DONE]: "done",
  [State.FAILED]: "failed",
  [State.SKIPPED]: "skipped",
};

/** A run's or a step's tone. */
export const stateTone = (s: State): Tone => look[s].tone;

/** Whether a run has ended. */
export const ended = (s: State) => s === State.DONE || s === State.FAILED || s === State.SKIPPED;

/** The steps in order, as a timeline; a running or failed step shows its last output lines. */
export function JobSteps({ steps }: { steps: readonly Step[] }) {
  const items: StatusItem[] = steps.map((s) => ({
    ...look[s.state],
    title: s.title,
    meta: s.detail || undefined,
    body:
      s.log.length > 0 && (s.state === State.RUNNING || s.state === State.FAILED) ? (
        <CodeBlock label={`Output of ${s.title}`}>{s.log.join("\n")}</CodeBlock>
      ) : undefined,
  }));
  return <StatusList timeline wrap items={items} />;
}
