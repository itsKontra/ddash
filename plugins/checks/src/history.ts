import { historyBars } from "@ddash/display";
import { useQuery } from "@ddash/plugin";
import { ChecksService, Window, type GetHistoryResponse } from "@ddash/proto/checks/checks_pb";
import type { UptimeBar } from "@ddash/ui";

export { when } from "@ddash/display";

/** The history windows, as the detail names them. */
export const windowNames = ["24 h", "30 d", "365 d"] as const;
export type WindowName = (typeof windowNames)[number];

const hour = 3_600_000;
const day = 24 * hour;

export const windows: Record<WindowName, { window: Window; span: number }> = {
  "24 h": { window: Window.WINDOW_24H, span: day },
  "30 d": { window: Window.WINDOW_30D, span: 30 * day },
  "365 d": { window: Window.WINDOW_365D, span: 365 * day },
};

/** A Check's history over one window, refreshed on the getter's SLOW tier. */
export function useHistory(id: bigint, name: WindowName) {
  return useQuery(ChecksService.method.getHistory, { id, window: windows[name].window });
}

/**
 * An uptime ratio as people read it, floored so 99.999 % never shows as 100 %.
 * checks answers exactly 1 for a window without down beats.
 */
export function percent(ratio: number | undefined): string {
  if (ratio === undefined) return "no data";
  return `${Number((Math.floor(ratio * 10_000) / 100).toFixed(2))} %`;
}

/** One bar per bucket of h's window, oldest first, empty buckets filled in as idle. */
export const bars = (h: GetHistoryResponse, name: WindowName): UptimeBar[] => historyBars(h, windows[name].span);
