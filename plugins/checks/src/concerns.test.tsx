import { act, cleanup, fireEvent, screen, within } from "@testing-library/react";
import type { Concern } from "@ddash/plugin";
import { pending, renderScoped, settle, unavailable, type Answer } from "@ddash/plugin/testing";
import { useState } from "react";
import { afterEach, expect, it } from "vitest";
import plugin from "./index";
import { check, checks } from "./testing";

afterEach(cleanup);

const listed = (...checks: object[]) => () => Response.json({ checks });

// What useConcerns reported last, without its Actions' functions.
let reported: Concern[] | undefined;
const plain = () => reported?.map(({ actions, ...c }) => ({ ...c, actions: actions?.map((a) => a.label) }));

// Runs the checks Plugin's useConcerns as the Overview does, drawing the
// Sheet of the Action asked for with open(id, label).
let open: (id: string, label: string) => void = () => {};
function Probe() {
  const concerns = plugin.useConcerns!();
  const [shown, setShown] = useState<{ id: string; label: string } | null>(null);
  reported = concerns;
  open = (id, label) => setShown({ id, label });
  const action = shown && concerns?.find((c) => c.id === shown.id)?.actions?.find((a) => a.label === shown.label);
  return action && "sheet" in action ? action.sheet(() => setShown(null)) : null;
}

async function show(answer: Answer) {
  const calls = renderScoped(checks, <Probe />, { answers: { ListChecks: answer } });
  await settle();
  return calls;
}

const ago = (mins: number) => new Date(Date.now() - mins * 60_000).toISOString();

it("reports each enabled down Check, quiet when muted or in Maintenance", async () => {
  await show(
    listed(
      check(1, "nas", "STATE_DOWN", { reason: "timeout", since: ago(6) }),
      check(2, "router", "STATE_UP"),
      check(3, "backup", "STATE_DOWN", { reason: "connection refused", since: ago(130), muted: true }),
      check(4, "nvr", "STATE_DOWN", { since: ago(20), maintenance: true, muted: true }),
      check(5, "printer", "STATE_DOWN", { disabled: true }),
      check(6, "camera", "STATE_PENDING", { reason: "timeout" }),
    ),
  );

  expect(plain()).toEqual([
    { id: "1", tone: "down", subject: "nas", title: "nas is down", cause: "timeout · 6 min ago", quiet: undefined, href: "/checks/1", actions: ["Mute", "Maintenance"] },
    { id: "3", tone: "down", subject: "backup", title: "backup is down", cause: "connection refused · 2 h ago", quiet: "muted", href: "/checks/3", actions: undefined },
    { id: "4", tone: "down", subject: "nvr", title: "nvr is down", cause: "down · 20 min ago", quiet: "in Maintenance", href: "/checks/4", actions: undefined },
  ]);
});

it("reports none while every Check is up, and nothing until checks answered", async () => {
  await show(listed(check(1, "nas", "STATE_UP")));
  expect(reported).toEqual([]);

  cleanup();
  await show(() => pending());
  expect(reported).toBeUndefined();
});

it("warns when checks can't be read", async () => {
  await show(() => unavailable("checks is restarting"));

  expect(plain()).toEqual([
    { id: "unavailable", tone: "warn", subject: "Checks", title: "Checks can't be read", cause: "checks is restarting", href: "/checks", actions: undefined },
  ]);
});

it("mutes a down Check as its row on the Page does", async () => {
  const calls = await show(listed(check(1, "nas", "STATE_DOWN", { reason: "timeout" })));

  const mute = reported![0].actions![0];
  await act(async () => void ("run" in mute && (await mute.run())));
  await settle();

  expect(calls.filter((c) => c.method === "SetMuted").map((c) => c.body)).toEqual([{ id: "1", muted: true }]);
});

it("opens the Page's Maintenance Sheet for a down Check", async () => {
  const calls = await show(listed(check(1, "nas", "STATE_DOWN", { reason: "timeout" })));

  act(() => open("1", "Maintenance"));
  const sheet = screen.getByRole("dialog", { name: "Maintenance for nas" });
  fireEvent.click(within(sheet).getByRole("button", { name: "Start" }));
  await settle();

  expect(calls.filter((c) => c.method === "StartMaintenance").map((c) => c.body)).toEqual([{ scope: { checkId: "1" }, duration: "3600s" }]);
});
