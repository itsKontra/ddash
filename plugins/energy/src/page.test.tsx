import { cleanup, fireEvent, screen, within } from "@testing-library/react";
import { metricValue, settle, showPlugin, type Answer } from "@ddash/plugin/testing";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { eur, kw, kwh, pct } from "./energy";
import plugin from "./index";
import { august, energy, flow, stopClock, today, todayFlows, totals, withFlows } from "./testing";

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
  // From the flows: solar and battery to the use, of the use.
  expect(metricValue("Self-sufficiency", history())).toContain(pct((26 + 3.6) / today.totals.consumption));
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
  // The savings split: what wasn't bought, less the grid's charge into the
  // battery (bought already), and the feed-in they include.
  expect(money.textContent).toContain(
    `${eur(29.6 * 0.2418)} not bought by using ${kwh(29.6)} of your own, less ${eur(4.7 * 0.2418)} for the ${kwh(4.7)} the grid charged into the battery, and ${eur(t.revenue)} from feed-in`,
  );
  expect(money.textContent).toContain(`${eur(t.savings)}saved`);
  expect(t.savings).toBeCloseTo((29.6 - 4.7) * 0.2418 + t.revenue);
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
  expect(node("unmetered")).toBe(`Unmetered${kwh(t.consumption - 5.7 - 0.7)}`);
  expect(node("batteryIn")).toBe(`Into battery${kwh(15.7)}`);
  const exported = within(energy).getByRole("img", { name: `Solar to Exported: ${kwh(4.2)}` });
  fireEvent.focus(exported);
  expect(within(energy).getByRole("status").textContent).toBe(`Solar → Exported ${kwh(4.2)}`);
  expect(energy.textContent).toContain(`Self-consumption ${pct(1 - 4.2 / 41.2)}`);
  expect(energy.textContent).not.toContain("estimated");
  expect(energy.textContent).not.toContain("Not split");
});

it("splits the battery's losses out of the unmetered, and says what the Victron didn't split", async () => {
  const t = { ...today.totals, unsplit: 8, flows: { ...todayFlows, chargeLoss: 1.4, dischargeLoss: 0.2 } };
  await show(() => Response.json({ ...today, totals: t }));

  const energy = panel("Energy");
  const node = (id: string) => energy.querySelector(`[data-node="${id}"]`)?.textContent;
  expect(node("batteryLosses")).toBe(`Battery losses${kwh(1.6)}`);
  expect(node("notSplit")).toBe(`Not split${kwh(8)}`);
  expect(node("unmetered")).toBe(`Unmetered${kwh(t.consumption - 8 - 5.7 - 0.7 - 1.6)}`);
  expect(energy.textContent).toContain(`so ${kwh(8)} of the use isn't split into house and heat pump`);
});

// One source of truth: the flows energy-api attributed every 5 minutes.
it("agrees on the own use in the tile, under the Sankey and in the ledger", async () => {
  await show();

  const own = todayFlows.solarUsed + todayFlows.batteryUsed;
  const used = own + todayFlows.gridUsed;
  expect(metricValue("Self-sufficiency", history())).toContain(pct(own / used));
  expect(panel("Energy").textContent).toContain(`Self-sufficiency ${pct(own / used)}`);
  expect(panel("Money").textContent).toContain(`by using ${kwh(own)} of your own`);
  expect(within(panel("Energy")).getByRole("img", { name: `Grid to Into battery: ${kwh(todayFlows.gridBattery)}` })).toBeTruthy();
});

it("says a split is estimated where the VE.Bus counters are missing", async () => {
  await show(() => Response.json({ ...today, totals: withFlows(today.totals, { ...todayFlows, estimated: true }) }));

  expect(panel("Energy").textContent).toContain("Estimated from the power readings where this period lacks");
  expect(metricValue("Self-sufficiency", history())).toContain("estimated");
});

it("splits a period answered without flows by its totals, and says so", async () => {
  await show(() => Response.json({ ...today, totals: totals(41.2, 18.9, 4.6, 15.7, 4, 5.7, 0.7) }));

  const t = totals(41.2, 18.9, 4.6, 15.7, 4, 5.7, 0.7);
  expect(panel("Energy").textContent).toContain("Estimated from the period's totals.");
  expect(metricValue("Self-sufficiency", history())).toContain(pct(1 - 18.9 / t.consumption));
  expect(panel("Money").textContent).toContain(`by using ${kwh(t.consumption - t.gridImport)} of your own, and`);
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
