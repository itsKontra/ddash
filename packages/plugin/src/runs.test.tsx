import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, expect, it } from "vitest";
import { shownRuns, useShownRuns, type RunState } from "./runs";

afterEach(cleanup);

const self = (r: RunState) => r;
const run = (id: string, ended: boolean): RunState => ({ id, ended });

it("shows the runs in flight and the ended ones that weren't ended at the first read", () => {
  const runs = [run("a", true), run("b", false), run("c", true)];
  expect(shownRuns(runs, new Set(["a"]), self).map((r) => r.id)).toEqual(["b", "c"]);
});

it("shows only the runs in flight before the first read", () => {
  expect(shownRuns([run("a", true), run("b", false)], undefined, self).map((r) => r.id)).toEqual(["b"]);
});

function Page({ runs, fresh }: { runs?: RunState[]; fresh: boolean }) {
  const shown = useShownRuns(runs, fresh, self);
  return <p>{shown.map((r) => `${r.id}:${r.ended ? "ended" : "running"}`).join(" ") || "none"}</p>;
}

it("drops a run that had ended when the Page opened, and keeps one that ended while it was open", () => {
  const { rerender } = render(<Page runs={[run("old", true), run("now", false)]} fresh />);
  expect(screen.getByText("now:running")).toBeTruthy();
  rerender(<Page runs={[run("old", true), run("now", true)]} fresh />);
  expect(screen.getByText("now:ended")).toBeTruthy();
});

it("keeps a run that started after the Page opened even when it is first read ended", () => {
  const { rerender } = render(<Page runs={[run("old", true)]} fresh />);
  expect(screen.getByText("none")).toBeTruthy();
  rerender(<Page runs={[run("old", true), run("quick", true)]} fresh />);
  expect(screen.getByText("quick:ended")).toBeTruthy();
});

it("doesn't take a cached answer as the first read", () => {
  // The cache says the run was in flight when the Operator left; it ended since.
  const { rerender } = render(<Page runs={[run("left", false)]} fresh={false} />);
  expect(screen.getByText("left:running")).toBeTruthy();
  rerender(<Page runs={[run("left", true)]} fresh />);
  expect(screen.getByText("none")).toBeTruthy();
});

it("forgets what ended here once the Page is left", () => {
  const { rerender, unmount } = render(<Page runs={[run("r", false)]} fresh />);
  rerender(<Page runs={[run("r", true)]} fresh />);
  expect(screen.getByText("r:ended")).toBeTruthy();
  unmount();
  render(<Page runs={[run("r", true)]} fresh />);
  expect(screen.getByText("none")).toBeTruthy();
});
