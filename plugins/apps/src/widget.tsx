import type { Status, Widget, WidgetSize } from "@ddash/plugin";
import { State } from "@ddash/proto/checks/checks_pb";
import { AppIcon, EmptyState, Skeleton, StatusDot } from "@ddash/ui";
import { useApps, visible, warned } from "./apps";
import { checkStatus } from "@ddash/display";
import { mutedDown, useAppChecks, worst } from "./checks";
import type { App } from "@ddash/proto/apps/apps_pb";

/** How many Apps fit each size. */
const fits: Record<WidgetSize, number> = { S: 4, M: 6, L: 16 };

/**
 * The worst of the visible Apps' Checks and the Warnings that count: down
 * names the Apps that are down; warn those up but Flapping or with a
 * certificate expiring within 14 days, and how many Warnings count on
 * which Apps; then Maintenance, else up with how many are. A pending
 * Check counts for nothing, and a muted one that is down is only told
 * after the summary; the up count is then of the watched (unmuted) Apps.
 * Idle without Apps or before any Check is up or down; warn while apps or
 * checks is unavailable.
 */
function useStatus(): Status | undefined {
  const { list, error } = useApps();
  const { byApp, error: checksError } = useAppChecks();
  if (!list) return error ? { tone: "warn", summary: `apps unavailable: ${error.rawMessage}` } : undefined;
  const apps = visible(list.apps);
  if (apps.length === 0) return { tone: "idle", summary: "No Apps yet" };
  if (checksError) return { tone: "warn", summary: `checks unavailable: ${checksError.rawMessage}` };
  if (!byApp) return undefined;
  const w = worst(apps, byApp);
  const muted = apps.filter((a) => mutedDown(byApp.get(a.id))).length;
  const also = muted ? ` · ${muted} muted down` : "";
  const flagged = warned(list.apps);
  if (w?.tone !== "down" && flagged.length > 0) {
    const n = flagged.reduce((sum, f) => sum + f.warnings.length, 0);
    const checkWarnings = w?.tone === "warn" ? w.hit.map((h) => `${h.app.name}: ${h.status.label}`) : [];
    const summary = `${n} ${n === 1 ? "Warning" : "Warnings"}: ${flagged.map((f) => f.app.name).join(", ")}`;
    return { tone: "warn", summary: [...checkWarnings, summary].join("; ") + also };
  }
  if (!w && !muted) {
    const pending = apps.some((a) => {
      const c = byApp.get(a.id);
      return c && !c.disabled && c.state === State.PENDING;
    });
    return { tone: "idle", summary: `${apps.length} ${apps.length === 1 ? "App" : "Apps"}, ${pending ? "Checks pending" : "no Checks yet"}` };
  }
  const hit = w?.hit ?? [];
  const names = hit.map((h) => h.app.name);
  switch (w?.tone) {
    case "down":
      return { tone: "down", summary: `${names.length} down: ${names.join(", ")}${also}` };
    case "warn":
      return { tone: "warn", summary: hit.map((h) => `${h.app.name}: ${h.status.label}`).join("; ") + also };
    case "maint":
      return { tone: "maint", summary: `${names.length} in Maintenance${also}` };
    default: {
      if (!muted) return { tone: "up", summary: `${names.length} of ${apps.length} Apps up` };
      // Only muted Checks are down: up, counting the watched (unmuted) Apps.
      const watched = (a: App) => !byApp.get(a.id)?.muted;
      return { tone: "up", summary: `${hit.filter((h) => watched(h.app)).length} of ${apps.filter(watched).length} watched Apps up${also}` };
    }
  }
}

/** The visible Apps as icons that open them, each with its Check's status, and how many are muted and down. */
function Body({ size }: { size: WidgetSize }) {
  const { list, error } = useApps();
  const { byApp } = useAppChecks();
  // While apps is unavailable, the Widget's status says so.
  if (!list) return error ? null : <Skeleton height={size === "L" ? "lg" : "md"} />;
  const apps = visible(list.apps);
  if (apps.length === 0) return <EmptyState icon="layout-grid" title="No Apps yet">Proxy Hosts show here once ddash reads them from Nginx Proxy Manager.</EmptyState>;
  const shown = apps.slice(0, fits[size]);
  const more = apps.length - shown.length;
  const muted = apps.filter((a) => mutedDown(byApp?.get(a.id))).length;
  const foot = [more > 0 && `and ${more} more`, muted > 0 && `${muted} muted down`].filter(Boolean).join(" · ");
  return (
    <div className="flex flex-col gap-2">
      <ul className={size === "L" ? "grid grid-cols-4 gap-3" : "grid grid-cols-6 gap-2"}>
        {shown.map((a) => {
          const check = byApp?.get(a.id);
          const status = checkStatus(check);
          return (
            <li key={a.id}>
              <a href={a.url} target="_blank" rel="noreferrer" title={a.url} className="flex flex-col items-center gap-1 rounded-ui p-1.5 hover:bg-muted">
                <AppIcon name={a.name} icon={a.icon} url={a.url} size={size === "L" ? 36 : 28} />
                <span className="flex w-full items-center justify-center gap-1">
                  {byApp && <StatusDot tone={status.tone} label={mutedDown(check) ? `${status.label}, muted` : status.label} />}
                  <span className="truncate text-center text-xs">{a.name}</span>
                </span>
              </a>
            </li>
          );
        })}
      </ul>
      {foot && <p className="text-xs text-muted-foreground">{foot}</p>}
    </div>
  );
}

/** The Overview Widget: App cards to open, and how their Checks stand. */
export const apps: Widget = {
  id: "apps",
  title: "Apps",
  sizes: ["M", "L"],
  defaultSize: "L",
  Body,
  useStatus,
};
