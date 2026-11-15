import type { Command, ConcernAction } from "@ddash/plugin";
import type { IconName } from "@ddash/ui";
import { toneIcon, type Listed } from "./concerns";

/** One entry of the command palette: a Plugin's Command, a Page, a Concern's Action, the scheme toggle. */
export interface Entry {
  /** Unique among all entries. */
  key: string;
  /** Where it comes from: "Needs you", "Go to", "Actions", or the Plugin's title. */
  group: string;
  title: string;
  hint?: string;
  icon?: Command["icon"];
  keywords?: readonly string[];
  /** A pinned Command: the empty palette lists it under "Pinned". */
  pinned?: boolean;
  run: () => void;
}

/** A string's words, lower-cased: "Open Grafana (media)" → open, grafana, media. */
const words = (s: string) => s.toLowerCase().split(/[^\p{L}\p{N}]+/u).filter(Boolean);

/**
 * How well an entry matches the query, by word start: each word of the
 * query must start a word of the entry's title, hint, keywords or group,
 * else it is 0. A word starting the title counts most, one starting a later
 * word of the title next, one found elsewhere least.
 */
export function score(query: string, e: Entry): number {
  const asked = words(query);
  if (asked.length === 0) return 0;
  const title = words(e.title);
  const rest = [e.hint ?? "", e.group, ...(e.keywords ?? [])].flatMap(words);
  let total = 0;
  for (const q of asked) {
    if (title[0]?.startsWith(q)) total += 4;
    else if (title.some((w) => w.startsWith(q))) total += 3;
    else if (rest.some((w) => w.startsWith(q))) total += 1;
    else return 0;
  }
  return total;
}

/** The entries matching the query, best first; a tie keeps the shorter title, then the given order. */
export function search(query: string, entries: readonly Entry[], limit = 12): Entry[] {
  return entries
    .map((e, i) => ({ e, i, s: score(query, e) }))
    .filter((m) => m.s > 0)
    .sort((a, b) => b.s - a.s || a.e.title.length - b.e.title.length || a.i - b.i)
    .slice(0, limit)
    .map((m) => m.e);
}

/** A Plugin's Commands as last reported, with its title. */
export interface Offered {
  plugin: string;
  title: string;
  commands: readonly Command[];
}

/**
 * Every Plugin's Commands as entries, in the Plugins' order and each
 * Plugin's, grouped under the Plugin's title. A Command without a title or run is dropped, and so
 * is a Plugin's second one with an id it already used.
 */
export function commandEntries(offered: readonly Offered[], run: (plugin: string, id: string) => void): Entry[] {
  const out: Entry[] = [];
  for (const { plugin, title, commands } of offered) {
    const ids = new Set<string>();
    for (const c of commands) {
      if (typeof c.title !== "string" || c.title === "" || typeof c.run !== "function" || ids.has(c.id)) continue;
      ids.add(c.id);
      out.push({
        key: `${plugin}:${c.id}`,
        group: title,
        title: c.title,
        hint: c.hint,
        icon: c.icon,
        keywords: c.keywords,
        pinned: c.pinned === true,
        run: () => run(plugin, c.id),
      });
    }
  }
  return out;
}

/**
 * The loud Concerns as entries under "Needs you", worst first: each runs
 * its main Action, titled "Start · n8n is down"; one without Actions goes
 * to its detail, else to the Overview.
 */
export function concernEntries(loud: readonly Listed[], act: (l: Listed, a: ConcernAction) => void, go: (to: string) => void): Entry[] {
  return loud.map((l) => {
    const c = l.concern;
    const main = c.actions?.[0];
    return {
      key: `concern:${l.plugin}:${c.id}`,
      group: "Needs you",
      title: main ? `${main.label} · ${c.title}` : c.title,
      hint: c.cause?.split(" · ")[0],
      icon: c.icon ?? toneIcon[c.tone],
      keywords: [c.subject],
      run: main ? () => act(l, main) : () => go(c.href ?? "/"),
    };
  });
}

/** A Page the palette goes to: the Overview, a Plugin's, Plugins, Settings, the Audit Log. */
export interface Destination {
  to: string;
  label: string;
  icon: string;
}

/** The Pages as entries under "Go to". */
export const pageEntries = (pages: readonly Destination[], go: (to: string) => void): Entry[] =>
  pages.map((p) => ({ key: `page:${p.to}`, group: "Go to", title: p.label, icon: p.icon as IconName, run: () => go(p.to) }));

// How many loud Concerns and pinned Commands lead the empty palette, so the Pages stay in sight.
const worstFew = 5;
const pinnedFew = 6;

/**
 * What the palette lists: with a query, the matches across everything;
 * without one, the worst few loud Concerns, the first few pinned Commands
 * under "Pinned" (the rest of both are found by typing), then the Pages,
 * then the shell's own Actions.
 */
export function listed(query: string, all: { concerns: readonly Entry[]; pages: readonly Entry[]; actions: readonly Entry[]; commands: readonly Entry[] }): Entry[] {
  if (query.trim() === "") {
    const pinned = all.commands.filter((e) => e.pinned).slice(0, pinnedFew);
    return [...all.concerns.slice(0, worstFew), ...pinned.map((e) => ({ ...e, group: "Pinned" })), ...all.pages, ...all.actions];
  }
  return search(query, [...all.concerns, ...all.commands, ...all.pages, ...all.actions]);
}
