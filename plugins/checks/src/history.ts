import { timestampMs } from "@bufbuild/protobuf/wkt";
import { useQuery } from "@ddash/plugin";
import { ChecksService, Window, type Bucket, type GetHistoryResponse } from "@ddash/proto/checks/checks_pb";
import type { UptimeBar } from "@ddash/ui";

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

/** An uptime ratio as people read it, floored so 99.999 % never shows as 100 %. */
export function percent(ratio: number | undefined): string {
  if (ratio === undefined) return "no data";
  return `${Number((Math.floor(ratio * 10_000) / 100).toFixed(2))} %`;
}

/** How a bucket reads: down if any beat failed, else up, else Maintenance; idle without beats. */
function toneOf(b: Bucket | undefined): UptimeBar["tone"] {
  if (!b) return "idle";
  if (b.down > 0n) return "down";
  if (b.up > 0n) return "up";
  if (b.maintenance > 0n) return "maint";
  return "idle";
}

/** One bar per bucket of h's window, oldest first, empty buckets filled in as idle. */
export function bars(h: GetHistoryResponse, name: WindowName): UptimeBar[] {
  const size = Number(h.bucket?.seconds ?? 60n) * 1000;
  const from = h.from ? timestampMs(h.from) : 0;
  const byStart = new Map(h.buckets.map((b) => [b.start ? timestampMs(b.start) : 0, b]));
  const count = Math.round(windows[name].span / size);
  return Array.from({ length: count }, (_, i) => {
    const start = from + i * size;
    const b = byStart.get(start);
    return { tone: toneOf(b), title: `${when(start, size)} · ${b ? summary(b) : "no beats"}` };
  });
}

function summary(b: Bucket): string {
  const parts = [`${b.up} up`, `${b.down} down`];
  if (b.maintenance > 0n) parts.push(`${b.maintenance} in Maintenance`);
  return parts.join(", ");
}

/** A bucket's start, as precise as its size. */
export function when(ms: number, size = 60_000): string {
  const d = new Date(ms);
  if (size >= day) return d.toLocaleDateString(undefined, { month: "short", day: "numeric", year: "numeric" });
  return d.toLocaleString(undefined, { month: "short", day: "numeric", hour: "2-digit", minute: "2-digit" });
}
