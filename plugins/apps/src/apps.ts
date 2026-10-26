import { useQuery, type Badge } from "@ddash/plugin";
import { AppsService, FixKind, WarningKind, type App, type ListAppsResponse, type Warning } from "@ddash/proto/apps/apps_pb";
import { matches } from "@ddash/ui";

/** ListApps, shared by the Page, the Widget and the badge, and when it last answered. */
export function useApps() {
  const { data, error, dataUpdatedAt } = useQuery(AppsService.method.listApps, {});
  return { list: data, error, answeredAt: dataUpdatedAt };
}

/** The title of the section ungrouped Apps show in, after the groups. */
export const ungrouped = "Ungrouped";

/** One group's Apps, in list order. */
export interface Section {
  title: string;
  apps: App[];
}

/** The Apps that aren't Hidden by group, groups by name, then the ungrouped ones. */
export function sections(apps: readonly App[]): Section[] {
  const by = new Map<string, App[]>();
  for (const a of apps) {
    if (a.hidden) continue;
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

/** The Warnings the pane and Needs attention show: those that count, and with showIgnored the Ignored ones too. None on a Hidden App lists it in Needs attention. */
export const shownWarnings = (app: App, showIgnored: boolean): Warning[] => app.warnings.filter((w) => showIgnored || !w.ignored);

/** Whether an App matches the search: its name, its URL's host, its aliases or its group. */
export const found = (q: string, app: App) => matches(q, app.name, URL.parse(app.url)?.hostname, ...app.aliases, app.group);

/** The list on the left, as one search and the Show ignored Warnings toggle leave it. */
export interface ListModel {
  /** Apps that aren't Hidden with Warnings that count (or Ignored ones, shown), pinned on top. */
  attention: App[];
  /** Every App that isn't Hidden, by group. */
  groups: Section[];
  hidden: App[];
  /** How many Warnings are Ignored on Apps that aren't Hidden, whatever the search. */
  ignored: number;
}

export function listModel(apps: readonly App[], q: string, showIgnored: boolean): ListModel {
  const shown = apps.filter((a) => found(q, a));
  return {
    attention: shown.filter((a) => !a.hidden && shownWarnings(a, showIgnored).length > 0),
    groups: sections(shown),
    hidden: shown.filter((a) => a.hidden),
    ignored: apps.filter((a) => !a.hidden).reduce((n, a) => n + a.warnings.filter((w) => w.ignored).length, 0),
  };
}

/** When the Apps were last read: the older of the two sources' last good reads; undefined before both were read. */
export function readAt(list: ListAppsResponse) {
  const times = [list.proxyHosts?.readAt, list.hostOverrides?.readAt];
  if (times.some((t) => !t)) return undefined;
  return times.sort((a, b) => Number(a!.seconds - b!.seconds))[0];
}

/** A Warning as a box or a row says it. */
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

/** The fixes that change ddash alone and so run at once; the others write to pfSense or Nginx Proxy Manager through a Plan. */
export const ddashOnly: ReadonlySet<FixKind> = new Set([
  FixKind.SET_WORKLOAD_OVERRIDE,
  FixKind.USE_AS_URL,
  FixKind.MERGE,
  FixKind.REATTACH,
  FixKind.DELETE_ORPHAN,
]);

/** The Nav Entry's badge: how many Warnings count, in warn. */
export function useWarningsBadge(): Badge | null {
  const { list } = useApps();
  const n = (list?.apps ?? []).reduce((sum, a) => sum + counting(a).length, 0);
  return n > 0 ? { count: n, tone: "warn" } : null;
}

/** An App's URL's hostname. */
export const hostOf = (a: App) => URL.parse(a.url)?.hostname ?? a.url;

/** The groups in use, offered while typing one. */
export const groupsOf = (apps: readonly App[]) => [...new Set(apps.map((a) => a.group).filter(Boolean))].sort();
