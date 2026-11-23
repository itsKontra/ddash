import type { Battery, Power } from "@ddash/proto/energy-api/energy_api_pb";
import { kw } from "./energy";

/** Below this a power counts as nothing. */
export const idle = 0.01;

/**
 * Below this the grid counts as balanced and the battery as resting: the
 * Victron holds the grid meter near zero, never at it.
 */
export const quiet = 0.05;

/**
 * What the readings add up to, in words: "Exporting 1.10 kW" while
 * feeding the grid, else named after the largest source: "Running on
 * battery", "Running on solar" (", charging" while the battery takes
 * some) or "Importing from the grid". Undefined without a reading.
 */
export function headline(p: Power | undefined): string | undefined {
  if (!p || (p.solar === undefined && p.grid === undefined && p.battery === undefined)) return undefined;
  const grid = p.grid ?? 0;
  const battery = p.battery ?? 0;
  if (grid <= -quiet) return `Exporting ${kw(-grid)}`;
  const fromSolar = Math.max(0, p.solar ?? 0);
  const fromBattery = Math.max(0, -battery);
  const fromGrid = Math.max(0, grid);
  const top = Math.max(fromSolar, fromBattery, fromGrid);
  if (top < idle) return "Idle";
  if (fromBattery === top) return "Running on battery";
  if (fromSolar === top) return battery >= quiet ? "Running on solar, charging" : "Running on solar";
  return "Importing from the grid";
}

/** How long, for people: "40 min", "12 h", "3 days". */
export function duration(hours: number): string {
  if (hours < 1) return `${Math.max(5, Math.round((hours * 60) / 5) * 5)} min`;
  if (hours < 48) return `${Math.round(hours)} h`;
  return `${Math.round(hours / 24)} days`;
}

/**
 * The battery in one line: its charge, and what it does at its power now.
 * With its capacity, how long until it is full, or down to its backup
 * reserve (0 % without one), at that rate: "48 % · about 12 h left at
 * 612 W", "64 % · full in about 2 h at 4.40 kW"; without, "discharging at
 * 612 W". At its reserve, "15 % · at backup reserve, discharging at
 * 300 W". Undefined without a reading.
 */
export function batteryLine(b: Battery | undefined, power: number | undefined): string | undefined {
  const soc = b?.socPct;
  if (soc === undefined && power === undefined) return undefined;
  const parts = soc === undefined ? [] : [`${Math.round(soc)} %`];
  if (power !== undefined) parts.push(doing(soc, power, b?.capacityKwh, b?.reservePct));
  return parts.join(" · ");
}

function doing(soc: number | undefined, power: number, capacity: number | undefined, reserve: number | undefined): string {
  const at = kw(Math.abs(power));
  if (Math.abs(power) < idle) return "idle";
  const charging = power > 0;
  const floor = reserve ?? 0;
  if (!charging && floor > 0 && soc !== undefined && soc <= floor) return `at backup reserve, discharging at ${at}`;
  if (soc === undefined || !capacity) return `${charging ? "charging" : "discharging"} at ${at}`;
  // The energy still to go in or come out, at today's rate.
  const hours = ((charging ? 100 - soc : soc - floor) / 100) * (capacity / Math.abs(power));
  if (hours * 60 < 2.5) return `${charging ? "nearly full" : floor > 0 ? "nearly at backup reserve" : "nearly empty"} at ${at}`;
  return charging ? `full in about ${duration(hours)} at ${at}` : `about ${duration(hours)} left at ${at}`;
}
