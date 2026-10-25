import { PluginScope, useAction, useQuery, type Fetch, type Status, type Widget, type WidgetSize } from "@ddash/plugin";
import { CoreService } from "@ddash/proto/ddash/core/core_pb";
import { Button, cx, Donut, HeroCard, Icon, Kicker, MetricTile, PageTitle, Panel, Pill, Ring, Segmented, Sheet, Timeline, toneBg, toneText, useVariant, WidgetCard, type HeroProblem, type Tone } from "@ddash/ui";
import { GripVertical, Pencil, Plus, X } from "lucide-react";
import { useCallback, useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { Link } from "react-router";
import { ErrorBoundary, ErrorFrame } from "./boundary";
import { hero } from "./hero";
import { reconcile, widgetKey, type Layout, type Placed } from "./layout";
import type { LoadedPlugin, Plugins } from "./plugins";
import { useReach } from "./reach";

/** The Overview layout as the shell shows it, and how to save a new one. */
export interface OverviewLayout {
  /** Undefined until the core answered with the layout and the installed Plugins. */
  layout?: Layout;
  /** Saves layout; resolves to whether the core took it. */
  save: (layout: Layout) => Promise<boolean>;
  /** Why the last save failed. */
  error?: Error;
}

/**
 * Reads the layout (tier NONE) and reconciles it against the Widgets the
 * installed Plugins offer, saving the result whenever reconciling changed
 * it. The shell runs this on every page, so an uninstall forgets the
 * Plugin's Widgets even when the Overview isn't open.
 */
export function useOverviewLayout(plugins: Plugins): OverviewLayout {
  const { data } = useQuery(CoreService.method.getLayout, {});
  const { run, error } = useAction(CoreService.method.saveLayout);
  const stored = useMemo(() => (data ? fromProto(data.layout) : undefined), [data]);
  const { installed, loaded } = plugins;
  const offered = useMemo(() => offeredWidgets({ installed, loaded }), [installed, loaded]);
  const layout = useMemo(() => (stored && offered ? reconcile(stored, offered) : undefined), [stored, offered]);

  const save = useCallback(async (l: Layout) => (await run({ layout: l })) !== undefined, [run]);

  // Saves each reconciled layout once: a failed save isn't retried until
  // reconciling changes the layout again.
  const saved = useRef("");
  useEffect(() => {
    if (!layout || !stored) return;
    const json = JSON.stringify(layout);
    if (json === JSON.stringify(stored) || json === saved.current) return;
    saved.current = json;
    void save(layout);
  }, [layout, stored, save]);

  return { layout, save, error };
}

/** The Widgets each installed Plugin offers, by name: undefined while its bundle loads or after it failed. */
function offeredWidgets({ installed, loaded }: Plugins): Map<string, readonly Widget[] | undefined> | undefined {
  if (!installed) return undefined;
  return new Map(installed.map((p) => [p.name, loaded.get(p.name)?.definition?.widgets ?? (loaded.get(p.name)?.definition ? [] : undefined)]));
}

function fromProto(l: { entries: { widget: string; size: string }[]; seen: string[] } | undefined): Layout {
  return {
    entries: (l?.entries ?? []).map((e) => ({ widget: e.widget, size: e.size as WidgetSize })),
    seen: [...(l?.seen ?? [])],
  };
}

const span: Record<WidgetSize, string> = {
  S: "col-span-1 row-span-1",
  M: "col-span-1 sm:col-span-2 row-span-1",
  L: "col-span-1 sm:col-span-2 row-span-2",
};

const count = (tones: readonly Tone[], t: Tone) => tones.filter((x) => x === t).length;
const today = () => new Date().toLocaleDateString(undefined, { weekday: "long", month: "long", day: "numeric" });

/** PROTOTYPE: the Overview's head, per variant. */
function OverviewHead({ tone, headline, problems, tones, actions }: { tone: Tone; headline: string; problems: readonly HeroProblem[]; tones: readonly Tone[]; actions: ReactNode }) {
  const v = useVariant();
  const up = count(tones, "up");
  const total = tones.length;
  const down = count(tones, "down");
  const warn = count(tones, "warn");
  const maint = count(tones, "maint");
  const ratio = total ? up / total : 0;
  if (v === "B") {
    return (
      <>
        <div className="flex flex-wrap items-center gap-3">
          <Pill tone={tone} dot>
            {headline}
          </Pill>
          <span className="text-xs text-muted-foreground">{today()}</span>
          <div className="ml-auto flex gap-2">{actions}</div>
        </div>
        <div className="grid gap-4 lg:grid-cols-[minmax(0,1fr)_minmax(0,1.2fr)]">
          <Panel kicker="Distribution" title="Widget status" description="What every Widget on the Overview last reported.">
            <Donut
              total={total}
              caption="Widgets"
              segments={[
                { tone: "up", label: "Up", value: up },
                { tone: "warn", label: "Needs attention", value: warn },
                { tone: "down", label: "Down", value: down },
                { tone: "maint", label: "Maintenance", value: maint },
              ]}
            />
          </Panel>
          <Panel kicker="Right now" title={problems.length ? "What needs you" : "All clear"} description="Widgets that aren't up, worst first.">
            {problems.length ? (
              <Timeline items={problems.map((p) => ({ tone: p.tone, title: p.title, meta: p.summary, aside: p.tone }))} />
            ) : (
              <Timeline items={[{ tone: "up", title: "Everything is up", meta: `${total} Widgets reporting`, aside: "now" }]} />
            )}
          </Panel>
        </div>
      </>
    );
  }
  if (v === "C") {
    return (
      <>
        <PageTitle kicker={today()} title={headline + "."} size="xl" actions={actions} />
        <div className="-mt-2 mb-2 grid grid-cols-2 gap-y-5 border-y border-border py-6 sm:grid-cols-4">
          {(
            [
              ["Up", up, "up"],
              ["Needs attention", warn, "warn"],
              ["Down", down, "down"],
              ["Maintenance", maint, "maint"],
            ] as const
          ).map(([label, n, t], i) => (
            <div key={label} className={cx("px-5", i > 0 && "sm:border-l sm:border-border")}>
              <div className={cx("text-4xl font-extrabold tracking-tight tabular-nums", n > 0 && t !== "up" && toneText[t])}>{n}</div>
              <div className="mt-1 flex items-center gap-1.5 text-xs font-semibold text-muted-foreground">
                <span className={cx("h-1.5 w-1.5 rounded-full", toneBg[t])} />
                {label}
              </div>
            </div>
          ))}
        </div>
        {problems.length > 0 && (
          <div className="flex flex-wrap gap-2">
            {problems.map((p) => (
              <Pill key={p.title} tone={p.tone} dot>
                {p.title}: {p.summary}
              </Pill>
            ))}
          </div>
        )}
      </>
    );
  }
  return (
    <>
      <PageTitle kicker="Home network" title="Overview" subtitle="Every Widget's latest report, worst first in the summary. Arrange the grid with Edit." actions={actions} />
      <HeroCard
        kicker={`Status · ${today()}`}
        title={headline}
        caption={problems.length ? problems.map((p) => `${p.title}: ${p.summary}`).join(" · ") : "Every Widget on the Overview reports up. Checks, Workloads and the VPN Tunnel are healthy."}
        foot={
          <>
            <span className="flex items-center gap-1.5">
              <Icon name="up" className="text-hero-accent" /> {up} up
            </span>
            <span className="h-3.5 w-px bg-hero-muted/30" />
            <span className="flex items-center gap-1.5">
              <Icon name="alert" className="text-hero-accent" /> {warn + down} need attention
            </span>
            <span className="h-3.5 w-px bg-hero-muted/30" />
            <span className="flex items-center gap-1.5">
              <Icon name="wrench" className="text-hero-accent" /> {maint} in Maintenance
            </span>
          </>
        }
        aside={<Ring ratio={ratio} value={`${Math.round(ratio * 100)}%`} label="Widgets up" />}
      />
      <div className="grid grid-cols-2 gap-4 xl:grid-cols-4">
        <MetricTile label="Up" value={up} icon="up" tone="up" desc={`of ${total} Widgets`} />
        <MetricTile label="Needs attention" value={warn} icon="alert" tone="warn" desc="flapping, expiring, degraded" />
        <MetricTile label="Down" value={down} icon="down" tone="down" desc="reporting down right now" />
        <MetricTile label="Maintenance" value={maint} icon="wrench" tone="maint" desc="not counted toward uptime" />
      </div>
      {total > 0 && <Kicker className="mt-4">Widgets</Kicker>}
    </>
  );
}

/** /: the Hero over the 4-column grid of placed Widgets, with Edit mode. */
export function Overview({ plugins, overview, fetch }: { plugins: Plugins; overview: OverviewLayout; fetch?: Fetch }) {
  const reach = useReach();
  // While editing, the Operator's unsaved layout.
  const [draft, setDraft] = useState<Layout | null>(null);
  const [adding, setAdding] = useState(false);
  const [dragging, setDragging] = useState<string | null>(null);
  const [statuses, setStatuses] = useState<ReadonlyMap<string, Status>>(new Map());
  const onStatus = useCallback((key: string, status: Status | undefined) => {
    setStatuses((prev) => {
      const old = prev.get(key);
      if (old?.tone === status?.tone && old?.summary === status?.summary) return prev;
      const next = new Map(prev);
      if (status) next.set(key, status);
      else next.delete(key);
      return next;
    });
  }, []);

  const layout = draft ?? overview.layout;
  const cards = (layout?.entries ?? []).flatMap((entry) => {
    const card = cardOf(entry, plugins);
    return card ? [card] : [];
  });
  const h = hero(
    cards.map((c) => ({ title: c.title, status: statuses.get(c.entry.widget) })),
    reach.unreachable,
  );

  const edit = (change: (l: Layout) => Layout) => setDraft((d) => (d ? change(d) : d));
  const move = (from: string, to: string) =>
    edit((l) => {
      const entries = [...l.entries];
      const [moved] = entries.splice(
        entries.findIndex((e) => e.widget === from),
        1,
      );
      entries.splice(
        entries.findIndex((e) => e.widget === to),
        0,
        moved,
      );
      return { ...l, entries };
    });
  const done = async () => {
    if (draft && JSON.stringify(draft) !== JSON.stringify(overview.layout) && !(await overview.save(draft))) return;
    setDraft(null);
    setAdding(false);
  };

  return (
    <div className="flex flex-col gap-6">
      {/* PROTOTYPE: each variant's head of the Overview. */}
      <OverviewHead
        tone={h.tone}
        headline={h.headline}
        problems={h.problems}
        tones={cards.map((c) => statuses.get(c.entry.widget)?.tone).filter((t): t is Tone => t !== undefined)}
        actions={
          layout &&
          (draft ? (
            <>
              <Button variant="outline" onClick={() => setAdding(true)}>
                <Plus className="h-4 w-4" aria-hidden /> Add Widget
              </Button>
              <Button onClick={done}>Done</Button>
            </>
          ) : (
            <Button variant="outline" onClick={() => setDraft(overview.layout!)}>
              <Pencil className="h-4 w-4" aria-hidden /> Edit
            </Button>
          ))
        }
      />
      {draft && overview.error && <ErrorFrame title="Saving the layout failed" error={overview.error} />}
      <ul aria-label="Widgets" className="grid grid-flow-dense auto-rows-[minmax(9rem,auto)] grid-cols-1 gap-4 sm:grid-cols-2 xl:grid-cols-4">
        {cards.map((c) => (
          <li
            key={c.entry.widget}
            aria-label={c.title}
            className={cx(span[c.entry.size], dragging === c.entry.widget && "opacity-40", draft && "cursor-grab")}
            draggable={draft ? true : undefined}
            onDragStart={(e) => {
              e.dataTransfer?.setData("text/plain", c.entry.widget);
              setDragging(c.entry.widget);
            }}
            onDragOver={(e) => {
              if (!dragging) return;
              e.preventDefault();
              if (dragging !== c.entry.widget) move(dragging, c.entry.widget);
            }}
            onDrop={(e) => e.preventDefault()}
            onDragEnd={() => setDragging(null)}
          >
            <WidgetCard
              className={cx(draft && "ring-2 ring-primary/30")}
              title={c.nav ? <Link to={`/${c.plugin!.name}`}>{c.title}</Link> : c.title}
              status={statuses.get(c.entry.widget)}
              actions={
                draft && (
                  <>
                    {c.widget && c.widget.sizes.length > 1 && (
                      <Segmented
                        label="Size"
                        value={c.entry.size}
                        options={c.widget.sizes}
                        onChange={(size) => edit((l) => ({ ...l, entries: l.entries.map((e) => (e.widget === c.entry.widget ? { ...e, size } : e)) }))}
                      />
                    )}
                    <GripVertical className="h-4 w-4 text-muted-foreground" aria-hidden />
                    <Button
                      variant="ghost"
                      size="sm"
                      aria-label={`Remove ${c.title}`}
                      onClick={() => edit((l) => ({ ...l, entries: l.entries.filter((e) => e.widget !== c.entry.widget) }))}
                    >
                      <X className="h-4 w-4" aria-hidden />
                    </Button>
                  </>
                )
              }
            >
              <CardBody card={c} fetch={fetch} onStatus={onStatus} />
            </WidgetCard>
          </li>
        ))}
      </ul>
      {draft && adding && (
        <AddWidget
          plugins={plugins}
          placed={draft.entries}
          onClose={() => setAdding(false)}
          onAdd={(key, w) => {
            edit((l) => ({
              entries: [...l.entries, { widget: key, size: w.defaultSize }],
              seen: l.seen.includes(key) ? l.seen : [...l.seen, key],
            }));
            setAdding(false);
          }}
        />
      )}
    </div>
  );
}

/** A placed Widget as the grid draws it. */
interface Card {
  entry: Placed;
  title: string;
  plugin?: LoadedPlugin;
  /** Undefined while the bundle loads, or when it failed (see plugin.error). */
  widget?: Widget;
  /** The Plugin has a Nav Entry for the title to link to. */
  nav: boolean;
}

function cardOf(entry: Placed, plugins: Plugins): Card | null {
  const name = entry.widget.slice(0, entry.widget.indexOf("."));
  const plugin = plugins.loaded.get(name);
  if (!plugin) return { entry, title: plugins.installed?.find((p) => p.name === name)?.title ?? name, nav: false };
  if (plugin.error) return { entry, title: plugin.title, plugin, nav: false };
  const widget = plugin.definition?.widgets?.find((w) => widgetKey(name, w.id) === entry.widget);
  if (!widget) return null; // gone: reconciling drops it
  return { entry, title: widget.title, plugin, widget, nav: !!plugin.definition?.nav };
}

/**
 * The Widget's body and status, in its Plugin's scope, behind an error
 * boundary: a crash in either is an error frame, reported at warn.
 */
function CardBody({ card, fetch, onStatus }: { card: Card; fetch?: Fetch; onStatus: (key: string, s: Status | undefined) => void }) {
  const key = card.entry.widget;
  if (card.plugin?.error) {
    const failed = `${card.plugin.title} failed to load`;
    return (
      <>
        <ErrorFrame title={failed} error={card.plugin.error} />
        <Report widget={key} status={{ tone: "warn", summary: failed }} onStatus={onStatus} />
      </>
    );
  }
  if (!card.plugin || !card.widget) return <p className="text-sm text-muted-foreground">Loading…</p>;
  const widget = card.widget;
  return (
    <PluginScope plugin={card.plugin} fetch={fetch}>
      <ErrorBoundary
        fallback={(error) => {
          const failed = `${widget.title} failed to render`;
          return (
            <>
              <ErrorFrame title={failed} error={error} />
              <Report widget={key} status={{ tone: "warn", summary: failed }} onStatus={onStatus} />
            </>
          );
        }}
      >
        <Live widget={widget} size={card.entry.size} widgetKey={key} onStatus={onStatus} />
      </ErrorBoundary>
    </PluginScope>
  );
}

function Live({
  widget,
  size,
  widgetKey: key,
  onStatus,
}: {
  widget: Widget;
  size: WidgetSize;
  widgetKey: string;
  onStatus: (key: string, s: Status | undefined) => void;
}) {
  const status = widget.useStatus();
  return (
    <>
      <Report widget={key} status={status} onStatus={onStatus} />
      <widget.Body size={size} />
    </>
  );
}

/** Tells the Overview a Widget's status, and forgets it when unmounted. */
function Report({ widget, status, onStatus }: { widget: string; status?: Status; onStatus: (key: string, s: Status | undefined) => void }) {
  const tone = status?.tone;
  const summary = status?.summary;
  useEffect(() => {
    onStatus(widget, tone === undefined ? undefined : { tone, summary: summary ?? "" });
    return () => onStatus(widget, undefined);
  }, [widget, tone, summary, onStatus]);
  return null;
}

/** The drawer of installed Widgets, by Plugin; the placed ones can't be added again. */
function AddWidget({
  plugins,
  placed,
  onClose,
  onAdd,
}: {
  plugins: Plugins;
  placed: readonly Placed[];
  onClose: () => void;
  onAdd: (key: string, w: Widget) => void;
}) {
  const groups = (plugins.installed ?? []).flatMap((p) => {
    const widgets = plugins.loaded.get(p.name)?.definition?.widgets ?? [];
    return widgets.length ? [{ plugin: p, widgets }] : [];
  });
  return (
    <Sheet title="Add Widget" onClose={onClose}>
      <div className="flex flex-col gap-5">
        {groups.map(({ plugin, widgets }) => (
          <section key={plugin.name} aria-label={plugin.title}>
            <h3 className="mb-2 text-xs font-semibold tracking-wide text-muted-foreground uppercase">{plugin.title}</h3>
            <ul className="flex flex-col gap-2">
              {widgets.map((w) => {
                const key = widgetKey(plugin.name, w.id);
                return (
                  <li key={key} className="flex items-center gap-3 rounded-ui border border-border bg-card px-3 py-2">
                    <div>
                      <div className="text-sm font-medium">{w.title}</div>
                      <div className="text-xs text-muted-foreground">sizes {w.sizes.join(" / ")}</div>
                    </div>
                    <Placement placed={placed.some((e) => e.widget === key)}>
                      <Button size="sm" aria-label={`Add ${w.title}`} onClick={() => onAdd(key, w)}>
                        Add
                      </Button>
                    </Placement>
                  </li>
                );
              })}
            </ul>
          </section>
        ))}
        {groups.length === 0 && <p className="text-sm text-muted-foreground">No installed Plugin offers a Widget. Install one from Plugins.</p>}
      </div>
    </Sheet>
  );
}

function Placement({ placed, children }: { placed: boolean; children: ReactNode }) {
  return <div className="ml-auto">{placed ? <span className="text-xs text-muted-foreground">On the Overview</span> : children}</div>;
}
