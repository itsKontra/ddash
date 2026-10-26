import { useQuery } from "@ddash/plugin";
import { AppsService, WarningKind, type App } from "@ddash/proto/apps/apps_pb";

/** apps' ListApps, for the Apps using each certificate and the Offline Proxy Hosts. */
export function useApps() {
  const { data, error } = useQuery(AppsService.method.listApps, {});
  return { list: data, error };
}

/**
 * How many "Proxy Host offline" Warnings count: those not Ignored, on Apps
 * that aren't Hidden, as Apps' Needs attention lists them.
 */
export const offlineCount = (apps: readonly App[]) =>
  apps.filter((a) => !a.hidden).reduce((n, a) => n + a.warnings.filter((w) => w.kind === WarningKind.PROXY_HOST_OFFLINE && !w.ignored).length, 0);
