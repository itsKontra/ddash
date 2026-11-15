import { cleanup, screen, within } from "@testing-library/react";
import { pending, showWidget, unavailable, type Answer } from "@ddash/plugin/testing";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { eur, kw, kwh } from "./energy";
import plugin from "./index";
import { energy, flow, local, stopClock, today } from "./testing";

beforeEach(stopClock);
afterEach(() => {
  cleanup();
  vi.useRealTimers();
});

const energyNow = () => plugin.widgets!.find((w) => w.id === "energy-now")!;

async function show(getFlow: Answer, size?: "S" | "M" | "L") {
  const { reported } = await showWidget(energy, energyNow(), { size, answers: { GetFlow: getFlow, GetPeriod: () => Response.json(today) } });
  return reported;
}

const widget = () => screen.getByRole("region", { name: "widget" });

/** A reading: the value under its label. */
const reading = (label: string) => within(widget()).getByText(label, { selector: "dt" }).nextElementSibling?.textContent;
/** Whether a reading is dimmed as zero. */
const dimmed = (label: string) => within(widget()).getByText(label, { selector: "dt" }).closest("[data-zero]") !== null;

/** The night: the battery carries the house. */
const night = { ...flow, power: { solar: 0, grid: 0.012, battery: -0.612, house: 0.47, heatPump: 0.075, loads: 0.545, consumption: 0.624 }, battery: { socPct: 48, voltage: 52.3 } };

it("is the Energy now Widget, S, M or L, M by default", () => {
  const w = energyNow();
  expect([w.title, w.sizes, w.defaultSize]).toEqual(["Energy now", ["S", "M", "L"], "M"]);
});

it("reports up with the solar power and the battery's charge", async () => {
  const reported = await show(() => Response.json(flow));

  expect(reported.status).toEqual({ tone: "up", summary: `Solar ${kw(6.5)} · Battery 64 %` });
});

it.each(["S", "M", "L"] as const)("leads with what the readings add up to, then the battery, at %s", async (size) => {
  await show(() => Response.json(flow), size);

  expect(within(widget()).getByText(`Exporting ${kw(1.1)}`)).toBeTruthy();
  expect(within(widget()).getByText(`64 % · charging at ${kw(4.4)}`)).toBeTruthy();
  expect(within(widget()).getByRole("meter", { name: "Battery state of charge" }).getAttribute("aria-valuenow")).toBe("64");
  expect([reading("Solar"), reading("House"), reading("Heat pump"), reading("Export")]).toEqual([kw(6.5), kw(0.62), kw(0.07), kw(1.1)]);
});

it("runs on the battery at night, with the time left once the capacity is known", async () => {
  await show(() => Response.json({ ...night, battery: { ...night.battery, capacityKwh: 15 } }));

  expect(within(widget()).getByText("Running on battery")).toBeTruthy();
  expect(within(widget()).getByText(`48 % · about 12 h left at ${kw(0.612)}`)).toBeTruthy();
});

it("leaves the time left out without the capacity", async () => {
  await show(() => Response.json(night));

  expect(within(widget()).getByText(`48 % · discharging at ${kw(0.612)}`)).toBeTruthy();
  expect(widget().textContent).not.toContain("left");
});

it("dims a reading at zero", async () => {
  await show(() => Response.json(night));

  expect([dimmed("Solar"), dimmed("House"), dimmed("Heat pump"), dimmed("Import")]).toEqual([true, false, false, false]);
  expect(reading("Solar")).toBe(kw(0));
});

it("doesn't conclude from a stale reading", async () => {
  await show(() => Response.json({ ...flow, at: local(0, 12, 40).toISOString() }));

  expect(within(widget()).getByText("No current reading")).toBeTruthy();
  expect(widget().textContent).not.toContain("Exporting");
});

it("keeps today to the curve and one line at M, and leaves it out at S", async () => {
  await show(() => Response.json(flow), "M");

  expect(within(widget()).getByRole("img", { name: "Solar power today" })).toBeTruthy();
  const text = widget().textContent;
  expect(text).toContain(`Today ${kwh(41.2)} solar`);
  expect(text).toContain(`${eur(today.totals.savings)} saved`);
  cleanup();

  await show(() => Response.json(flow), "S");
  expect(within(widget()).queryByRole("img", { name: "Solar power today" })).toBeNull();
  expect(widget().textContent).not.toContain("Today");
});

it("draws no flow diagram: that stays on the Energy Page", async () => {
  await show(() => Response.json(flow), "L");

  expect(within(widget()).queryByRole("group", { name: "Victron" })).toBeNull();
});

it("adds today so far at L", async () => {
  await show(() => Response.json(flow), "L");

  const text = widget().textContent;
  expect(text).toContain(`Solar today${kwh(41.2)}`);
  expect(text).toContain(`Saved${eur(today.totals.savings)}`);
});

it("reports warn when the last reading is older than five minutes", async () => {
  const at = local(0, 12, 40);
  const reported = await show(() => Response.json({ ...flow, at: at.toISOString() }));

  expect(reported.status).toEqual({ tone: "warn", summary: `No reading since ${at.toLocaleTimeString(undefined, { hour: "2-digit", minute: "2-digit" })}` });
});

it("reports idle and links to Plugins while energy-api isn't set up", async () => {
  const reported = await show(() => Response.json({}));

  expect(reported.status).toEqual({ tone: "idle", summary: "Not set up" });
  expect(within(widget()).getByRole("link", { name: "Set it up in Plugins" }).getAttribute("href")).toBe("/plugins");
});

it("reports warn when energy-api is unavailable", async () => {
  const reported = await show(() => unavailable("energy-api is down"));

  expect(reported.status).toEqual({ tone: "warn", summary: "energy-api unavailable: energy-api is down" });
});

it("reports nothing until energy-api first answers", async () => {
  const reported = await show(pending);

  expect(reported.status).toBeUndefined();
});
