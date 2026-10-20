import { timestampMs } from "@bufbuild/protobuf/wkt";
import { useQuery, type Tone } from "@ddash/plugin";
import { ChecksService, State, type Check } from "@ddash/proto/checks/checks_pb";
import type { App } from "@ddash/proto/apps/apps_pb";

/** Every App's Check by App id, refreshed on ListChecks' FAST tier. */
export function useAppChecks() {
  const { data, error } = useQuery(ChecksService.method.listChecks, {});
  const byApp = data && new Map(data.checks.filter((c) => c.appId).map((c) => [c.appId, c]));
  return { byApp, error };
}

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
 * Maintenance, else its state; an up Check whose certificate expires
 * within 14 days warns.
 */
export function checkStatus(c: Check | undefined): CheckStatus {
  if (!c) return { tone: "idle", label: "no Check yet" };
  if (c.disabled) return { tone: "idle", label: "Check disabled" };
  if (c.maintenance) return { tone: "maint", label: "in Maintenance" };
  switch (c.state) {
    case State.UP: {
      const cert = certWarning(c);
      return cert ? { tone: "warn", label: `up, ${cert}` } : { tone: "up", label: "up" };
    }
    case State.DOWN:
      return { tone: "down", label: c.reason ? `down: ${c.reason}` : "down" };
    default:
      return { tone: "warn", label: c.reason ? `pending: ${c.reason}` : "pending" };
  }
}

const worse: Tone[] = ["down", "warn", "maint", "up"];

/**
 * The worst of the Apps' Checks with the Apps in it, or undefined when
 * none has a Check that counts (none yet, or all disabled).
 */
export function worst(apps: readonly App[], byApp: ReadonlyMap<string, Check>) {
  const rated = apps.map((app) => ({ app, status: checkStatus(byApp.get(app.id)) }));
  for (const tone of worse) {
    const hit = rated.filter((r) => r.status.tone === tone);
    if (hit.length) return { tone, hit };
  }
  return undefined;
}
