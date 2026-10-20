import type { ComponentType } from "react";
import type { RouteObject } from "react-router";
import type { Tone } from "@ddash/ui";

export type { Tone };

/** A Widget's size on the Overview grid: S 1×1, M 2×1, L 2×2. */
export type WidgetSize = "S" | "M" | "L";

/** What a Widget or a badge reports. */
export interface Status {
  tone: Tone;
  summary: string;
}

/** A Nav Entry's live badge: a count, e.g. "3" down, or a short text such as "!". */
export type Badge = { tone: Tone } & ({ count: number } | { text: string });

/** A Plugin's link in the sidebar, leading to its index Page. */
export interface NavEntry {
  /** Repo-curated place in the sidebar; ties are broken by title. */
  order: number;
  /** Defaults to the manifest's title. */
  label?: string;
  /** A lucide icon name; defaults to the manifest's icon. */
  icon?: string;
  /** A hook returning the badge, or null for none. */
  useBadge?: () => Badge | null;
}

/** A card the Plugin offers for the Overview. The host draws its frame. */
export interface Widget {
  id: string;
  title: string;
  sizes: WidgetSize[];
  defaultSize: WidgetSize;
  Body: ComponentType<{ size: WidgetSize }>;
  /** Undefined until the first status is known. */
  useStatus: () => Status | undefined;
}

/** What a Plugin's bundle exports as default. */
export interface PluginDefinition {
  /** Zero or one Nav Entry. */
  nav?: NavEntry;
  /** react-router routes, relative to /<plugin-name>/. The index route is the Nav Entry's target. */
  pages?: RouteObject[];
  widgets?: Widget[];
}

/**
 * Declares a Plugin. A bundle's default export:
 *
 *     export default definePlugin({ nav: { order: 50 }, pages: [{ index: true, Component: Containers }] });
 */
export function definePlugin(definition: PluginDefinition): PluginDefinition {
  return definition;
}
