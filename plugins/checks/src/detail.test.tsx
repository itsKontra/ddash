import { act, cleanup, fireEvent, render, screen, within } from "@testing-library/react";
import { createQueryClient, PluginHost, PluginScope } from "@ddash/plugin";
import { MemoryRouter, useRoutes } from "react-router";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import plugin from "./index";
import { check } from "./testing";

afterEach(cleanup);

const listed = { checks: [check(1, "nas", "STATE_UP", { latencyMs: 12, lastBeat: "2026-10-20T08:00:00Z" })] };

// What checks answers GetHistory with, by window (Connect JSON).
const histories: Record<string, unknown> = {
  WINDOW_24H: {
    window: "WINDOW_24H",
    bucket: "60s",
    from: "2026-10-19T08:01:00Z",
    buckets: [
      { start: "2026-10-20T07:57:00Z", maintenance: "1" },
      { start: "2026-10-20T07:58:00Z", down: "1" },
      { start: "2026-10-20T07:59:00Z", up: "1", latency: { avgMs: 8, minMs: 8, maxMs: 8 } },
      { start: "2026-10-20T08:00:00Z", up: "1", latency: { avgMs: 40, minMs: 40, maxMs: 40 } },
    ],
    changes: [
      { at: "2026-10-20T07:59:00Z", up: true, state: "STATE_UP", changed: true, latencyMs: 8 },
      { at: "2026-10-20T07:58:00Z", state: "STATE_DOWN", changed: true, reason: "connection refused", maintenance: true },
    ],
  },
  WINDOW_30D: {
    window: "WINDOW_30D",
    bucket: "3600s",
    from: "2026-09-20T09:00:00Z",
    buckets: [{ start: "2026-10-20T07:00:00Z", up: "59", down: "1", latency: { avgMs: 12.4, minMs: 8, maxMs: 40 } }],
  },
};
const uptime = [
  { window: "WINDOW_24H", ratio: 0.995, up: "199", down: "1", maintenance: "1", latency: { avgMs: 12.4, minMs: 8, maxMs: 40 } },
  { window: "WINDOW_30D", ratio: 0.99987, up: "7999", down: "1", latency: { avgMs: 12, minMs: 8, maxMs: 40 } },
  { window: "WINDOW_365D", up: "0", down: "0" },
];

let asked: unknown[];
// What GetHistory answers instead of histories, when a test sets it.
let history: (() => Promise<Response>) | undefined;
beforeEach(() => {
  asked = [];
  history = undefined;
});

const fetch = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
  const method = String(input).split("/").pop()!;
  if (method === "ListChecks") return Response.json(listed);
  if (method === "GetHistory") {
    const req = JSON.parse(new TextDecoder().decode(init?.body as Uint8Array));
    asked.push(req);
    if (history) return history();
    return Response.json({ uptime, ...(histories[req.window ?? "WINDOW_24H"] as object) });
  }
  return Response.json({}, { status: 500 });
});

const settle = () => act(() => new Promise((r) => setTimeout(r, 50)));

async function openDetail() {
  const Page = plugin.pages![0].Component!;
  render(
    <MemoryRouter>
      <PluginHost client={createQueryClient()}>
        <PluginScope plugin={{ name: "checks", requires: ["checks"] }} fetch={fetch}>
          <Page />
        </PluginScope>
      </PluginHost>
    </MemoryRouter>,
  );
  await settle();
  fireEvent.click(screen.getByRole("button", { name: "nas" }));
  await settle();
  return screen.getByRole("dialog", { name: "nas" });
}

it("opens the detail of the Check a Notification links to (/checks/<id>)", async () => {
  function Routes() {
    return useRoutes(plugin.pages!);
  }
  render(
    <MemoryRouter initialEntries={["/1"]}>
      <PluginHost client={createQueryClient()}>
        <PluginScope plugin={{ name: "checks", requires: ["checks"] }} fetch={fetch}>
          <Routes />
        </PluginScope>
      </PluginHost>
    </MemoryRouter>,
  );
  await settle();

  expect(await screen.findByRole("dialog", { name: "nas" })).toBeTruthy();
});

const tones = (bars: HTMLElement) => [...bars.querySelectorAll("[data-tone]")].map((b) => b.getAttribute("data-tone"));

it("opens a Check's detail with its uptime over 24 h, 30 d and 365 d", async () => {
  const detail = await openDetail();

  expect(within(detail).getByRole("heading", { name: "Uptime" })).toBeTruthy();
  expect(within(detail).getByRole("group", { name: "24 h" }).textContent).toBe("24 h99.5 %");
  expect(within(detail).getByRole("group", { name: "30 d" }).textContent).toBe("30 d99.98 %");
  expect(within(detail).getByRole("group", { name: "365 d" }).textContent).toBe("365 dno data");
  expect(asked).toEqual([{ id: "1", window: "WINDOW_24H" }]);
});

it("draws skeletons, not text, until the history comes", async () => {
  history = () => new Promise<Response>(() => {});
  const detail = await openDetail();

  expect(within(within(detail).getByRole("group", { name: "24 h" })).getByRole("status", { name: "Loading" })).toBeTruthy();
  expect(within(detail).queryByText("…")).toBeNull();
});

it("says what failed when checks can't answer the history", async () => {
  history = async () => Response.json({ code: "unavailable", message: "checks is down" }, { status: 503 });
  const detail = await openDetail();

  expect(within(detail).getByRole("alert").textContent).toContain("checks is down");
});

it("draws the last 24 h as one bar per minute, buckets without beats idle", async () => {
  const detail = await openDetail();

  const got = tones(within(detail).getByRole("img", { name: "Uptime, last 24 h" }));
  expect(got).toHaveLength(1440);
  expect(got.slice(-5)).toEqual(["idle", "maint", "down", "up", "up"]);
  expect(got.filter((t) => t !== "idle")).toHaveLength(4);
});

it("shows the window's latency and state changes", async () => {
  const detail = await openDetail();

  expect(within(detail).getByRole("heading", { name: "Latency" })).toBeTruthy();
  expect(within(detail).getByRole("group", { name: "average" }).textContent).toBe("average12 ms");
  expect(within(detail).getByRole("group", { name: "min" }).textContent).toBe("min8 ms");
  expect(within(detail).getByRole("group", { name: "max" }).textContent).toBe("max40 ms");

  expect(within(detail).getByRole("heading", { name: "State changes" })).toBeTruthy();
  const changes = within(detail).getAllByRole("listitem");
  expect(changes.map((c) => within(c).getByRole("img").getAttribute("aria-label"))).toEqual(["up", "down"]);
  expect(changes.map((c) => c.textContent)).toEqual([expect.stringMatching(/^up/), expect.stringMatching(/^down.*connection refused.*in Maintenance$/)]);
});

it("switches to 30 d of hour buckets", async () => {
  const detail = await openDetail();

  fireEvent.click(within(detail).getByRole("radio", { name: "30 d" }));
  await settle();

  expect(asked.at(-1)).toEqual({ id: "1", window: "WINDOW_30D" });
  const got = tones(within(detail).getByRole("img", { name: "Uptime, last 30 d" }));
  expect(got).toHaveLength(720);
  expect(got.at(-2)).toBe("down");
  expect(within(detail).getByText("No state changes in the last 30 d.")).toBeTruthy();
});
