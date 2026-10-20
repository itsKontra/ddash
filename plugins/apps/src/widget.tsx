import type { Status, Widget, WidgetSize } from "@ddash/plugin";
import { AppIcon } from "@ddash/ui";
import { useApps, visible } from "./apps";

/** How many Apps fit each size. */
const fits: Record<WidgetSize, number> = { S: 4, M: 6, L: 16 };

/**
 * Up with the number of visible Apps, idle without any, warn while apps is
 * unavailable. #46 replaces this with the worst of their Checks.
 */
function useStatus(): Status | undefined {
  const { list, error } = useApps();
  if (!list) return error ? { tone: "warn", summary: `apps unavailable: ${error.rawMessage}` } : undefined;
  const n = visible(list.apps).length;
  if (n === 0) return { tone: "idle", summary: "No Apps yet" };
  return { tone: "up", summary: `${n} ${n === 1 ? "App" : "Apps"}` };
}

/** The visible Apps as icons that open them. */
function Body({ size }: { size: WidgetSize }) {
  const { list } = useApps();
  if (!list) return null;
  const apps = visible(list.apps);
  if (apps.length === 0) return <p className="text-sm text-muted-foreground">No Apps yet.</p>;
  const shown = apps.slice(0, fits[size]);
  const more = apps.length - shown.length;
  return (
    <div className="flex flex-col gap-2">
      <ul className={size === "L" ? "grid grid-cols-4 gap-3" : "grid grid-cols-6 gap-2"}>
        {shown.map((a) => (
          <li key={a.id}>
            <a href={a.url} target="_blank" rel="noreferrer" title={a.url} className="flex flex-col items-center gap-1 rounded-ui p-1.5 hover:bg-muted">
              <AppIcon name={a.name} icon={a.icon} url={a.url} size={size === "L" ? 36 : 28} />
              <span className="w-full truncate text-center text-xs">{a.name}</span>
            </a>
          </li>
        ))}
      </ul>
      {more > 0 && <p className="text-xs text-muted-foreground">and {more} more</p>}
    </div>
  );
}

/** The Overview Widget: App cards to open. */
export const apps: Widget = {
  id: "apps",
  title: "Apps",
  sizes: ["M", "L"],
  defaultSize: "L",
  Body,
  useStatus,
};
