import type { NavEntry } from "@ddash/plugin";
import type { LoadedPlugin } from "./plugins";

/** One Plugin's link in the sidebar. */
export interface NavItem {
  plugin: LoadedPlugin;
  label: string;
  /** A lucide icon name. */
  icon: string;
  useBadge?: NavEntry["useBadge"];
  /** Set when the bundle failed to load. */
  broken?: Error;
}

/**
 * The Plugins' Nav Entries in sidebar order: by their curated order, ties
 * broken by title, then the Plugins whose bundle failed to load (their
 * order is in the bundle), by title. A Plugin without a Nav Entry has no
 * link.
 */
export function navItems(plugins: Iterable<LoadedPlugin>): NavItem[] {
  const entries: (NavItem & { order: number })[] = [];
  const broken: NavItem[] = [];
  for (const p of plugins) {
    if (p.error) {
      broken.push({ plugin: p, label: p.title, icon: p.icon, broken: p.error });
    } else if (p.definition?.nav) {
      const nav = p.definition.nav;
      entries.push({ plugin: p, order: nav.order, label: nav.label ?? p.title, icon: nav.icon ?? p.icon, useBadge: nav.useBadge });
    }
  }
  const byLabel = (a: NavItem, b: NavItem) => a.label.localeCompare(b.label);
  entries.sort((a, b) => a.order - b.order || byLabel(a, b));
  broken.sort(byLabel);
  return [...entries, ...broken];
}
