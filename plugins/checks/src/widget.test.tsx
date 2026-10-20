import { act, cleanup, render, screen, within } from "@testing-library/react";
import { createQueryClient, PluginHost, PluginScope, type Badge, type Status, type Widget } from "@ddash/plugin";
import { afterEach, expect, it, vi } from "vitest";
import plugin from "./index";
import { check } from "./testing";

afterEach(cleanup);

const summary = () => plugin.widgets!.find((w) => w.id === "summary")!;

const listed = (...checks: object[]) => Response.json({ checks });

// Renders the summary Widget's body at size and the Nav Entry's badge,
// against checks answering with answer, and returns what they reported last.
async function show(answer: Response | (() => Response), size: "S" | "M" = "S") {
  const w = summary();
  const fetch = vi.fn(async () => (typeof answer === "function" ? answer() : answer.clone()));
  const reported: { status?: Status; badge?: Badge | null } = {};
  function Probe({ widget }: { widget: Widget }) {
    reported.status = widget.useStatus();
    reported.badge = plugin.nav!.useBadge!();
    return null;
  }
  render(
    <PluginHost client={createQueryClient()}>
      <PluginScope plugin={{ name: "checks", requires: ["checks"] }} fetch={fetch}>
        <Probe widget={w} />
        <section aria-label="widget">
          <w.Body size={size} />
        </section>
      </PluginScope>
    </PluginHost>,
  );
  await act(() => new Promise((r) => setTimeout(r, 50)));
  return reported;
}

const stat = (label: string) => within(screen.getByRole("region", { name: "widget" })).getByText(label).previousSibling?.textContent;

it("is the summary Widget, S or M, S by default", () => {
  const w = summary();
  expect([w.title, w.sizes, w.defaultSize]).toEqual(["Checks", ["S", "M"], "S"]);
});

it("reports up with the count while every Check is up", async () => {
  const reported = await show(listed(check(1, "nas", "STATE_UP"), check(2, "router", "STATE_UP"), check(3, "nvr", "STATE_PENDING")));

  expect(reported.status).toEqual({ tone: "up", summary: "2 of 3 up" });
  expect(reported.badge).toBeNull();
  expect([stat("up"), stat("down"), stat("Maintenance")]).toEqual(["2", "0", "0"]);
});

it("reports down, naming what's down, with an N down badge", async () => {
  const reported = await show(
    listed(check(1, "nas", "STATE_DOWN"), check(2, "router", "STATE_UP"), check(3, "", "STATE_DOWN", { spec: { type: "CHECK_TYPE_ICMP", target: "198.18.1.5" } })),
  );

  expect(reported.status).toEqual({ tone: "down", summary: "2 down: nas, 198.18.1.5" });
  expect(reported.badge).toEqual({ count: 2, tone: "down" });
  expect([stat("up"), stat("down")]).toEqual(["1", "2"]);
});

it("counts a Check in Maintenance as maint, never as down", async () => {
  const reported = await show(listed(check(1, "nas", "STATE_DOWN", { maintenance: true }), check(2, "router", "STATE_UP")));

  expect(reported.status).toEqual({ tone: "maint", summary: "1 in Maintenance" });
  expect(reported.badge).toBeNull();
  expect([stat("up"), stat("down"), stat("Maintenance")]).toEqual(["1", "0", "1"]);
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
  expect(nas.textContent).toMatch(/nas.*connection refused/);
});

it("reports warn when checks is unavailable", async () => {
  const reported = await show(() => Response.json({ code: "unavailable", message: "checks is down" }, { status: 503 }));

  expect(reported.status).toEqual({ tone: "warn", summary: "checks unavailable: checks is down" });
});

it("reports nothing until checks first answers", async () => {
  const reported = await show(() => new Promise<Response>(() => {}) as unknown as Response);

  expect(reported.status).toBeUndefined();
  expect(reported.badge).toBeNull();
});
