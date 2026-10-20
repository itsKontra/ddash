import type { Status, Widget, WidgetSize } from "@ddash/plugin";
import { StatusDot, Stat } from "@ddash/ui";
import { latency, shownName, stateLabel, tally, toneOf, useChecks, warning } from "./checks";

/**
 * The worst of the enabled Checks: down names what's down, warn what is
 * Flapping while up or has an expiring certificate, then maint, else up
 * with the count. Idle without Checks; warn when checks is unavailable.
 */
function useStatus(): Status | undefined {
  const { data, error } = useChecks();
  if (error) return { tone: "warn", summary: `checks unavailable: ${error.rawMessage}` };
  if (!data) return undefined;
  const { enabled, up, warn, down, maint } = tally(data.checks);
  if (!enabled.length) return { tone: "idle", summary: "No Checks yet" };
  if (down.length) return { tone: "down", summary: `${down.length} down: ${down.map(shownName).join(", ")}` };
  if (warn.length) return { tone: "warn", summary: warn.map(warning).join("; ") };
  if (maint.length) return { tone: "maint", summary: `${maint.length} in Maintenance` };
  return { tone: "up", summary: `${up.length} of ${enabled.length} up` };
}

/** S: up, down and Maintenance counts. M: each Check that isn't up, too. */
function Body({ size }: { size: WidgetSize }) {
  const { data } = useChecks();
  if (!data) return null;
  const { enabled, up, down, maint } = tally(data.checks);
  const troubled = enabled.filter((c) => toneOf(c) !== "up");
  return (
    <div className="flex flex-col gap-3">
      <div className="flex gap-6">
        <Stat label="up" value={up.length} tone="up" />
        <Stat label="down" value={down.length} tone={down.length ? "down" : undefined} />
        <Stat label="Maintenance" value={maint.length} tone={maint.length ? "maint" : undefined} />
      </div>
      {size === "M" && troubled.length > 0 && (
        <ul className="flex flex-col gap-1 text-sm">
          {troubled.map((c) => (
            <li key={String(c.id)} className="flex items-center gap-2">
              <StatusDot tone={toneOf(c)} label={stateLabel(c)} />
              <span className="truncate">{shownName(c)}</span>
              <span className="ml-auto truncate text-muted-foreground">{c.reason || (c.latencyMs ? latency(c.latencyMs) : stateLabel(c))}</span>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}

/** The Overview Widget: how many Checks are up, down or in Maintenance. */
export const summary: Widget = {
  id: "summary",
  title: "Checks",
  sizes: ["S", "M"],
  defaultSize: "S",
  Body,
  useStatus,
};
