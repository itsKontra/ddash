import type { Status, Widget, WidgetSize } from "@ddash/plugin";
import { State } from "@ddash/proto/checks/checks_pb";
import { AppIcon, StatusDot } from "@ddash/ui";
import { useApps, visible } from "./apps";
import { checkStatus, useAppChecks, worst } from "./checks";

/** How many Apps fit each size. */
const fits: Record<WidgetSize, number> = { S: 4, M: 6, L: 16 };

/**
 * The worst of the visible Apps' Checks: down names the Apps that are
 * down, warn those up but Flapping or with a certificate expiring within
 * 14 days, then Maintenance, else up with how many are. A pending Check
 * counts for nothing. Idle without Apps or before any Check is up or down;
 * warn while apps or checks is unavailable.
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
  if (!w) {
    const pending = apps.some((a) => {
      const c = byApp.get(a.id);
      return c && !c.disabled && c.state === State.PENDING;
    });
    return { tone: "idle", summary: `${apps.length} ${apps.length === 1 ? "App" : "Apps"}, ${pending ? "Checks pending" : "no Checks yet"}` };
  }
  const names = w.hit.map((h) => h.app.name);
  switch (w.tone) {
    case "down":
      return { tone: "down", summary: `${names.length} down: ${names.join(", ")}` };
    case "warn":
      return { tone: "warn", summary: w.hit.map((h) => `${h.app.name}: ${h.status.label}`).join("; ") };
    case "maint":
      return { tone: "maint", summary: `${names.length} in Maintenance` };
    default:
      return { tone: "up", summary: `${names.length} of ${apps.length} Apps up` };
  }
}

/** The visible Apps as icons that open them, each with its Check's status. */
function Body({ size }: { size: WidgetSize }) {
  const { list } = useApps();
  const { byApp } = useAppChecks();
  if (!list) return null;
  const apps = visible(list.apps);
  if (apps.length === 0) return <p className="text-sm text-muted-foreground">No Apps yet.</p>;
  const shown = apps.slice(0, fits[size]);
  const more = apps.length - shown.length;
  return (
    <div className="flex flex-col gap-2">
      <ul className={size === "L" ? "grid grid-cols-4 gap-3" : "grid grid-cols-6 gap-2"}>
        {shown.map((a) => {
          const status = checkStatus(byApp?.get(a.id));
          return (
            <li key={a.id}>
              <a href={a.url} target="_blank" rel="noreferrer" title={a.url} className="flex flex-col items-center gap-1 rounded-ui p-1.5 hover:bg-muted">
                <AppIcon name={a.name} icon={a.icon} url={a.url} size={size === "L" ? 36 : 28} />
                <span className="flex w-full items-center justify-center gap-1">
                  {byApp && <StatusDot tone={status.tone} label={status.label} />}
                  <span className="truncate text-center text-xs">{a.name}</span>
                </span>
              </a>
            </li>
          );
        })}
      </ul>
      {more > 0 && <p className="text-xs text-muted-foreground">and {more} more</p>}
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
