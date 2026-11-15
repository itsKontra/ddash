import type { Fact, Status, Tone } from "@ddash/plugin";
import type { Listed } from "./concerns";

/** A placed Widget as the hero reads it: its Plugin, title and status (undefined until it first reports). */
export interface Reporting {
  plugin: string;
  title: string;
  status?: Status;
}

/** A piece of the hero's sentence; a quiet one is drawn softer. */
export interface Part {
  text: string;
  quiet?: boolean;
}

/** What the hero shows. */
export interface HeroState {
  tone: Tone;
  /** The sentence, in pieces: join them with a space to read it. */
  sentence: Part[];
  /** The reporting Widgets' facts, in grid order. */
  facts: Fact[];
}

/** The sentence as one string, as a screen reader and a test read it. */
export const said = (parts: readonly Part[]) => parts.map((p) => p.text).join(" ");

// The most names the sentence lists; it counts the rest.
const named = 3;

const numbers = ["no", "one", "two", "three", "four", "five", "six", "seven", "eight", "nine", "ten", "eleven", "twelve"];
const inWords = (n: number) => numbers[n] ?? String(n);
const capital = (s: string) => s.charAt(0).toUpperCase() + s.slice(1);

/** "A", "A and B", "A, B and C", "A, B, C and 2 more". */
function list(names: readonly string[]): string {
  const shown = names.slice(0, named);
  if (names.length > named) return `${shown.join(", ")} and ${names.length - named} more`;
  return shown.length > 1 ? `${shown.slice(0, -1).join(", ")} and ${shown.at(-1)}` : shown.join("");
}

const unique = (names: readonly string[]) => [...new Set(names)];

/**
 * The hero over the placed Widgets, in grid order, and the Concerns of
 * every installed Plugin (undefined until one has reported), worst first.
 *
 * It names what is down by the loud Concerns' subjects. A placed Widget
 * that is down or warn without a loud Concern of its Plugin at least as
 * bad behind it counts too, by its title, so nothing red is hidden while a
 * Plugin doesn't report Concerns. Quiet Concerns never count, and nor do
 * Maintenance, news or unknown. Warnings alone are counted, not named.
 * While ddash is unreachable the sentence says so instead, at warn at
 * least, since what's on screen may be stale.
 */
export function hero(placed: readonly Reporting[], concerns: readonly Listed[] | undefined, unreachable: boolean): HeroState {
  const reported = placed.filter((p) => p.status !== undefined);
  const loud = (concerns ?? []).filter((l) => !l.concern.quiet);
  const covered = (p: Reporting) =>
    loud.some((l) => l.plugin === p.plugin && (l.concern.tone === "down" || l.concern.tone === p.status!.tone));
  const troubled = reported.filter((p) => (p.status!.tone === "down" || p.status!.tone === "warn") && !covered(p));
  const at = (tone: Tone) =>
    unique([...loud.filter((l) => l.concern.tone === tone).map((l) => l.concern.subject), ...troubled.filter((p) => p.status!.tone === tone).map((p) => p.title)]);
  const down = at("down");
  const warn = at("warn").filter((s) => !down.includes(s));
  const facts = reported.flatMap((p) => p.status!.fact ?? []);

  const anything = reported.length > 0 || concerns !== undefined;
  let tone: Tone = down.length ? "down" : warn.length ? "warn" : anything ? "up" : "idle";
  if (unreachable && tone !== "down") tone = "warn";

  let sentence: Part[];
  if (unreachable) sentence = [{ text: "ddash can't be reached." }, { text: "What you see may be out of date.", quiet: true }];
  else if (down.length) sentence = [{ text: `${list(down)} ${down.length === 1 ? "is" : "are"} down.` }, { text: "Everything else is up.", quiet: true }];
  else if (warn.length)
    sentence = [
      { text: "Everything is up.", quiet: true },
      { text: `${capital(inWords(warn.length))} ${warn.length === 1 ? "thing is" : "things are"} getting tight.` },
    ];
  else if (anything) sentence = [{ text: "Everything you watch is up." }];
  else if (placed.length) sentence = [{ text: "Waiting for the Widgets to report." }];
  else sentence = [{ text: "No Widgets on the Overview." }];
  return { tone, sentence, facts };
}

/** How long ago the oldest data on screen was read: "read 12 s ago", "read 3 min ago", "read 2 h ago". */
export function readAgo(ms: number): string {
  const s = Math.max(0, Math.floor(ms / 1000));
  if (s < 60) return `read ${s} s ago`;
  if (s < 3600) return `read ${Math.floor(s / 60)} min ago`;
  return `read ${Math.floor(s / 3600)} h ago`;
}

/**
 * When the oldest data on screen was read, in ms since the epoch: the
 * least recent read among the queries that are mounted and refetch on a
 * tier. A query that never refetches (the layout, the installed Plugins)
 * or has no data yet doesn't count. Undefined with none.
 */
export function oldestRead(queries: readonly { readAt: number; refetches: boolean }[]): number | undefined {
  const reads = queries.filter((q) => q.refetches && q.readAt > 0).map((q) => q.readAt);
  return reads.length ? Math.min(...reads) : undefined;
}
