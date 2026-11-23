import { cleanup, screen } from "@testing-library/react";
import { showPlugin } from "@ddash/plugin/testing";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import plugin from "./index";
import { energy, flow, stopClock, today } from "./testing";

/** Makes the browser report prefers-reduced-motion: reduce, or not. */
function reducedMotion(reduce: boolean) {
  vi.stubGlobal("matchMedia", (query: string) => ({
    matches: reduce && query === "(prefers-reduced-motion: reduce)",
    media: query,
    addEventListener: () => {},
    removeEventListener: () => {},
  }));
}

beforeEach(() => {
  stopClock();
  reducedMotion(false);
});
afterEach(() => {
  cleanup();
  vi.useRealTimers();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

const show = () => showPlugin(plugin, energy, "/energy", { answers: { GetFlow: () => Response.json(flow), GetPeriod: () => Response.json(today) } });

const now = () => screen.getByRole("region", { name: "Now" });
/** The diagram's running dots: each one moves along its line. */
const dots = () => now().querySelectorAll("circle > animateMotion");

/** Each running dot's duration in seconds. */
const durations = () => [...dots()].map((m) => parseFloat(m.getAttribute("dur") ?? ""));

it("runs three dots on every flowing line in the wide (desktop) layout while motion is allowed", async () => {
  await show();

  expect(now().querySelector("svg")?.getAttribute("viewBox")).toBe("0 0 720 300");
  const flowing = now().querySelectorAll("[data-flowing]");
  expect(flowing.length).toBeGreaterThan(0);
  expect(dots().length).toBe(flowing.length * 3);
  expect(screen.queryByRole("radiogroup", { name: "Animate flow" })).toBeNull();
});

it("runs one dot per flowing line, three times slower, with reduced motion", async () => {
  await show();
  const normal = durations();
  cleanup();

  reducedMotion(true);
  await show();

  const flowing = now().querySelectorAll("[data-flowing]");
  expect(dots().length).toBe(flowing.length);
  expect(Math.min(...durations())).toBeCloseTo(Math.min(...normal) * 3);
  expect(screen.queryByText(/reduced motion/)).toBeNull();
});
