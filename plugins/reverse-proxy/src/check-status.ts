import { timestampMs } from "@bufbuild/protobuf/wkt";
import type { Tone } from "@ddash/plugin";
import { State, type Check } from "@ddash/proto/checks/checks_pb";

// The same reading of an App's Check as the apps Plugin's cards
// (plugins/apps/src/checks.ts): Plugins share no code but @ddash/*.

/** Whole days until a certificate expires, never below 0. */
export function daysLeft(c: Check, now = Date.now()): number | undefined {
  if (!c.certExpires) return undefined;
  return Math.max(0, Math.floor((timestampMs(c.certExpires) - now) / 86_400_000));
}

/** "certificate expires in 5 days" for a Check whose certificate expires within 14 days. */
export function certWarning(c: Check | undefined): string | undefined {
  if (!c?.certExpiring) return undefined;
  const d = daysLeft(c);
  if (d === undefined) return undefined;
  return d === 0 ? "certificate expires today" : `certificate expires in ${d} ${d === 1 ? "day" : "days"}`;
}

/** What an App's Check says at a glance. */
export interface CheckStatus {
  tone: Tone;
  /** e.g. "up", "down: HTTP 502", "pending", "Check disabled". */
  label: string;
}

/**
 * An App's Check status: idle without one or while disabled, maint in
 * Maintenance, else its state; an up Check that is Flapping or whose
 * certificate expires within 14 days warns. Flapping is in the label.
 */
export function checkStatus(c: Check | undefined): CheckStatus {
  if (!c) return { tone: "idle", label: "no Check yet" };
  if (c.disabled) return { tone: "idle", label: "Check disabled" };
  if (c.maintenance) return { tone: "maint", label: "in Maintenance" };
  switch (c.state) {
    case State.UP: {
      const marks = [c.flapping && "flapping", certWarning(c)].filter(Boolean);
      return marks.length ? { tone: "warn", label: ["up", ...marks].join(", ") } : { tone: "up", label: "up" };
    }
    case State.DOWN: {
      const down = c.flapping ? "down, flapping" : "down";
      return { tone: "down", label: c.reason ? `${down}: ${c.reason}` : down };
    }
    default:
      return { tone: "warn", label: c.reason ? `pending: ${c.reason}` : "pending" };
  }
}
