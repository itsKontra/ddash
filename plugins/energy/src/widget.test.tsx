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

/** A figure of the S body: the value under its label. */
const figure = (label: string) => within(widget()).getByText(label).nextElementSibling?.textContent;

it("is the Energy now Widget, S, M or L, M by default", () => {
  const w = energyNow();
  expect([w.title, w.sizes, w.defaultSize]).toEqual(["Energy now", ["S", "M", "L"], "M"]);
});

it("reports up with the solar power and the battery's charge", async () => {
  const reported = await show(() => Response.json(flow));

  expect(reported.status).toEqual({ tone: "up", summary: `Solar ${kw(6.5)} · Battery 64 %` });
});

it("shows solar, use, the grid and the battery at S", async () => {
  await show(() => Response.json(flow), "S");

  expect([figure("Solar"), figure("Using"), figure("Export"), figure("Battery")]).toEqual([kw(6.5), kw(1), kw(1.1), "64 %"]);
});

it("draws the flow at M, each part with its power", async () => {
  await show(() => Response.json(flow));

  const part = (name: string) => within(widget()).getByRole("group", { name }).textContent;
  expect(part("Grid")).toContain("Export");
  expect(part("Solar")).toContain(kw(6.5));
  expect(part("Heat pump")).toContain(kw(0.07));
  expect(part("House")).toContain(kw(0.62));
  expect(part("Battery")).toContain("64 %");
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
