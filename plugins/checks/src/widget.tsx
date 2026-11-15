import type { Status, Widget, WidgetSize } from "@ddash/plugin";
import { MetricTile, Row, Rows, StatusList } from "@ddash/ui";
import { latency, shownName, stateLabel, tally, toneOf, useChecks, warning } from "./checks";

/**
 * The worst of the enabled Checks: down names what's down, warn what is
 * Flapping while up or has an expiring certificate, then maint, else up
 * with the count. A muted Check that is down doesn't count: it is only
 * told after the summary, and the up count is then of the watched
 * (unmuted) Checks; idle when none is watched. Idle without Checks; warn
 * when checks is unavailable. Its fact: how many of the watched Checks are up.
 */
function useStatus(): Status | undefined {
  const { data, error } = useChecks();
  if (error) return { tone: "warn", summary: `checks unavailable: ${error.rawMessage}` };
  if (!data) return undefined;
  const { enabled, up, warn, down, muted, maint } = tally(data.checks);
  if (!enabled.length) return { tone: "idle", summary: "No Checks yet" };
  const watched = enabled.filter((c) => !c.muted);
  const fact = watched.length ? { value: up.filter((c) => !c.muted).length, label: `of ${watched.length} ${watched.length === 1 ? "Check" : "Checks"} up` } : undefined;
  const also = muted.length ? ` · ${muted.length} muted down` : "";
  if (down.length) return { tone: "down", summary: `${down.length} down: ${down.map(shownName).join(", ")}${also}`, fact };
  if (warn.length) return { tone: "warn", summary: warn.map(warning).join("; ") + also, fact };
  if (maint.length) return { tone: "maint", summary: `${maint.length} in Maintenance${also}`, fact };
  if (!muted.length) return { tone: "up", summary: `${up.length} of ${enabled.length} up`, fact };
  if (!watched.length) return { tone: "idle", summary: `${muted.length} muted down` };
  return { tone: "up", summary: `${fact!.value} of ${watched.length} watched up${also}`, fact };
}

/**
 * Up, down and Maintenance counts; down counts the muted ones too, in idle
 * with how many are muted while none else is down. S draws them as rows,
 * so the narrowest S cuts no label or number; M as tiles, with each Check
 * that isn't up under them, the muted ones last and quiet.
 */
function Body({ size }: { size: WidgetSize }) {
  const { data, error } = useChecks();
  // The Widget's status already says checks is unavailable.
  if (error && !data) return null;
  const t = data && tally(data.checks);
  const troubled = t?.enabled.filter((c) => toneOf(c) !== "up" && !t.muted.includes(c)) ?? [];
  if (size === "S") {
    return (
      <Rows>
        <Row label="Up" value={t?.up.length} tone="up" />
        <Row
          label="Down"
          tone={t?.down.length ? "down" : "idle"}
          value={
            t && (
              <>
                {t.down.length + t.muted.length}
                {t.muted.length > 0 && <span className="text-xs font-normal text-muted-foreground">{t.muted.length} muted</span>}
              </>
            )
          }
        />
        <Row label="Maintenance" value={t?.maint.length} tone={t?.maint.length ? "maint" : "idle"} />
      </Rows>
    );
  }
  return (
    <div className="flex flex-col gap-3">
      <div className="grid grid-cols-3 gap-3">
        <MetricTile label="up" value={t?.up.length} tone="up" />
        <MetricTile
          label="down"
          value={t && t.down.length + t.muted.length}
          tone={t?.down.length ? "down" : "idle"}
          desc={t?.muted.length ? `${t.muted.length} muted` : undefined}
        />
        <MetricTile label="Maintenance" value={t?.maint.length} tone={t?.maint.length ? "maint" : "idle"} />
      </div>
      {t && troubled.length + t.muted.length > 0 && (
        <StatusList
          items={[
            ...troubled.map((c) => ({
              tone: toneOf(c),
              title: shownName(c),
              // The aside never shrinks, so a long reason goes under the name instead.
              aside: c.latencyMs ? latency(c.latencyMs) : stateLabel(c),
              meta: c.reason ? <span className="text-down-text">{c.reason}</span> : undefined,
            })),
            ...t.muted.map((c) => ({ tone: "idle" as const, title: shownName(c), aside: "down, muted", meta: c.reason || undefined })),
          ]}
        />
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
  defaultOrder: 60,
  Body,
  useStatus,
};
