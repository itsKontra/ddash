import type { Status, Tone } from "@ddash/plugin";
import type { HeroProblem } from "@ddash/ui";

/** What the Hero shows. */
export interface HeroState {
  tone: Tone;
  headline: string;
  problems: HeroProblem[];
}

const worse: Tone[] = ["down", "warn", "maint", "idle", "up"];

/**
 * The Hero over the placed Widgets, in grid order, with each one's status
 * (undefined until it first reports, which the Hero ignores). It lists the
 * Widgets whose tone isn't up and takes the worst tone among them. While
 * ddash is unreachable its tone is at least warn, since what's on screen
 * may be stale.
 */
export function hero(placed: readonly { title: string; status?: Status }[], unreachable: boolean): HeroState {
  const reported = placed.filter((p) => p.status !== undefined);
  const problems = reported
    .filter((p) => p.status!.tone !== "up")
    .map((p) => ({ title: p.title, tone: p.status!.tone, summary: p.status!.summary }));
  let tone: Tone = problems.length ? worse.find((t) => problems.some((p) => p.tone === t))! : reported.length ? "up" : "idle";
  if (unreachable && tone !== "down") tone = "warn";
  let headline: string;
  if (problems.length) headline = `${problems.length} ${problems.length === 1 ? "Widget needs" : "Widgets need"} attention`;
  else if (reported.length) headline = "Everything is up";
  else if (placed.length) headline = "Waiting for the Widgets to report";
  else headline = "No Widgets on the Overview";
  return { tone, headline, problems };
}
