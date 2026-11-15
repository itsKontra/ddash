import { checkStatus } from "@ddash/display";
import { usePluginLink, type Status, type Tone, type Widget, type WidgetSize } from "@ddash/plugin";
import type { App } from "@ddash/proto/apps/apps_pb";
import { State, type Check } from "@ddash/proto/checks/checks_pb";
import { AppIcon, Button, cx, EmptyState, Icon, Notice, Select, Skeleton, StatusDot } from "@ddash/ui";
import { useState, type ReactNode } from "react";
import { Link } from "react-router";
import { hostOf, useApps, visible, warned } from "./apps";
import { mutedDown, useAppChecks, worst } from "./checks";
import { pinned, usePins } from "./pins";

/** How many Apps fit each size with nothing pinned. */
const fits: Record<WidgetSize, number> = { S: 4, M: 6, L: 16 };

/**
 * The worst of the visible Apps' Checks and the Warnings that count: down
 * names the Apps that are down; warn those up but Flapping or with a
 * certificate expiring within 14 days, and how many Warnings count on
 * which Apps; then Maintenance, else up with how many are. A pending
 * Check counts for nothing, and a muted one that is down is only told
 * after the summary; the up count is then of the watched (unmuted) Apps,
 * and it is idle when every App's Check is muted.
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
      // Only muted Checks are down: up, counting the watched (unmuted) Apps;
      // idle when no App has an enabled Check that isn't muted.
      const watched = (a: App) => !byApp.get(a.id)?.muted;
      const checked = apps.some((a) => {
        const c = byApp.get(a.id);
        return c && !c.disabled && !c.muted;
      });
      if (!checked) return { tone: "idle", summary: `${muted} muted down` };
      return { tone: "up", summary: `${hit.filter((h) => watched(h.app)).length} of ${apps.filter(watched).length} watched Apps up${also}` };
    }
  }
}

/**
 * The pinned Apps in the Operator's order, else (nothing pinned) the
 * visible Apps as many as fit; both as icons that open them. The Widget's
 * edit state pins, unpins and orders them.
 */
function Body({ size }: { size: WidgetSize }) {
  const { list, error } = useApps();
  const { byApp } = useAppChecks();
  const [editing, setEditing] = useState(false);
  // While apps is unavailable, the Widget's status says so.
  if (!list) return error ? null : <Skeleton height={size === "L" ? "lg" : "md"} />;
  const apps = visible(list.apps);
  if (apps.length === 0) return <EmptyState icon="layout-grid" title="No Apps yet">Proxy Hosts show here once ddash reads them from Nginx Proxy Manager.</EmptyState>;
  if (editing) return <EditPins apps={apps} onDone={() => setEditing(false)} />;
  const pins = pinned(list.apps);
  const muted = apps.filter((a) => mutedDown(byApp?.get(a.id))).length;
  const edit = <Button variant="ghost" size="sm" icon="pin" aria-label="Edit pins" title="Pin, unpin and order Apps" onClick={() => setEditing(true)} />;
  if (pins.length === 0) return <Every apps={apps} byApp={byApp} size={size} muted={muted} edit={edit} />;
  return (
    <div className="flex flex-col gap-2">
      <ul aria-label="Pinned Apps" className="grid grid-cols-3 sm:grid-cols-5 gap-1">
        {pins.map((a) => (
          <li key={a.id} className="min-w-0">
            <Pinned app={a} check={byApp?.get(a.id)} />
          </li>
        ))}
      </ul>
      <Foot muted={muted}>
        {edit}
        <AllApps count={apps.length} />
      </Foot>
    </div>
  );
}

/** With nothing pinned: the visible Apps that fit, each with its Check's status, and how many more. */
function Every({ apps, byApp, size, muted, edit }: { apps: App[]; byApp?: ReadonlyMap<string, Check>; size: WidgetSize; muted: number; edit: ReactNode }) {
  const shown = apps.slice(0, fits[size]);
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
      <Foot muted={muted} more={apps.length - shown.length}>
        {edit}
      </Foot>
    </div>
  );
}

/** The Widget's last line: how many more Apps there are and how many are muted and down, then its controls. */
function Foot({ muted, more = 0, children }: { muted: number; more?: number; children: ReactNode }) {
  const text = [more > 0 && `and ${more} more`, muted > 0 && `${muted} muted down`].filter(Boolean).join(" · ");
  return (
    <div className="flex items-center gap-2">
      <p className="min-w-0 flex-1 truncate text-xs text-muted-foreground">{text}</p>
      {children}
    </div>
  );
}

/** "All 27 Apps", to the Apps Page. */
function AllApps({ count }: { count: number }) {
  const page = usePluginLink("apps");
  if (!page) return null;
  return (
    <Link to={page} className="inline-flex items-center gap-0.5 text-xs font-medium whitespace-nowrap text-muted-foreground hover:text-foreground">
      All {count} {count === 1 ? "App" : "Apps"}
      <Icon name="chevron-right" size="sm" />
    </Link>
  );
}

/** The dot on a pinned App's icon by its Check's tone: none while up or unknown. */
const dot: Partial<Record<Tone, string>> = { down: "bg-down", warn: "bg-warn", maint: "bg-maint" };

/**
 * A pinned App: its icon and name, opening it. Its Check rides on the
 * icon: nothing while up, else a dot in the Check's tone; down and not
 * muted greys the icon too. The tooltip gives the hostname and, unless
 * up, the Check's state with its cause.
 */
function Pinned({ app, check }: { app: App; check?: Check }) {
  const status = checkStatus(check);
  const muted = mutedDown(check);
  const label = muted ? `${status.label}, muted` : status.label;
  const marked = dot[status.tone];
  const greyed = status.tone === "down" && !muted;
  return (
    <a
      href={app.url}
      target="_blank"
      rel="noreferrer"
      title={marked ? `${hostOf(app)} · ${label}` : hostOf(app)}
      className="flex min-w-0 flex-col items-center gap-1.5 rounded-ui px-1 pt-3 pb-2.5 text-xs font-medium text-muted-foreground transition-colors hover:bg-muted hover:text-foreground"
    >
      <span className="relative">
        {/* A square, whatever the icon's own aspect, so the names line up. */}
        <span data-greyed={greyed || undefined} className={cx("flex h-9 w-9 items-center justify-center", greyed && "opacity-50 grayscale")}>
          <AppIcon name={app.name} icon={app.icon} url={app.url} size={36} />
        </span>
        {marked && <span role="img" aria-label={label} className={cx("absolute -top-1 -right-1.5 h-2.5 w-2.5 rounded-full border-2 border-card", marked)} />}
      </span>
      <span className="max-w-full truncate">{app.name}</span>
    </a>
  );
}

/**
 * The Widget's edit state: the pinned Apps in order, each moved earlier or
 * later or unpinned by its buttons, and a pick of the others to pin last.
 */
function EditPins({ apps, onDone }: { apps: App[]; onDone: () => void }) {
  const { pin, move, pending, error } = usePins();
  const pins = pinned(apps);
  const others = apps.filter((a) => !pins.includes(a));
  return (
    <div className="flex flex-col gap-3">
      {pins.length > 0 ? (
        <ol aria-label="Pinned Apps" className="grid grid-cols-2 sm:grid-cols-4 gap-2">
          {pins.map((a, i) => (
            <li key={a.id} className="flex min-w-0 flex-col items-center gap-1 rounded-ui border border-border px-1 pt-2 pb-1">
              <span className="flex h-7 w-7 items-center justify-center">
                <AppIcon name={a.name} icon={a.icon} url={a.url} size={28} />
              </span>
              <span className="max-w-full truncate text-xs font-medium">{a.name}</span>
              <span className="flex">
                <Button variant="ghost" size="sm" icon="chevron-left" aria-label={`Move ${a.name} earlier`} disabled={pending || i === 0} onClick={() => void move(a.id, -1)} />
                <Button variant="ghost" size="sm" icon="pin-off" aria-label={`Unpin ${a.name}`} disabled={pending} onClick={() => void pin(a.id, false)} />
                <Button
                  variant="ghost"
                  size="sm"
                  icon="chevron-right"
                  aria-label={`Move ${a.name} later`}
                  disabled={pending || i === pins.length - 1}
                  onClick={() => void move(a.id, 1)}
                />
              </span>
            </li>
          ))}
        </ol>
      ) : (
        <p className="text-xs text-muted-foreground">Nothing pinned: the Widget shows the first Apps. Pin the ones you open most.</p>
      )}
      {error && <Notice tone="down">{error.rawMessage}</Notice>}
      <div className="flex flex-wrap items-center gap-2">
        <Select label="Pin an App" value="" disabled={pending || others.length === 0} onChange={(e) => e.target.value && void pin(e.target.value, true)}>
          <option value="">Pin an App…</option>
          {others.map((a) => (
            <option key={a.id} value={a.id}>
              {a.name}
            </option>
          ))}
        </Select>
        <span className="ml-auto">
          <Button size="sm" icon="check" onClick={onDone}>
            Done
          </Button>
        </span>
      </div>
    </div>
  );
}

/** The Overview Widget: the Apps the Operator pinned (else the first ones) to open, and how their Checks stand. */
export const apps: Widget = {
  id: "apps",
  title: "Apps",
  sizes: ["M", "L"],
  defaultSize: "L",
  Body,
  useStatus,
};
