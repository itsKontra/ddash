import type { Widget, WidgetSize } from "@ddash/plugin";

/** A placed Widget. */
export interface Placed {
  /** <plugin>.<widget-id>. */
  widget: string;
  size: WidgetSize;
}

/** The Overview layout: the placed Widgets in grid order, and every Widget the shell has seen. */
export interface Layout {
  entries: Placed[];
  seen: string[];
}

/** A Widget's layout key: <plugin>.<widget-id>. */
export const widgetKey = (plugin: string, id: string) => `${plugin}.${id}`;

/** The Plugin a layout key belongs to. Plugin names hold no dot. */
const pluginOf = (key: string) => key.slice(0, key.indexOf("."));

/**
 * Reconciles the layout against the Widgets the installed Plugins offer,
 * given by Plugin name (undefined while a bundle loads, or when it failed):
 *
 * - a Plugin that isn't installed is forgotten, placed and seen, so a
 *   reinstall places its Widgets again;
 * - a Widget its loaded Plugin no longer offers is dropped and forgotten,
 *   and a size it no longer supports is reset to its default;
 * - a Plugin whose Widgets aren't known keeps its entries as they are;
 * - each Widget is placed at most once;
 * - every unseen Widget becomes seen; one with a defaultOrder is appended
 *   at its default size, by that order, and one without waits under Add
 *   Widget. A seen Widget that isn't placed was removed by the Operator (or
 *   never placed) and stays so.
 */
export function reconcile(layout: Layout, installed: ReadonlyMap<string, readonly Widget[] | undefined>): Layout {
  // undefined: the Widget's Plugin is installed but its Widgets aren't known.
  const lookup = (key: string): Widget | null | undefined => {
    const plugin = pluginOf(key);
    if (!installed.has(plugin)) return null;
    const widgets = installed.get(plugin);
    if (!widgets) return undefined;
    return widgets.find((w) => widgetKey(plugin, w.id) === key) ?? null;
  };
  const seen = layout.seen.filter((key, i) => lookup(key) !== null && layout.seen.indexOf(key) === i);
  const entries: Placed[] = [];
  for (const e of layout.entries) {
    const w = lookup(e.widget);
    if (w === null || entries.some((p) => p.widget === e.widget)) continue;
    entries.push(w === undefined || w.sizes.includes(e.size) ? e : { widget: e.widget, size: w.defaultSize });
    if (!seen.includes(e.widget)) seen.push(e.widget);
  }
  const offered: { placed: Placed; order: number }[] = [];
  for (const [plugin, widgets] of installed) {
    for (const w of widgets ?? []) {
      const key = widgetKey(plugin, w.id);
      if (seen.includes(key)) continue;
      seen.push(key);
      if (w.defaultOrder !== undefined) offered.push({ placed: { widget: key, size: w.defaultSize }, order: w.defaultOrder });
    }
  }
  entries.push(...offered.sort((a, b) => a.order - b.order).map((o) => o.placed));
  return { entries, seen };
}

/**
 * Whether the shell waits before reconciling a layout that was never saved
 * (nothing seen yet) while a Plugin's bundle still loads, so a fresh
 * Overview places every Plugin's Widgets at once, in their defaultOrder,
 * rather than in the order the bundles happen to load.
 */
export function waits(layout: Layout, installed: readonly string[], settled: (plugin: string) => boolean): boolean {
  return layout.seen.length === 0 && installed.some((p) => !settled(p));
}
