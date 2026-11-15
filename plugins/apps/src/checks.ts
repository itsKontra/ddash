import { checkStatus } from "@ddash/display";
import { useQuery, type Tone } from "@ddash/plugin";
import { ChecksService, type Check } from "@ddash/proto/checks/checks_pb";
import type { App } from "@ddash/proto/apps/apps_pb";

/** Every App's Check by App id, refreshed on ListChecks' FAST tier, and when checks last answered. */
export function useAppChecks() {
  const { data, error, dataUpdatedAt } = useQuery(ChecksService.method.listChecks, {});
  const byApp = data && new Map(data.checks.filter((c) => c.appId).map((c) => [c.appId, c]));
  return { byApp, error, answeredAt: dataUpdatedAt };
}

const worse: Tone[] = ["down", "warn", "maint", "up"];

/** Whether c is down but muted: the Operator knows, so it doesn't count as down. */
export const mutedDown = (c: Check | undefined) => !!c?.muted && checkStatus(c).tone === "down";

/**
 * The worst of the Apps' Checks with the Apps in it, or undefined when
 * none has a Check that counts (none yet, all disabled or pending, or
 * down but muted).
 */
export function worst(apps: readonly App[], byApp: ReadonlyMap<string, Check>) {
  const rated = apps.filter((app) => !mutedDown(byApp.get(app.id))).map((app) => ({ app, status: checkStatus(byApp.get(app.id)) }));
  for (const tone of worse) {
    const hit = rated.filter((r) => r.status.tone === tone);
    if (hit.length) return { tone, hit };
  }
  return undefined;
}
