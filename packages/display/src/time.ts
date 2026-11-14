import { timestampMs, type Timestamp } from "@bufbuild/protobuf/wkt";

/** How long before now a time was, e.g. "2 min ago"; "never" when unset. */
export function ago(ts: Timestamp | undefined, now: number): string {
  if (!ts) return "never";
  const mins = Math.floor((now - timestampMs(ts)) / 60_000);
  if (mins < 1) return "<1 min ago";
  if (mins < 60) return `${mins} min ago`;
  const hours = Math.floor(mins / 60);
  if (hours < 48) return `${hours} h ago`;
  return `${Math.floor(hours / 24)} d ago`;
}

/** How long after now a time is, e.g. "in 6 h"; "soon" when it's due. */
export function until(ts: Timestamp | undefined, now: number): string {
  if (!ts) return "never";
  const mins = Math.floor((timestampMs(ts) - now) / 60_000);
  if (mins < 1) return "soon";
  if (mins < 60) return `in ${mins} min`;
  const hours = Math.round(mins / 60);
  if (hours < 48) return `in ${hours} h`;
  return `in ${Math.round(hours / 24)} d`;
}
