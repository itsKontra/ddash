import { useQuery } from "@ddash/plugin";
import { AppsService, type App } from "@ddash/proto/apps/apps_pb";

/** ListApps, shared by the Page and the Widget. */
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

/** The visible Apps by group, groups by name, then the ungrouped ones. */
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

/** The Apps the Widget shows: every visible one, in list order. */
export const visible = (apps: readonly App[]) => apps.filter((a) => !a.hidden);
