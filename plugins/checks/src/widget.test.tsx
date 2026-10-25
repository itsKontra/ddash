import { cleanup, screen, within } from "@testing-library/react";
import type { Badge, Status, Widget } from "@ddash/plugin";
import { metricValue, pending, renderScoped, settle, unavailable, type Answer } from "@ddash/plugin/testing";
import { afterEach, expect, it } from "vitest";
import plugin from "./index";
import { check, checks } from "./testing";

afterEach(cleanup);

const summary = () => plugin.widgets!.find((w) => w.id === "summary")!;

const listed = (...checks: object[]) => () => Response.json({ checks });

// Renders the summary Widget's body at size and the Nav Entry's badge,
// against checks answering with answer, and returns what they reported last.
async function show(answer: Answer, size: "S" | "M" = "S") {
  const w = summary();
  const reported: { status?: Status; badge?: Badge | null } = {};
  function Probe({ widget }: { widget: Widget }) {
    reported.status = widget.useStatus();
    reported.badge = plugin.nav!.useBadge!();
    return null;
  }
  renderScoped(
    checks,
    <>
      <Probe widget={w} />
      <section aria-label="widget">
        <w.Body size={size} />
      </section>
    </>,
    { answers: { ListChecks: answer } },
  );
  await settle();
  return reported;
}

// A MetricTile's value in the Widget.
const metric = (label: string) => metricValue(label, screen.getByRole("region", { name: "widget" }));

it("is the summary Widget, S or M, S by default", () => {
  const w = summary();
  expect([w.title, w.sizes, w.defaultSize]).toEqual(["Checks", ["S", "M"], "S"]);
});

it("reports up with the count while every Check is up", async () => {
  const reported = await show(listed(check(1, "nas", "STATE_UP"), check(2, "router", "STATE_UP"), check(3, "nvr", "STATE_PENDING")));

  expect(reported.status).toEqual({ tone: "up", summary: "2 of 3 up" });
  expect(reported.badge).toBeNull();
  expect([metric("up"), metric("down"), metric("Maintenance")]).toEqual(["2", "0", "0"]);
});

it("reports down, naming what's down, with an N down badge", async () => {
  const reported = await show(
    listed(check(1, "nas", "STATE_DOWN"), check(2, "router", "STATE_UP"), check(3, "", "STATE_DOWN", { spec: { type: "CHECK_TYPE_ICMP", target: "198.18.1.5" } })),
  );

  expect(reported.status).toEqual({ tone: "down", summary: "2 down: nas, 198.18.1.5" });
  expect(reported.badge).toEqual({ text: "2 down", tone: "down" });
  expect([metric("up"), metric("down")]).toEqual(["1", "2"]);
});

it("counts a Check in Maintenance as maint, never as down", async () => {
  const reported = await show(listed(check(1, "nas", "STATE_DOWN", { maintenance: true }), check(2, "router", "STATE_UP")));

  expect(reported.status).toEqual({ tone: "maint", summary: "1 in Maintenance" });
  expect(reported.badge).toBeNull();
  expect([metric("up"), metric("down"), metric("Maintenance")]).toEqual(["1", "0", "1"]);
});

it("reports warn, naming them, for a Flapping Check that is up and an expiring certificate", async () => {
  const reported = await show(
    listed(
      check(1, "nas", "STATE_UP", { flapping: true }),
      check(2, "Jellyfin", "STATE_UP", { certExpiring: true }),
      check(3, "router", "STATE_UP"),
      check(4, "nvr", "STATE_UP", { maintenance: true }),
    ),
  );

  expect(reported.status).toEqual({ tone: "warn", summary: "nas flapping; Jellyfin certificate expiring" });
  expect(reported.badge).toBeNull();
});

it("reports a Flapping Check that is down as down", async () => {
  const reported = await show(listed(check(1, "nas", "STATE_DOWN", { flapping: true }), check(2, "router", "STATE_UP")));

  expect(reported.status).toEqual({ tone: "down", summary: "1 down: nas" });
});

it("leaves disabled Checks out", async () => {
  const reported = await show(listed(check(1, "nas", "STATE_DOWN", { disabled: true }), check(2, "router", "STATE_UP")));

  expect(reported.status).toEqual({ tone: "up", summary: "1 of 1 up" });
  expect(reported.badge).toBeNull();
});

it("is idle without Checks", async () => {
  const reported = await show(listed());

  expect(reported.status).toEqual({ tone: "idle", summary: "No Checks yet" });
});

it("lists what isn't up at M", async () => {
  await show(listed(check(1, "nas", "STATE_DOWN", { reason: "connection refused" }), check(2, "router", "STATE_UP")), "M");

  const nas = within(screen.getByRole("region", { name: "widget" })).getByRole("listitem");
  expect(within(nas).getByRole("img").getAttribute("aria-label")).toBe("down");
  expect(nas.textContent).toMatch(/nas.*connection refused/);
});

it("reports warn when checks is unavailable", async () => {
  const reported = await show(() => unavailable("checks is down"));

  expect(reported.status).toEqual({ tone: "warn", summary: "checks unavailable: checks is down" });
});

it("reports nothing and draws skeletons until checks first answers", async () => {
  const reported = await show(pending);

  expect(reported.status).toBeUndefined();
  expect(reported.badge).toBeNull();
  expect(within(screen.getByRole("region", { name: "widget" })).getAllByRole("status", { name: "Loading" })).toHaveLength(3);
});
