import { vi } from "vitest";

/** The energy Plugin's scope in tests. */
export const energy = { name: "energy", requires: ["energy-api"] };

/** The tests' clock: Saturday 14 November 2026, 13:00, in the browser's zone. */
export const now = new Date(2026, 10, 14, 13, 0);

/** Stops Date at now (only Date: the fake core's timers still run). */
export function stopClock() {
  vi.useFakeTimers({ toFake: ["Date"] });
  vi.setSystemTime(now);
}

/** The local time h:m on the day n days from now. */
export const local = (n: number, h = 0, m = 0) => new Date(2026, 10, 14 + n, h, m);

/** What energy-api answers GetFlow with: noon sun charging the battery and exporting the rest. */
export const flow = {
  setUp: true,
  at: local(0, 12, 59).toISOString(),
  power: { solar: 6.5, grid: -1.1, battery: 4.4, house: 0.62, heatPump: 0.07, loads: 0.69, consumption: 1.0 },
  battery: { socPct: 64, voltage: 53.4 },
};

/** A totals message (Connect JSON) with every field, money at the test prices. */
export function totals(solar: number, gridImport: number, gridExport: number, charged: number, discharged: number, house = 0, heatPump = 0) {
  const consumption = Math.max(0, solar + gridImport - gridExport - charged + discharged);
  const cost = gridImport * 0.2418;
  const revenue = gridExport * 0.05786;
  return {
    solar,
    gridImport,
    gridExport,
    batteryCharged: charged,
    batteryDischarged: discharged,
    consumption,
    house,
    heatPump,
    cost,
    revenue,
    savings: Math.max(0, consumption - gridImport) * 0.2418 + revenue,
  };
}

const prices = { buy: 0.2418, sell: 0.05786 };

/** What energy-api answers GetPeriod with for today: two hours of curve, and its totals. */
export const today = {
  setUp: true,
  span: "SPAN_DAY",
  from: local(0).toISOString(),
  to: local(1).toISOString(),
  previous: "2026-11-13",
  next: "",
  totals: totals(41.2, 18.9, 4.6, 15.7, 4, 5.7, 0.7),
  buckets: [
    { from: local(0, 0).toISOString(), to: local(0, 1).toISOString(), totals: totals(0, 2, 0, 0, 0) },
    { from: local(0, 12).toISOString(), to: local(0, 13).toISOString(), totals: totals(6.5, 0, 1.1, 4.4, 0, 0.6, 0.1) },
  ],
  samples: [
    { at: local(0, 12, 0).toISOString(), power: { solar: 6.4, grid: -1.0, battery: 4.4, consumption: 1.0 }, socPct: 60 },
    { at: local(0, 12, 5).toISOString(), power: { solar: 6.5, grid: -1.1, battery: 4.4, consumption: 1.0 }, socPct: 62 },
  ],
  prices,
  firstDay: "2023-10-15",
};

/** What energy-api answers GetPeriod with for August 2026: three days of it. */
export const august = {
  setUp: true,
  span: "SPAN_MONTH",
  from: new Date(2026, 7, 1).toISOString(),
  to: new Date(2026, 8, 1).toISOString(),
  previous: "2026-07-31",
  next: "2026-09-01",
  totals: totals(140, 1.2, 90, 0, 0),
  buckets: [1, 2, 3].map((d) => ({
    from: new Date(2026, 7, d).toISOString(),
    to: new Date(2026, 7, d + 1).toISOString(),
    totals: totals(46.7, 0.4, 30, 0, 0),
  })),
  prices,
  firstDay: "2023-10-15",
};
