import { timestampMs } from "@bufbuild/protobuf/wkt";
import type { Tone } from "@ddash/plugin";
import type { Bucket, GetHistoryResponse } from "@ddash/proto/checks/checks_pb";

const day = 86_400_000;

/** One bar of a Check's history, as UptimeBars draws it. */
export interface HistoryBar {
  tone: Tone;
  title: string;
}

/** How a bucket reads: down if any beat was down, else warn if one left the Check pending, else up, else Maintenance; idle without beats. */
function toneOf(b: Bucket | undefined): Tone {
  if (!b) return "idle";
  if (b.down > 0n) return "down";
  if (b.pending > 0n) return "warn";
  if (b.up > 0n) return "up";
  if (b.maintenance > 0n) return "maint";
  return "idle";
}

/** One bar per bucket of a history spanning span ms, oldest first, empty buckets filled in as idle. */
export function historyBars(h: GetHistoryResponse, span: number): HistoryBar[] {
  const size = Number(h.bucket?.seconds ?? 60n) * 1000;
  const from = h.from ? timestampMs(h.from) : 0;
  const byStart = new Map(h.buckets.map((b) => [b.start ? timestampMs(b.start) : 0, b]));
  const count = Math.round(span / size);
  return Array.from({ length: count }, (_, i) => {
    const start = from + i * size;
    const b = byStart.get(start);
    return { tone: toneOf(b), title: `${when(start, size)} · ${b ? summary(b) : "no beats"}` };
  });
}

function summary(b: Bucket): string {
  const parts = [`${b.up} up`, `${b.down} down`];
  if (b.pending > 0n) parts.push(`${b.pending} pending`);
  if (b.maintenance > 0n) parts.push(`${b.maintenance} in Maintenance`);
  return parts.join(", ");
}

/** A bucket's start, as precise as its size. */
export function when(ms: number, size = 60_000): string {
  const d = new Date(ms);
  if (size >= day) return d.toLocaleDateString(undefined, { month: "short", day: "numeric", year: "numeric" });
  return d.toLocaleString(undefined, { month: "short", day: "numeric", hour: "2-digit", minute: "2-digit" });
}
