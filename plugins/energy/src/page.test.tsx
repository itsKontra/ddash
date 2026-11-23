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

it("shows the battery's backup reserve beside its charge once it is configured", async () => {
  await show(undefined, () => Response.json({ ...flow, battery: { ...flow.battery, reservePct: 15 } }));

  expect(panel("Now").textContent).toContain(`Charging ${kw(4.4)} · 53.4 V · reserve 15 %`);
});

it("leaves the backup reserve out while none is configured", async () => {
  await show();

  expect(panel("Now").textContent).toContain(`Charging ${kw(4.4)} · 53.4 V`);
  expect(panel("Now").textContent).not.toContain("reserve");
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

it("counts the money at the prices as a ledger: bought less sold makes the grid bill", async () => {
  await show();

  const money = panel("Money");
  const t = today.totals;
  expect(money.textContent).toContain(`Bought from the grid${kwh(t.gridImport)} × 24.18 ct− ${eur(t.cost)}`);
  expect(money.textContent).toContain(`Sold to the grid${kwh(t.gridExport)} × 5.786 ct+ ${eur(t.revenue)}`);
  expect(money.textContent).toContain(`Grid bill${eur(t.cost - t.revenue)}`);
  expect(within(money).getByRole("img", { name: `The feed-in covered ${pct(t.revenue / t.cost)} of the grid cost` })).toBeTruthy();
  expect(money.textContent).toContain(`Left to pay ${eur(t.cost - t.revenue)}`);
  // The savings split: what wasn't bought, and the feed-in they include.
  expect(money.textContent).toContain(`${eur(t.savings - t.revenue)} not bought by using ${kwh(t.consumption - t.gridImport)} of your own, and ${eur(t.revenue)} from feed-in`);
  expect(money.textContent).toContain(`${eur(t.savings)}saved`);
  expect(money.textContent).toContain(`Buying all ${kwh(t.consumption)} from the grid would have cost ${eur(t.consumption * 0.2418)}`);
});

it("draws the period's energy as a Sankey, each node's counter as text and each flow's kWh on focus", async () => {
  await show();

  const energy = panel("Energy");
  const t = today.totals;
  const node = (id: string) => energy.querySelector(`[data-node="${id}"]`)?.textContent;
  expect(node("solar")).toBe(`Solar${kwh(41.2)}`);
  expect(node("used")).toBe(`Used${kwh(t.consumption)}`);
  expect(node("house")).toBe(`House${kwh(5.7)}`);
  expect(node("other")).toBe(`Lost or unmetered${kwh(t.consumption - 5.7 - 0.7)}`);
  expect(node("batteryIn")).toBe(`Into battery${kwh(15.7)}`);
  const exported = within(energy).getByRole("img", { name: `Solar to Exported: ${kwh(4.6)}` });
  fireEvent.focus(exported);
  expect(within(energy).getByRole("status").textContent).toBe(`Solar → Exported ${kwh(4.6)}`);
  expect(energy.textContent).toContain(`Self-consumption ${pct(1 - 4.6 / 41.2)}`);
});

it("leaves the battery's nodes out of a period without one", async () => {
  await show(() => Response.json({ ...today, totals: totals(41.2, 18.9, 4.6, 0, 0, 40, 10) }));

  const energy = panel("Energy");
  expect(energy.querySelector('[data-node="batteryOut"]')).toBeNull();
  expect(energy.querySelector('[data-node="batteryIn"]')).toBeNull();
  expect(energy.querySelector('[data-node="grid"]')).toBeTruthy();
});

it("says so when a period has no readings", async () => {
  await show(() => Response.json({ ...today, totals: totals(0, 0, 0, 0, 0) }));

  expect(panel("Energy").textContent).toContain("No energy counted in this period.");
  expect(panel("Energy").textContent).toContain("Self-sufficiency –");
  expect(within(panel("Money")).queryByRole("img")).toBeNull();
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

it("keeps a series hidden on the curve across Day and Week, other days and a month between", async () => {
  await show((body) => {
    const span = (body as { span: string }).span;
    return Response.json(span === "SPAN_MONTH" ? august : span === "SPAN_WEEK" ? { ...today, span } : today);
  });
  const used = () => screen.getByRole("button", { name: "Used" });
  const drawn = () => !!screen.getByRole("img", { name: "Power" }).querySelector('[data-series="Used"]');

  fireEvent.click(used());
  expect([used().getAttribute("aria-pressed"), drawn()]).toEqual(["false", false]);

  fireEvent.click(screen.getByRole("radio", { name: "Week" }));
  await settle();
  expect([used().getAttribute("aria-pressed"), drawn()]).toEqual(["false", false]);
  fireEvent.click(screen.getByRole("radio", { name: "Day" }));
  await settle();
  fireEvent.click(screen.getByRole("button", { name: "Previous" }));
  await settle();
  expect([used().getAttribute("aria-pressed"), drawn()]).toEqual(["false", false]);

  // The month's columns replace the curve; the day opened from them hides Used again.
  fireEvent.click(screen.getByRole("radio", { name: "Month" }));
  await settle();
  const name = new Date(2026, 7, 2).toLocaleDateString(undefined, { weekday: "short", day: "numeric", month: "long" });
  fireEvent.click(screen.getByRole("button", { name: `Show ${name}` }));
  await settle();
  expect([used().getAttribute("aria-pressed"), drawn()]).toEqual(["false", false]);

  fireEvent.click(used());
  expect(drawn()).toBe(true);
});

it("says where to set prices when none are", async () => {
  await show(() => Response.json({ ...today, prices: undefined, totals: totals(41.2, 18.9, 4.6, 15.7, 4) }));

  expect(metricValue("Exported", history())).toContain("Set prices in Plugins to see costs");
  const money = panel("Money");
  expect(within(money).getByRole("link", { name: "Set them in Plugins" }).getAttribute("href")).toBe("/plugins");
  expect(within(money).queryByRole("img")).toBeNull();
  expect(money.textContent).not.toContain("Grid bill");
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
  expect(panel("Energy").querySelector('[data-node="solar"]')?.textContent).toBe(`Solar${kwh(41.2)}`);
});
