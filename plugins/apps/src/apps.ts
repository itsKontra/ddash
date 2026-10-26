import { useQuery, type Badge } from "@ddash/plugin";
import { AppsService, FixKind, WarningKind, type App, type Fix, type HostOverrideOrAlias, type ListAppsResponse, type Warning } from "@ddash/proto/apps/apps_pb";
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

/** "nas.home.example": a Host Override's or alias's hostname, or its parent's. */
export const fqdn = (n: { host: string; domain: string }) => `${n.host ? `${n.host}.` : ""}${n.domain}`;

/** "nas.home.example" for a Host Override, "media.home.example (alias of nas.home.example)" for an alias. */
export const overrideOrAliasText = (e?: HostOverrideOrAlias) => {
  if (!e) return "";
  return e.parent ? `${fqdn(e)} (alias of ${fqdn(e.parent)})` : fqdn(e);
};

/** What the pane opens for a fix it runs elsewhere: a Panel's Edit, the Reattach Dialog or Delete's Plan Dialog. */
export type FixPlace = "editWorkload" | "reattach" | "editProxy" | "addProxy" | "remove";

/**
 * Each fix: what its button says; whether it removes something (a danger
 * button); whether it changes ddash alone and so runs at once (the others
 * write to pfSense or Nginx Proxy Manager through a Plan); and where the
 * pane runs it when not in its Warning box or a Plan Dialog of its own.
 */
export const fixes: Record<FixKind, { label: (f: Fix, apps: readonly App[]) => string; removes?: boolean; ddashOnly?: boolean; place?: FixPlace }> = {
  [FixKind.UNSPECIFIED]: { label: () => "Fix" },
  [FixKind.REGISTER]: { label: (f) => `Register ${f.hostname}` },
  [FixKind.DELETE_APP]: { label: () => "Delete App…", removes: true, place: "remove" },
  [FixKind.ADD_REVERSE_PROXY]: { label: () => "Add reverse proxy…", place: "addProxy" },
  [FixKind.REMOVE_NAME]: { label: (f) => `Remove ${f.hostname}`, removes: true },
  [FixKind.SET_WORKLOAD_OVERRIDE]: { label: () => "Set a Workload override", ddashOnly: true, place: "editWorkload" },
  [FixKind.PICK_CERTIFICATE]: { label: () => "Pick a certificate…", place: "editProxy" },
  [FixKind.SAVE_AGAIN]: { label: () => "Save again" },
  [FixKind.USE_AS_URL]: { label: (f) => `Use ${f.hostname} as the URL`, ddashOnly: true },
  [FixKind.POINT_AT_REVERSE_PROXY]: { label: (f) => `Point ${f.hostname} at the Reverse Proxy` },
  [FixKind.DELETE_PROXY_HOST]: { label: () => "Delete Proxy Host", removes: true },
  [FixKind.DELETE_ENTRY]: { label: (f) => `Delete ${overrideOrAliasText(f.hostOverride)}`, removes: true },
  [FixKind.MERGE]: { label: (f, apps) => `Merge ${apps.find((a) => a.id === f.appId)?.name ?? f.appId} into this App`, ddashOnly: true },
  [FixKind.REATTACH]: { label: () => "Reattach to an App…", ddashOnly: true, place: "reattach" },
  [FixKind.DELETE_ORPHAN]: { label: () => "Delete", removes: true, ddashOnly: true },
};

/** What a fix's button says. */
export const fixLabel = (f: Fix, apps: readonly App[]) => fixes[f.kind].label(f, apps);

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
