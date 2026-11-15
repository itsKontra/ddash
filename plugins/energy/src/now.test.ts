import { create } from "@bufbuild/protobuf";
import { BatterySchema, PowerSchema } from "@ddash/proto/energy-api/energy_api_pb";
import { describe, expect, it } from "vitest";
import { kw } from "./energy";
import { batteryLine, duration, headline } from "./now";

const power = (p: { solar?: number; grid?: number; battery?: number }) => create(PowerSchema, p);
const battery = (socPct?: number, capacityKwh?: number) => create(BatterySchema, { socPct, capacityKwh });

describe("headline", () => {
  it.each([
    // The night: the battery carries the house, the grid meter hovers near zero.
    ["the battery the largest source", { solar: 0, grid: 0.012, battery: -0.612 }, "Running on battery"],
    ["the battery the largest source, a little export", { solar: 0, grid: -0.02, battery: -0.6 }, "Running on battery"],
    ["solar the largest source, charging", { solar: 3, grid: 0.01, battery: 2.2 }, "Running on solar, charging"],
    ["solar the largest source, the battery resting", { solar: 0.8, grid: 0.03, battery: 0.02 }, "Running on solar"],
    ["solar the largest source, the battery helping", { solar: 1.2, grid: 0, battery: -0.4 }, "Running on solar"],
    ["solar the largest source, some import", { solar: 1.2, grid: 0.5, battery: 0 }, "Running on solar"],
    ["feeding the grid", { solar: 6.5, grid: -1.1, battery: 4.4 }, `Exporting ${kw(1.1)}`],
    ["feeding the grid with the battery idle", { solar: 3, grid: -2.4, battery: 0 }, `Exporting ${kw(2.4)}`],
    ["the grid the largest source", { solar: 0, grid: 0.47, battery: 0 }, "Importing from the grid"],
    ["the grid larger than an emptying battery", { solar: 0.1, grid: 1.8, battery: -0.3 }, "Importing from the grid"],
    ["the grid charging the battery", { solar: 0, grid: 3, battery: 2.5 }, "Importing from the grid"],
    ["nothing flowing", { solar: 0, grid: 0, battery: 0 }, "Idle"],
  ])("says so with %s", (_, p, want) => {
    expect(headline(power(p))).toBe(want);
  });

  it("reads a missing reading as none", () => {
    expect(headline(power({ battery: -0.5 }))).toBe("Running on battery");
    expect(headline(power({ grid: -0.5 }))).toBe(`Exporting ${kw(0.5)}`);
  });

  it("says nothing without solar, grid or battery", () => {
    expect(headline(undefined)).toBeUndefined();
    expect(headline(power({}))).toBeUndefined();
  });
});

describe("batteryLine", () => {
  it("adds the time left at the current rate when the capacity is known", () => {
    // 48 % of 15 kWh at 612 W: 11.8 h.
    expect(batteryLine(battery(48, 15), -0.612)).toBe(`48 % · about 12 h left at ${kw(0.612)}`);
  });

  it("adds the time to full while charging", () => {
    // 36 % of 10 kWh to go at 4.4 kW: 49 minutes.
    expect(batteryLine(battery(64, 10), 4.4)).toBe(`64 % · full in about 50 min at ${kw(4.4)}`);
  });

  it("says only what it does without the capacity", () => {
    expect(batteryLine(battery(48), -0.612)).toBe(`48 % · discharging at ${kw(0.612)}`);
    expect(batteryLine(battery(64), 4.4)).toBe(`64 % · charging at ${kw(4.4)}`);
  });

  it("says idle when the battery rests", () => {
    expect(batteryLine(battery(80, 10), 0.004)).toBe("80 % · idle");
  });

  it("says nearly full or empty rather than counting minutes to zero", () => {
    expect(batteryLine(battery(100, 10), 0.3)).toBe(`100 % · nearly full at ${kw(0.3)}`);
    expect(batteryLine(battery(0, 10), -0.3)).toBe(`0 % · nearly empty at ${kw(0.3)}`);
  });

  it("leaves what is missing out", () => {
    expect(batteryLine(battery(48, 15), undefined)).toBe("48 %");
    expect(batteryLine(battery(undefined, 15), -0.6)).toBe(`discharging at ${kw(0.6)}`);
    expect(batteryLine(undefined, undefined)).toBeUndefined();
  });
});

it("rounds a duration to what a person would say", () => {
  expect([0.01, 0.82, 1.4, 11.76, 47, 50, 200].map(duration)).toEqual(["5 min", "50 min", "1 h", "12 h", "47 h", "2 days", "8 days"]);
});
