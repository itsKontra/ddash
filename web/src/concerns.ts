import type { Concern, ConcernTone } from "@ddash/plugin";
import type { IconName } from "@ddash/ui";

/** A Concern's icon when it names none: its tone's. */
export const toneIcon: Record<ConcernTone, IconName> = { down: "circle-x", warn: "triangle-exclamation-point", info: "info" };

/** A Concern as the Overview lists it, with the Plugin that reported it. */
export interface Listed {
  /** The Plugin's name. */
  plugin: string;
  concern: Concern;
}

/** What "Needs you" shows. */
export interface Needs {
  /** The Concerns that aren't quiet, worst first. */
  loud: Listed[];
  /** The quiet ones, folded away, worst first. */
  quiet: Listed[];
  /** The worst loud Concern's tone; undefined with none. */
  tone?: ConcernTone;
}

const worse: readonly ConcernTone[] = ["down", "warn", "info"];

/**
 * Every Plugin's Concerns in one list, split into loud and quiet, each
 * ordered down, warn, info and, within a tone, by title. A Plugin's second
 * Concern with an id it already used is dropped; another Plugin's isn't,
 * since the list keys each by its Plugin too.
 */
export function needs(reports: readonly { plugin: string; concerns: readonly Concern[] }[]): Needs {
  const all: Listed[] = [];
  for (const { plugin, concerns } of reports) {
    const ids = new Set<string>();
    for (const concern of concerns) {
      if (ids.has(concern.id)) continue;
      ids.add(concern.id);
      all.push({ plugin, concern });
    }
  }
  const rank = (l: Listed) => worse.indexOf(l.concern.tone);
  const sorted = all.sort((a, b) => rank(a) - rank(b) || a.concern.title.localeCompare(b.concern.title));
  const loud = sorted.filter((l) => !l.concern.quiet);
  return { loud, quiet: sorted.filter((l) => l.concern.quiet), tone: loud[0]?.concern.tone };
}

/** How many loud Concerns "Needs you" shows before the rest wait behind "and 26 more". */
export const cap = 5;

/**
 * The loud Concerns "Needs you" draws: the worst cap of them, unless
 * expanded; and how many are past the cap, folded or not, 0 when there is
 * no line to fold them. A single one past it is drawn rather than folded,
 * since its line would take a row's place.
 */
export function capped(loud: readonly Listed[], expanded: boolean): { shown: readonly Listed[]; more: number } {
  if (loud.length <= cap + 1) return { shown: loud, more: 0 };
  return { shown: expanded ? loud : loud.slice(0, cap), more: loud.length - cap };
}

/**
 * The folded line's count of the quiet Concerns by why each is quiet, most
 * first: "4 muted, 1 in Maintenance".
 */
export function offOnPurpose(quiet: readonly Listed[]): string {
  const counts = new Map<string, number>();
  for (const { concern } of quiet) counts.set(concern.quiet!, (counts.get(concern.quiet!) ?? 0) + 1);
  return [...counts]
    .sort(([, a], [, b]) => b - a)
    .map(([why, n]) => `${n} ${why}`)
    .join(", ");
}
