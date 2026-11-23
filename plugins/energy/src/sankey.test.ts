import type { Totals } from "@ddash/proto/energy-api/energy_api_pb";
import { expect, it } from "vitest";
import { fit, flows, layout, type Flow, type NodeId } from "./sankey";
import { todayFlows, totals } from "./testing";

const t = (...a: Parameters<typeof totals>) => totals(...a) as unknown as Totals;
/** The kWh flowing out of a node, or into it. */
const out = (fs: Flow[], id: NodeId) => fs.filter((f) => f.from === id).reduce((a, f) => a + f.kwh, 0);
const into = (fs: Flow[], id: NodeId) => fs.filter((f) => f.to === id).reduce((a, f) => a + f.kwh, 0);

const sunny = t(182.4, 41.3, 96.8, 38.2, 34.1, 71.5, 44.9);
// January: the battery charged from the grid, beyond what the solar gave.
const winter = t(1.5, 148.2, 0.4, 9.8, 8.9, 88, 50);

it.each([
  ["a sunny week", sunny],
  ["a winter month charging from the grid", winter],
])("splits %s so each counter's flows add up to it", (_, x) => {
  const fs = flows(x);

  expect(out(fs, "solar")).toBeCloseTo(x.solar);
  expect(out(fs, "batteryOut")).toBeCloseTo(x.batteryDischarged);
  expect(out(fs, "grid")).toBeCloseTo(x.gridImport);
  expect(into(fs, "export")).toBeCloseTo(x.gridExport);
  expect(into(fs, "batteryIn")).toBeCloseTo(x.batteryCharged);
  expect(into(fs, "used")).toBeCloseTo(x.consumption);
  expect(out(fs, "used")).toBeCloseTo(x.consumption);
  expect(fs.every((f) => f.kwh > 0)).toBe(true);
});

it("draws the sources' bands from the flows energy-api returned, not from the totals", () => {
  const x = t(41.2, 18.9, 4.6, 15.7, 4, 5.7, 0.7);
  const fs = flows({ ...x, flows: todayFlows } as unknown as Totals);
  const band = (from: NodeId, to: NodeId) => fs.find((f) => f.from === from && f.to === to)?.kwh;

  expect(band("solar", "used")).toBe(26);
  expect(band("solar", "batteryIn")).toBe(11);
  expect(band("solar", "export")).toBe(4.2);
  expect(band("batteryOut", "used")).toBe(3.6);
  expect(band("batteryOut", "export")).toBe(0.4);
  expect(band("grid", "used")).toBe(14.2);
  expect(band("grid", "batteryIn")).toBe(4.7);
  // The totals alone would have given the grid no charge at all.
  expect(flows(x).find((f) => f.from === "grid" && f.to === "batteryIn")).toBeUndefined();
  expect(into(fs, "used")).toBeCloseTo(x.consumption);
  expect(out(fs, "used")).toBeCloseTo(x.consumption);
});

it("draws the bands' widths to the period's totals on one scale", () => {
  const fs = flows(sunny);
  const columns: NodeId[][] = [
    ["solar", "batteryOut", "grid"],
    ["export", "batteryIn", "used"],
    ["house", "heatPump", "batteryLosses", "unmetered", "notSplit"],
  ];
  const scale = fit(fs, columns, 260);
  const lay = layout(sunny, fs, columns, [100, 300, 500], 260, scale);
  const band = (from: NodeId, to: NodeId) => lay.bands.find((b) => b.from === from && b.to === to)!.h;
  const node = (id: NodeId) => lay.nodes.find((n) => n.id === id)!;

  expect(lay.bands.filter((b) => b.from === "solar").reduce((a, b) => a + b.h, 0)).toBeCloseTo(sunny.solar * scale);
  expect(lay.bands.filter((b) => b.to === "used").reduce((a, b) => a + b.h, 0)).toBeCloseTo(sunny.consumption * scale);
  expect(node("used").h).toBeCloseTo(sunny.consumption * scale);
  expect(node("used").value).toBe(sunny.consumption);
  expect(band("used", "house") / band("used", "heatPump")).toBeCloseTo(71.5 / 44.9);
  // The tallest column, the sources, fills the height less its gaps.
  expect(node("grid").y + node("grid").h).toBeCloseTo(260);
  expect(node("solar").y).toBeCloseTo(0);
});

it("leaves the battery out of a period without one", () => {
  const fs = flows(t(41.2, 18.9, 4.6, 0, 0, 40, 10));

  expect(fs.some((f) => f.from === "batteryOut" || f.to === "batteryIn")).toBe(false);
});

it("ends at the use when the Victron doesn't split it", () => {
  const fs = flows(t(41.2, 18.9, 4.6, 15.7, 4));

  expect(fs.map((f) => f.to)).not.toContain("house");
  expect(into(fs, "used")).toBeCloseTo(41.2 + 18.9 - 4.6 - 15.7 + 4);
});

it("has no flows in a period with no readings", () => {
  expect(flows(t(0, 0, 0, 0, 0))).toEqual([]);
});

it("splits the use into the house, the heat pump, the battery's losses, the unmetered and what the Victron didn't split", () => {
  const x = { ...t(41.2, 18.9, 4.6, 15.7, 4, 20, 10), unsplit: 6, flows: { ...todayFlows, chargeLoss: 1.3, dischargeLoss: 0.2 } } as unknown as Totals;
  const fs = flows(x);
  const band = (to: NodeId) => fs.find((f) => f.from === "used" && f.to === to)?.kwh;

  expect(band("house")).toBe(20);
  expect(band("heatPump")).toBe(10);
  expect(band("batteryLosses")).toBeCloseTo(1.5);
  expect(band("notSplit")).toBe(6);
  expect(band("unmetered")).toBeCloseTo(x.consumption - 20 - 10 - 1.5 - 6);
  expect(out(fs, "used")).toBeCloseTo(x.consumption);
});

it("ends a period before the Victron's readings in what it didn't split, not in the unmetered", () => {
  const x = t(41.2, 18.9, 4.6, 15.7, 4);
  const fs = flows({ ...x, unsplit: x.consumption } as unknown as Totals);

  expect(fs.filter((f) => f.from === "used")).toEqual([{ from: "used", to: "notSplit", kwh: x.consumption }]);
});

it("counts no more battery losses than the use leaves, so the use's bands add up to it", () => {
  const x = { ...t(10, 5, 0, 2, 2, 9, 5.5), flows: { ...todayFlows, chargeLoss: 1, dischargeLoss: 1 } } as unknown as Totals;
  const fs = flows(x);

  expect(fs.find((f) => f.to === "batteryLosses")?.kwh).toBeCloseTo(0.5);
  expect(fs.some((f) => f.to === "unmetered")).toBe(false);
  expect(out(fs, "used")).toBeCloseTo(x.consumption);
});
