import type { ComponentType, ReactNode } from "react";
import type { RouteObject } from "react-router";
import type { IconName, Tone } from "@ddash/ui";

export type { Tone };

/** A Widget's size on the Overview grid: S 1×1, M 2×1, L 2×2. */
export type WidgetSize = "S" | "M" | "L";

/** What a Widget or a badge reports. */
export interface Status {
  tone: Tone;
  summary: string;
  /** A count about the network the Overview's hero lists under its sentence, for a placed Widget. */
  fact?: Fact;
}

/**
 * A count with what it counts, as the hero reads it with the count in bold:
 * { value: 12, label: "guests running" }, { value: 22, label: "of 22 Checks up" }.
 */
export interface Fact {
  value: number;
  label: string;
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

/** How much a Concern needs the Operator: down, warn (attention) or info (news). */
export type ConcernTone = Extract<Tone, "down" | "warn" | "info">;

/**
 * An Action a Concern offers: the one the Plugin's Page runs, with the same
 * confirm or Plan and the same Audit Log entry. The core adds no path of
 * its own for changes.
 */
export type ConcernAction = {
  /** The button's label: "Start", "Adopt", "Mute". */
  label: string;
  icon?: IconName;
  /** In flight: the button is disabled. */
  pending?: boolean;
  /** Why its last run failed, shown under the Concern's cause. */
  error?: string;
  /**
   * It doesn't fix the cause, only deals with the Concern another way,
   * e.g. Mute or Maintenance: drawn outlined even as the main button,
   * which is filled only for a fix (Start, Adopt, Check again).
   */
  secondary?: boolean;
} & (
  | {
      /** Runs it, e.g. useAction's run with the confirm the Page asks. */
      run: () => unknown;
    }
  | {
      /**
       * Draws the Plugin's own Sheet for it (a Plan, a form) in the
       * Plugin's scope, until it calls close. Called on every render while
       * open, so what it returns must be elements of stable components.
       */
      sheet: (close: () => void) => ReactNode;
    }
);

/**
 * Something that needs the Operator. The Overview lists every installed
 * Plugin's Concerns in one place, "Needs you", worst first.
 */
export interface Concern {
  /** Stable while its cause lasts, and unique within the Plugin. */
  id: string;
  tone: ConcernTone;
  /** What it is about, as a headline names it: "Paperless", "nas". */
  subject: string;
  /** "Paperless is down". */
  title: string;
  /** Why, and since when: "Compose Project paperless exited (137) · 6 min ago". */
  cause?: string;
  /**
   * Known and silenced, and why, as the folded line counts it: "muted",
   * "in Maintenance". A quiet Concern is folded away under "Off on
   * purpose" and never sets the Overview's tone.
   */
  quiet?: string;
  /** A lucide icon; defaults to its tone's. */
  icon?: IconName;
  /** The first is the main one, at the row's end; the others sit before it, quieter. */
  actions?: ConcernAction[];
  /** Where its detail lives, e.g. "/checks/12". */
  href?: string;
}

/**
 * An entry the Plugin offers the command palette (Ctrl K): open an App,
 * go to one of its Pages, or start one of its Actions. The palette only
 * calls run or draws sheet; it never changes anything itself.
 */
export type Command = CommandEntry &
  (
    | {
        /**
         * Does it the way the Plugin's Page does: opens the App, navigates, or
         * runs the Action with the confirm its button asks.
         */
        run: () => void;
      }
    | {
        /**
         * Draws the Plugin's own Sheet for it, the one its button on the
         * Page opens (a Plan, a form), in the Plugin's scope on whatever
         * Page the palette was opened, until it calls close. Called on
         * every render while open, so what it returns must be elements of
         * stable components.
         */
        sheet: (close: () => void) => ReactNode;
      }
  );

/** What every Command has, whether it runs or opens a Sheet. */
export interface CommandEntry {
  /** Stable while the Command lasts, and unique within the Plugin. */
  id: string;
  /** What it does, verb first: "Open Grafana", "Restart paperless". */
  title: string;
  /** What it acts on, shown at the row's end: "grafana.example.org", "Compose Project on media". */
  hint?: string;
  /**
   * A lucide icon, or an App's own icon as AppIcon draws it (its
   * dashboard-icons name, else by the App's name, else its favicon).
   */
  icon?: IconName | { app: string; icon?: string; url?: string };
  /** More words it is found by, e.g. a group or an alias: "media", "pull". */
  keywords?: string[];
  /**
   * The Operator pinned what it opens, e.g. a pinned App: the empty
   * palette lists it under Pinned, in the order the Plugin gives.
   */
  pinned?: boolean;
  /**
   * Why its last run failed, e.g. the Action's refusal, as a ConcernAction
   * has it. The Plugin's Page that would show it may not be mounted, so the
   * shell shows a newly set one where the Operator is, on any Page ("Start
   * n8n failed: …"), until it is dismissed or the Command runs again.
   */
  error?: string;
}

/** What a Plugin's bundle exports as default. */
export interface PluginDefinition {
  /** Zero or one Nav Entry. */
  nav?: NavEntry;
  /** react-router routes, relative to /<plugin-name>/. The index route is the Nav Entry's target. */
  pages?: RouteObject[];
  widgets?: Widget[];
  /**
   * A hook returning the Plugin's Concerns now: [] for none, undefined
   * until known. It runs for every installed Plugin on every Page (the
   * Overview lists them, the command palette offers the loud ones),
   * whether or not any of its Widgets is placed.
   */
  useConcerns?: () => Concern[] | undefined;
  /**
   * A hook returning the Commands the Plugin offers the command palette
   * now: [] for none, undefined until known. It runs for every installed
   * Plugin on every Page, so the palette opens with them.
   */
  useCommands?: () => Command[] | undefined;
}

/**
 * Declares a Plugin. A bundle's default export:
 *
 *     export default definePlugin({ nav: { order: 50 }, pages: [{ index: true, Component: Containers }] });
 */
export function definePlugin(definition: PluginDefinition): PluginDefinition {
  return definition;
}
