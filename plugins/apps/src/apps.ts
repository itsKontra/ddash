import { useQuery, type Badge } from "@ddash/plugin";
import { AppsService, WarningKind, type App, type Warning } from "@ddash/proto/apps/apps_pb";

/** ListApps, shared by the Page, the Widget and the badge. */
export function useApps() {
  const { data, error } = useQuery(AppsService.method.listApps, {});
  return { list: data, error };
}

/** The title of the section ungrouped Apps show in, after the groups. */
export const ungrouped = "Ungrouped";

/** One group's Apps, in list order. */
export interface Section {
  title: string;
  apps: App[];
}

/** The visible Apps by group, groups by name, then the ungrouped ones. Orphaned App settings are no section's. */
export function sections(apps: readonly App[]): Section[] {
  const by = new Map<string, App[]>();
  for (const a of apps) {
    if (a.hidden || a.orphaned) continue;
    const list = by.get(a.group) ?? [];
    list.push(a);
    by.set(a.group, list);
  }
  const named = [...by.keys()].filter((g) => g !== "").sort((a, b) => a.localeCompare(b, undefined, { sensitivity: "base" }));
  const out = named.map((g) => ({ title: g, apps: by.get(g)! }));
  if (by.has("")) out.push({ title: ungrouped, apps: by.get("")! });
  return out;
}

/** The Apps the Widget shows: every visible one, in list order; orphaned App settings open nothing. */
export const visible = (apps: readonly App[]) => apps.filter((a) => !a.hidden && !a.orphaned);

/** An App's Warnings that count: those not Ignored, none on a Hidden App. */
export const counting = (app: App): Warning[] => (app.hidden ? [] : app.warnings.filter((w) => !w.ignored));

/** Each App with Warnings that count, and them. */
export const warned = (apps: readonly App[]) => apps.map((app) => ({ app, warnings: counting(app) })).filter((w) => w.warnings.length > 0);

/** A Warning as a card says it. */
export const warningText: Record<WarningKind, string> = {
  [WarningKind.UNSPECIFIED]: "",
  [WarningKind.NO_WORKLOAD]: "No Workload",
  [WarningKind.NO_TLS]: "No TLS",
  [WarningKind.PROXY_HOST_OFFLINE]: "Proxy Host offline",
  [WarningKind.URL_NOT_ON_PROXY_HOST]: "URL not on the Proxy Host",
  [WarningKind.ORPHANED]: "Orphaned App settings",
  [WarningKind.NOT_REGISTERED]: "Not registered",
  [WarningKind.NO_PROXY_HOST]: "No Proxy Host",
  [WarningKind.DNS_BYPASSES]: "DNS bypasses the Reverse Proxy",
  [WarningKind.REGISTERED_TWICE]: "Registered twice",
  [WarningKind.HOSTNAME_ON_TWO_APPS]: "Hostname on two Apps",
};

/** The Nav Entry's badge: how many Warnings count, in warn. */
export function useWarningsBadge(): Badge | null {
  const { list } = useApps();
  const n = (list?.apps ?? []).reduce((sum, a) => sum + counting(a).length, 0);
  return n > 0 ? { count: n, tone: "warn" } : null;
}
