import type { Status, Tone } from "@ddash/plugin";
/** One line of the hero's caption: a placed Widget that isn't up. */
export interface Problem {
  title: string;
  tone: Tone;
  summary: string;
}

/** What the hero shows. */
export interface HeroState {
  tone: Tone;
  headline: string;
  problems: Problem[];
}

const worse: Tone[] = ["down", "warn", "maint", "info", "idle", "up"];

// What the headline says of n Widgets at a tone: [one, many].
const said: Record<Tone, [string, string]> = {
  down: ["down", "down"],
  warn: ["needs attention", "need attention"],
  maint: ["in Maintenance", "in Maintenance"],
  info: ["has news", "have news"],
  idle: ["unknown", "unknown"],
  up: ["up", "up"],
};

/**
 * The hero over the placed Widgets, in grid order, with each one's status
 * (undefined until it first reports, which the hero ignores). It lists the
 * Widgets whose tone isn't up and takes the worst tone among them. While
 * ddash is unreachable its tone is at least warn, since what's on screen
 * may be stale.
 */
export function hero(placed: readonly { title: string; status?: Status }[], unreachable: boolean): HeroState {
  const reported = placed.filter((p) => p.status !== undefined);
  const problems = reported
    .filter((p) => p.status!.tone !== "up")
    .map((p) => ({ title: p.title, tone: p.status!.tone, summary: p.status!.summary }));
  const worst = worse.find((t) => problems.some((p) => p.tone === t));
  let tone: Tone = worst ?? (reported.length ? "up" : "idle");
  if (unreachable && tone !== "down") tone = "warn";
  let headline: string;
  if (worst) {
    // The worst tone's count, as its metric tile has it.
    const n = problems.filter((p) => p.tone === worst).length;
    headline = `${n} ${n === 1 ? "Widget" : "Widgets"} ${n === 1 ? said[worst][0] : said[worst][1]}`;
  } else if (reported.length) headline = "Everything is up";
  else if (placed.length) headline = "Waiting for the Widgets to report";
  else headline = "No Widgets on the Overview";
  return { tone, headline, problems };
}
