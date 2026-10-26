import { ago, checkStatus } from "@ddash/display";
import { useAction } from "@ddash/plugin";
import { AppsService, Reach, WarningKind, type App, type ListAppsResponse } from "@ddash/proto/apps/apps_pb";
import type { Check } from "@ddash/proto/checks/checks_pb";
import { AppIcon, Button, Checkbox, cx, Icon, noMatch, Notice, SearchField, StatusDot, type IconName } from "@ddash/ui";
import { useState, type ReactNode } from "react";
import { listModel, readAt, shownWarnings, warningText } from "./apps";

/** How each Reach shows as a row's glyph. */
export const reachLook: Record<Reach, { icon: IconName; label: string }> = {
  [Reach.UNSPECIFIED]: { icon: "circle-dashed", label: "Not read yet" },
  [Reach.PROXIED]: { icon: "waypoints", label: "Proxied" },
  [Reach.NOT_PROXIED]: { icon: "arrow-right", label: "Not proxied" },
  [Reach.LINK_ONLY]: { icon: "link", label: "Link only" },
};

/**
 * The list on the left: search and Show ignored Warnings over Needs
 * attention, the groups, then Hidden (n), folded unless asked or holding
 * the selected App; Refresh with when the sources were read.
 */
export function AppList({
  list,
  checks,
  q,
  onSearch,
  showIgnored,
  onShowIgnored,
  selected,
  onSelect,
  now,
}: {
  list: ListAppsResponse;
  /** Each App's Check by App id; undefined while checks hasn't answered. */
  checks?: ReadonlyMap<string, Check>;
  q: string;
  onSearch: (q: string) => void;
  showIgnored: boolean;
  onShowIgnored: (on: boolean) => void;
  selected: string;
  onSelect: (id: string) => void;
  /** When ListApps answered: what the read time counts from. */
  now: number;
}) {
  const [unfolded, setUnfolded] = useState(false);
  const m = listModel(list.apps, q, showIgnored);
  const hiddenOpen = unfolded || q !== "" || m.hidden.some((a) => a.id === selected);
  const row = (a: App, note?: string) => <Row key={a.id} app={a} check={checks?.get(a.id)} note={note} selected={a.id === selected} onSelect={() => onSelect(a.id)} />;
  const empty = m.attention.length === 0 && m.groups.length === 0 && m.hidden.length === 0;
  return (
    <div className="flex flex-col">
      <div className="flex flex-col gap-2 border-b border-border p-3">
        <SearchField label="Search Apps" value={q} onChange={onSearch} placeholder="Search by name, hostname or group" />
        {m.ignored > 0 && <Checkbox label={`Show ignored Warnings (${m.ignored})`} checked={showIgnored} onChange={(e) => onShowIgnored(e.target.checked)} />}
        <RefreshLine list={list} now={now} />
      </div>
      <nav aria-label="Apps" className="py-2">
        {empty && list.apps.length > 0 && <p className="px-3 py-6 text-center text-sm text-muted-foreground">{noMatch("Apps", q)}</p>}
        {m.attention.length > 0 && (
          <Group title="Needs attention" tone="warn">
            {m.attention.map((a) => row(a, warningText[shownWarnings(a, showIgnored)[0].kind]))}
          </Group>
        )}
        {m.groups.map((g) => (
          <Group key={g.title} title={g.title}>
            {g.apps.map((a) => row(a, a.orphaned ? warningText[WarningKind.ORPHANED] : undefined))}
          </Group>
        ))}
        {m.hidden.length > 0 && (
          <Group
            title={`Hidden (${m.hidden.length})`}
            action={
              q === "" && (
                <Button size="sm" variant="ghost" aria-expanded={hiddenOpen} onClick={() => setUnfolded(!hiddenOpen)} aria-label={hiddenOpen ? "Fold Hidden Apps" : "Show Hidden Apps"}>
                  {hiddenOpen ? "Fold" : "Show"}
                </Button>
              )
            }
          >
            {hiddenOpen && m.hidden.map((a) => row(a))}
          </Group>
        )}
      </nav>
    </div>
  );
}

/** Refresh, and when the sources were last read. */
function RefreshLine({ list, now }: { list: ListAppsResponse; now: number }) {
  const refresh = useAction(AppsService.method.refresh);
  const at = readAt(list);
  return (
    <div className="flex flex-col gap-1">
      <div className="flex items-center gap-2 text-xs text-muted-foreground">
        <span className="flex-1">{at ? `read ${ago(at, now)}` : "not read yet"}</span>
        <Button size="sm" variant="ghost" icon="refresh-cw" disabled={refresh.pending} onClick={() => void refresh.run({})}>
          Refresh
        </Button>
      </div>
      {refresh.error && <Notice tone="down">Refresh failed: {refresh.error.rawMessage}</Notice>}
    </div>
  );
}

function Group({ title, tone, action, children }: { title: string; tone?: "warn"; action?: ReactNode; children: ReactNode }) {
  return (
    <section aria-label={title} className="pb-2">
      <div className={cx("flex min-h-8 items-center gap-2 px-3 text-xs font-bold tracking-wide uppercase", tone === "warn" ? "text-warn-text" : "text-muted-foreground")}>
        <h3 className="flex-1">{title}</h3>
        {action}
      </div>
      <ul>{children}</ul>
    </section>
  );
}

/** A row: the Check dot, the icon, the name (and a note), the Reach glyph. */
function Row({ app, check, note, selected, onSelect }: { app: App; check?: Check; note?: string; selected: boolean; onSelect: () => void }) {
  const status = checkStatus(check);
  const reach = reachLook[app.reach];
  return (
    <li>
      <button
        type="button"
        data-app
        aria-label={app.name}
        aria-current={selected || undefined}
        onClick={onSelect}
        className={cx("flex w-full cursor-pointer items-center gap-2 px-3 py-1.5 text-left text-sm hover:bg-muted", selected && "bg-primary-tint")}
      >
        <StatusDot tone={status.tone} label={status.label} />
        <AppIcon name={app.name} icon={app.icon} url={app.url} size={20} />
        <span className="flex min-w-0 flex-1 flex-col">
          <span className="truncate font-medium">{app.name}</span>
          {note && <span className="truncate text-xs text-warn-text">{note}</span>}
        </span>
        {!app.orphaned && (
          <span title={reach.label} className="text-muted-foreground">
            <Icon name={reach.icon} size="sm" label={reach.label} />
          </span>
        )}
      </button>
    </li>
  );
}
