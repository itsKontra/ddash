import { timestampDate } from "@bufbuild/protobuf/wkt";
import { isNotConfigured, useQuery } from "@ddash/plugin";
import { EnergyApiService, Span, type GetFlowResponse, type Totals } from "@ddash/proto/energy-api/energy_api_pb";
import type { ChartColour, Tone } from "@ddash/ui";

/** The power flowing now, and whether energy-api is set up. */
export function useFlow() {
  const q = useQuery(EnergyApiService.method.getFlow, {});
  // Not set up: an empty config (set_up false), or none saved yet.
  const notSetUp = q.data ? !q.data.setUp : isNotConfigured(q.error);
  return { ...q, notSetUp };
}

/** The browser's zone, which days are counted in. */
export const timeZone = () => Intl.DateTimeFormat().resolvedOptions().timeZone;

/** One day, week, month or year holding date (YYYY-MM-DD, today when empty). */
export function usePeriod(span: Span, date: string) {
  return useQuery(EnergyApiService.method.getPeriod, { span, date, timeZone: timeZone() });
}

/** How old the latest reading may be before the flow is stale. */
export const staleAfter = 5 * 60_000;

/** Whether the flow has a reading from the last five minutes. */
export const isFresh = (flow: GetFlowResponse, now = Date.now()) => !!flow.at && now - timestampDate(flow.at).getTime() < staleAfter;

/**
 * Each part's colour, the same on every chart. Slot 5 is red in the Lagoon
 * Theme, so it goes to the heat pump, never to the battery.
 */
export const colours = {
  solar: "chart-4",
  grid: "chart-1",
  export: "chart-6",
  battery: "chart-3",
  house: "chart-2",
  heatPump: "chart-5",
} as const satisfies Record<string, ChartColour>;

const number = (digits: number) => new Intl.NumberFormat(undefined, { maximumFractionDigits: digits, minimumFractionDigits: digits });

/** Power for people: "420 W" under a kW, "3.27 kW", "12.4 kW". */
export function kw(v: number): string {
  const a = Math.abs(v);
  if (a < 1) return `${number(0).format(v * 1000)} W`;
  return `${number(a < 10 ? 2 : 1).format(v)} kW`;
}

/** Energy for people: "4.6 kWh", "86 kWh", "9,854 kWh". */
export function kwh(v: number): string {
  const a = Math.abs(v);
  return `${number(a < 10 ? 1 : 0).format(v)} kWh`;
}

/** An axis's energy label: as kwh without decimals past 10. */
export const kwhAxis = (v: number) => `${number(Math.abs(v) < 10 && v % 1 ? 1 : 0).format(v)} kWh`;
/** An axis's power label. */
export const kwAxis = (v: number) => `${number(Math.abs(v) < 10 && v % 1 ? 1 : 0).format(v)} kW`;

const euroFormat = new Intl.NumberFormat(undefined, { style: "currency", currency: "EUR" });
/** Money: "€4.58" (in the browser's locale). */
export const eur = (v: number) => euroFormat.format(v);

/** A share as "62 %". */
export const pct = (ratio: number) => `${Math.round(ratio * 100)} %`;

/** What a period's totals mean beyond the counters. */
export interface Shares {
  /** How much of the use the house's own solar and battery covered. */
  selfSufficiency?: number;
  /** How much of the solar the house used itself, directly or later from the battery. */
  selfConsumption?: number;
  /** Consumption the Victron doesn't meter as a load: conversion losses, and all of it before the Victron was read. */
  other: number;
  /** Solar used as it was produced. */
  solarDirect: number;
  /** What the house used of its own: solar and battery to the use, in kWh. */
  ownUse: number;
  /**
   * How the split was made: "counters" from the VE.Bus counters every 5
   * minutes, "powers" from the power readings for part of the period that
   * lacks them, "totals" from the period's totals alone.
   */
  basis: "counters" | "powers" | "totals";
}

const ratio = (part: number, whole: number) => (whole > 0 ? Math.min(1, Math.max(0, part / whole)) : undefined);

/**
 * The period's shares, from the flows energy-api attributed every 5
 * minutes. Without them, from its totals: the solar fills the export and
 * the battery first and the rest counts as used directly, which misses
 * any energy that moved both ways in the period.
 */
export function shares(t: Totals): Shares {
  const other = Math.max(0, t.consumption - t.house - t.heatPump);
  const f = t.flows;
  if (f) {
    const ownUse = f.solarUsed + f.batteryUsed;
    return {
      selfSufficiency: ratio(ownUse, ownUse + f.gridUsed),
      selfConsumption: ratio(t.solar - f.solarExport, t.solar),
      other,
      solarDirect: f.solarUsed,
      ownUse,
      basis: f.estimated ? "powers" : "counters",
    };
  }
  return {
    selfSufficiency: ratio(t.consumption - t.gridImport, t.consumption),
    selfConsumption: ratio(t.solar - t.gridExport, t.solar),
    other,
    solarDirect: Math.max(0, t.solar - t.gridExport - t.batteryCharged),
    ownUse: Math.max(0, t.consumption - t.gridImport),
    basis: "totals",
  };
}

/** What a split's basis says about it, or nothing when it is the counters'. */
export function estimate(basis: Shares["basis"]): string | undefined {
  if (basis === "powers") return "Estimated from the power readings where this period lacks the Victron's VE.Bus counters.";
  if (basis === "totals") return "Estimated from the period's totals.";
  return undefined;
}

/** A self-sufficiency's tone: up from 70 %, info from 30 %, else idle. */
export const shareTone = (ratio?: number): Tone => (ratio === undefined ? "idle" : ratio >= 0.7 ? "up" : ratio >= 0.3 ? "info" : "idle");

export { Span };
