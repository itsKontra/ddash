import { cleanup, fireEvent, screen, within } from "@testing-library/react";
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
  window.localStorage.clear();
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
const motion = () => within(now()).getByRole("radiogroup", { name: "Animate flow" });
const picked = () => within(motion()).getByRole("radio", { checked: true }).textContent;
const pick = (name: string) => fireEvent.click(within(motion()).getByRole("radio", { name }));

it("runs the dots on every flowing line in the wide (desktop) layout while motion is allowed", async () => {
  await show();

  expect(now().querySelector("svg")?.getAttribute("viewBox")).toBe("0 0 720 300");
  const flowing = now().querySelectorAll("[data-flowing]");
  expect(flowing.length).toBeGreaterThan(0);
  expect(dots().length).toBe(flowing.length * 3);
  expect(picked()).toBe("Auto");
  expect(screen.queryByText(/asks for reduced motion/)).toBeNull();
});

it("keeps the dots still with reduced motion by default, the coloured lines drawn, and says why", async () => {
  reducedMotion(true);
  await show();

  expect(dots().length).toBe(0);
  expect(now().querySelectorAll("[data-flowing] path").length).toBeGreaterThan(0);
  expect(picked()).toBe("Auto");
  expect(within(now()).getByText("Still: this device asks for reduced motion.")).toBeTruthy();
});

it("runs the dots with reduced motion once On is picked, and stops them at Off", async () => {
  reducedMotion(true);
  await show();

  pick("On");
  expect(dots().length).toBeGreaterThan(0);
  expect(screen.queryByText(/asks for reduced motion/)).toBeNull();

  reducedMotion(false);
  pick("Off");
  expect(dots().length).toBe(0);
});

it("remembers the pick in this browser across a reload, and Auto forgets it", async () => {
  reducedMotion(true);
  await show();
  pick("On");
  cleanup();

  await show();
  expect(picked()).toBe("On");
  expect(dots().length).toBeGreaterThan(0);

  pick("Auto");
  expect(window.localStorage.getItem("ddash.energy.flowMotion")).toBeNull();
  expect(dots().length).toBe(0);
});

it("still applies a pick where the browser can't remember it", async () => {
  vi.spyOn(Storage.prototype, "getItem").mockImplementation(() => {
    throw new Error("blocked");
  });
  vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => {
    throw new Error("blocked");
  });
  reducedMotion(true);
  await show();

  expect(picked()).toBe("Auto");
  pick("On");
  expect(picked()).toBe("On");
  expect(dots().length).toBeGreaterThan(0);
});
