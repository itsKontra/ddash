import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, expect, it } from "vitest";
import { planEnded, PlanNotices, PlanSteps, stepState, type PlanOutcome, type StepState } from "./index";

afterEach(cleanup);

const states = (run: Parameters<typeof stepState>[1]) => [0, 1, 2].map((i) => stepState(i, run));

it("a step is planned until a Plan was confirmed, and again once it was refused or failed before any step", () => {
  const planned: StepState[] = ["planned", "planned", "planned"];
  expect(states({ ran: false, pending: false, progress: [] })).toEqual(planned);
  expect(states({ ran: true, pending: false, progress: [], outcome: { kind: "changed", plan: {} } })).toEqual(planned);
  expect(states({ ran: true, pending: false, progress: [], outcome: { kind: "failed", message: "refused" } })).toEqual(planned);
});

it("a step is as RunAction streams it while it runs, and running before the first message", () => {
  expect(states({ ran: true, pending: true, progress: [] })).toEqual(["running", "running", "running"]);
  expect(states({ ran: true, pending: true, progress: ["done", "running", "planned"] })).toEqual(["done", "running", "planned"]);
});

it("a run that stopped shows the steps before as done, the one it stopped at as failed and the rest as not run", () => {
  expect(states({ ran: true, pending: false, progress: [], outcome: { kind: "stopped", done: 1, message: "pfSense refused" } })).toEqual(["done", "failed", "not run"]);
  expect(states({ ran: true, pending: false, progress: [], outcome: { kind: "done", result: {} } })).toEqual(["done", "done", "done"]);
});

it("a run ended for good unless it was refused with a new Plan", () => {
  const ended = (o?: PlanOutcome<unknown, unknown>) => planEnded(o);
  expect([ended(), ended({ kind: "changed", plan: {} }), ended({ kind: "done", result: {} }), ended({ kind: "stopped", done: 0, message: "" }), ended({ kind: "failed", message: "" })]).toEqual([
    false,
    false,
    true,
    true,
    true,
  ]);
});

it("PlanSteps draws each step with its system, its values and its state", () => {
  render(
    <PlanSteps
      steps={[
        { title: "Create the Proxy Host", system: "Nginx Proxy Manager", values: [{ label: "Upstream", before: "", after: "http://198.18.1.20:8096" }] },
        { title: "Delete the Host Override", system: "pfSense", values: [{ label: "Address", before: "198.18.1.20", after: "" }] },
      ]}
      state={(i) => (i === 0 ? "done" : "failed")}
    />,
  );

  expect(screen.getByText("Create the Proxy Host")).toBeTruthy();
  expect(screen.getByText("Nginx Proxy Manager")).toBeTruthy();
  expect(screen.getByText("Upstream: http://198.18.1.20:8096")).toBeTruthy();
  expect(screen.getByText("Address: 198.18.1.20 → removed")).toBeTruthy();
  expect(screen.getByText("done")).toBeTruthy();
  expect(screen.getByText("failed")).toBeTruthy();
});

it("PlanNotices says what blocks the Plan, what to know, that it changed, and why a run stopped", () => {
  const { rerender } = render(<PlanNotices blocked={["jellyfin is Protected"]} notes={["The Check restarts"]} />);
  expect(screen.getByText("jellyfin is Protected")).toBeTruthy();
  expect(screen.getByText("The Check restarts")).toBeTruthy();

  rerender(<PlanNotices blocked={[]} notes={[]} outcome={{ kind: "changed", plan: {} }} />);
  expect(screen.getByText(/Changed since you looked/)).toBeTruthy();

  rerender(<PlanNotices blocked={[]} notes={[]} outcome={{ kind: "stopped", done: 1, message: "pfSense refused the alias" }} />);
  expect(screen.getByText("pfSense refused the alias")).toBeTruthy();
});
