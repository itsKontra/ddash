import { cleanup, fireEvent, screen, within } from "@testing-library/react";
import { metricValue, settle, showPlugin, type Answer } from "@ddash/plugin/testing";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { eur, kw, kwh, pct } from "./energy";
import plugin from "./index";
import { august, energy, flow, stopClock, today, totals } from "./testing";

beforeEach(stopClock);
afterEach(() => {
  cleanup();
  vi.useRealTimers();
});

const show = (getPeriod: Answer = () => Response.json(today), getFlow: Answer = () => Response.json(flow)) =>
  showPlugin(plugin, energy, "/energy", { answers: { GetFlow: getFlow, GetPeriod: getPeriod } });

const panel = (name: string | RegExp) => screen.getByRole("region", { name });
/** The history's Panel: the one picking the period. */
const history = () => screen.getByRole("radiogroup", { name: "Period" }).closest("section")!;
/** A Facts value by its label. */
const fact = (region: HTMLElement, label: string) => within(region).getByText(label, { selector: "dt" }).nextElementSibling?.textContent;

it("is the Energy Nav Entry at order 65 (its icon, sun, is in plugin.yaml)", () => {
  expect(plugin.nav?.order).toBe(65);
});

it("shows the flow now, and what the house uses split by the Victron", async () => {
  await show();

  const now = panel("Now");
  expect(within(now).getByRole("group", { name: "Battery" }).textContent).toContain(`Charging ${kw(4.4)}`);
  expect(now.textContent).toContain(`Using${kw(1)}`);
  expect(fact(now, "House")).toBe(kw(0.62));
  expect(fact(now, "Heat pump")).toBe(kw(0.07));
  // What the inverters use beyond the loads: consumption less loads.
  expect(fact(now, "Conversion losses")).toBe(kw(1 - 0.69));
  expect(within(now).getByRole("meter", { name: "Battery state of charge" }).getAttribute("aria-valuenow")).toBe("64");
});

it("asks for today in the browser's zone, and shows its figures", async () => {
  const calls = await show();

  const asked = calls.find((c) => c.method === "GetPeriod")!;
  expect(asked.url).toBe("/api/c/energy-api/ddash.energyapi.EnergyApiService/GetPeriod");
  expect(asked.body).toEqual({ span: "SPAN_DAY", timeZone: Intl.DateTimeFormat().resolvedOptions().timeZone });
  expect(metricValue("Solar", history())).toContain(kwh(41.2));
  expect(metricValue("Used", history())).toContain(kwh(today.totals.consumption));
  expect(metricValue("Self-sufficiency", history())).toContain(pct(1 - 18.9 / today.totals.consumption));
  expect(metricValue("Saved", history())).toContain(eur(today.totals.savings));
  expect(screen.getByRole("img", { name: "Power" })).toBeTruthy();
  expect(screen.getByRole("img", { name: "State of charge" })).toBeTruthy();
});

it("counts the money at the prices", async () => {
  await show();

  const money = panel("Money");
  const t = today.totals;
  expect(fact(money, "Grid cost")).toBe(eur(t.cost));
  expect(fact(money, "Grid bill")).toBe(eur(t.cost - t.revenue));
  expect(fact(money, "Saved by solar")).toBe(eur(t.savings));
  expect(fact(panel("Energy"), "… lost or unmetered")).toBe(kwh(t.consumption - 5.7 - 0.7));
});

it("says the feed-in earned more than the grid cost, when it did", async () => {
  await show(() => Response.json(august));

  expect(fact(panel("Money"), "Earned net")).toBe(eur(august.totals.revenue - august.totals.cost));
});

it("steps to the period before, and back to today", async () => {
  const calls = await show();

  fireEvent.click(screen.getByRole("button", { name: "Previous" }));
  await settle();

  const last = () => calls.filter((c) => c.method === "GetPeriod").at(-1)!.body as { date?: string };
  expect(last().date).toBe("2026-11-13");
  fireEvent.click(screen.getByRole("button", { name: "Today" }));
  await settle();
  expect(last().date).toBeUndefined();
});

it("shows a month's days as columns, and a day's column opens it", async () => {
  const calls = await show((body) => Response.json((body as { span: string }).span === "SPAN_MONTH" ? august : today));

  fireEvent.click(screen.getByRole("radio", { name: "Month" }));
  await settle();
  expect(screen.getByRole("img", { name: "Energy by day" })).toBeTruthy();
  const name = new Date(2026, 7, 2).toLocaleDateString(undefined, { weekday: "short", day: "numeric", month: "long" });
  fireEvent.click(screen.getByRole("button", { name: `Show ${name}` }));
  await settle();

  expect(calls.filter((c) => c.method === "GetPeriod").at(-1)!.body).toMatchObject({ span: "SPAN_DAY", date: "2026-08-02" });
});

it("says where to set prices when none are", async () => {
  await show(() => Response.json({ ...today, prices: undefined, totals: totals(41.2, 18.9, 4.6, 15.7, 4) }));

  expect(metricValue("Exported", history())).toContain("Set prices in Plugins to see costs");
  expect(within(panel("Money")).getByRole("link", { name: "Set them in Plugins" }).getAttribute("href")).toBe("/plugins");
});

it("asks to set up energy-api while it isn't", async () => {
  await show(() => Response.json({}), () => Response.json({}));

  expect(screen.getByText("Set up energy-api")).toBeTruthy();
  expect(screen.getByRole("link", { name: "Configure it in Plugins" }).getAttribute("href")).toBe("/plugins");
});

it("still shows a period's figures when the answer lacks its ends", async () => {
  const { from: _from, to: _to, ...endless } = today as Record<string, unknown>;
  await show(() => Response.json(endless));

  expect(screen.queryByText(/Energy crashed/)).toBeNull();
  expect(fact(panel("Energy"), "Solar")).toBeTruthy();
});
