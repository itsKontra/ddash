import { PluginScope, useAction, useQuery, type Fetch, type Status, type Widget, type WidgetSize } from "@ddash/plugin";
import { CoreService } from "@ddash/proto/ddash/core/core_pb";
import { Button, cx, Dialog, EmptyState, FormActions, HeroCard, Icon, Kicker, MetricTile, Notice, PageTitle, Panel, Pill, ProtoSheet, Ring, SearchField, Segmented, Tabs, tintOf, useVariant, WidgetCard, type HeroProblem, type Tone } from "@ddash/ui";
import { Check, GripVertical, Pencil, Plus, X } from "lucide-react";
import { useCallback, useEffect, useMemo, useRef, useState, type DragEvent, type ReactNode } from "react";
import { Link } from "react-router";
import { ErrorBoundary, ErrorFrame } from "./boundary";
import { icon } from "./icons";
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

/** The Overview's head (variant A of the first prototype). */
function OverviewHead({ headline, problems, tones, actions }: { headline: string; problems: readonly HeroProblem[]; tones: readonly Tone[]; actions: ReactNode }) {
  const up = count(tones, "up");
  const total = tones.length;
  const down = count(tones, "down");
  const warn = count(tones, "warn");
  const maint = count(tones, "maint");
  const ratio = total ? up / total : 0;
  return (
    <>
      <PageTitle kicker="Home network" title="Overview" subtitle="Every Widget's latest report, worst first in the summary. Arrange the grid with Edit." actions={actions} />
      <HeroCard
        kicker={`Status · ${today()}`}
        title={headline}
        caption={problems.length ? problems.map((p) => `${p.title}: ${p.summary}`).join(" · ") : "Every Widget on the Overview reports up."}
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

/**
 * /: the Hero over the 4-column grid of placed Widgets, with Edit mode.
 * PROTOTYPE: edit mode per ?variant= (1 Edit in place, 2 Arrange board,
 * 3 Docked library); ?edit opens it on load.
 */
export function Overview({ plugins, overview, fetch }: { plugins: Plugins; overview: OverviewLayout; fetch?: Fetch }) {
  const reach = useReach();
  const v = useVariant();
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
  const autoEdit = useRef(new URLSearchParams(window.location.search).has("edit"));
  useEffect(() => {
    if (autoEdit.current && overview.layout) {
      autoEdit.current = false;
      setDraft(overview.layout);
      if (new URLSearchParams(window.location.search).has("add")) setAdding(true);
    }
  }, [overview.layout]);

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
  const cancel = () => {
    setDraft(null);
    setAdding(false);
  };
  const add = (key: string, w: Widget) =>
    edit((l) => ({
      entries: [...l.entries, { widget: key, size: w.defaultSize }],
      seen: l.seen.includes(key) ? l.seen : [...l.seen, key],
    }));
  const resize = (key: string, size: WidgetSize) => edit((l) => ({ ...l, entries: l.entries.map((e) => (e.widget === key ? { ...e, size } : e)) }));
  const remove = (key: string) => edit((l) => ({ ...l, entries: l.entries.filter((e) => e.widget !== key) }));
  const changed = draft && JSON.stringify(draft) !== JSON.stringify(overview.layout);
  const offer = useMemo(() => offerOf(plugins), [plugins]);

  const dnd = (key: string) =>
    draft
      ? {
          draggable: true,
          onDragStart: (e: DragEvent) => {
            e.dataTransfer?.setData("text/plain", key);
            setDragging(key);
          },
          onDragOver: (e: DragEvent) => {
            if (!dragging) return;
            e.preventDefault();
            if (dragging !== key) move(dragging, key);
          },
          onDrop: (e: DragEvent) => e.preventDefault(),
          onDragEnd: () => setDragging(null),
        }
      : {};

  const editTools = (c: Card) => (
    <>
      {c.widget && c.widget.sizes.length > 1 && <Segmented label="Size" value={c.entry.size} options={c.widget.sizes} onChange={(size) => resize(c.entry.widget, size)} />}
      <Button variant="ghost" size="sm" aria-label={`Remove ${c.title}`} onClick={() => remove(c.entry.widget)}>
        <X className="h-4 w-4" aria-hidden />
      </Button>
    </>
  );

  const liveGrid = (
    <ul aria-label="Widgets" className={cx("grid grid-flow-dense auto-rows-[minmax(9rem,auto)] grid-cols-1 gap-4 sm:grid-cols-2", v === "3" && draft ? "2xl:grid-cols-3" : "xl:grid-cols-4")}>
      {cards.map((c) => (
        <li key={c.entry.widget} aria-label={c.title} className={cx(span[c.entry.size], dragging === c.entry.widget && "opacity-40", draft && "cursor-grab")} {...dnd(c.entry.widget)}>
          {draft ? (
            <div className="relative h-full rounded-ui outline-2 outline-offset-2 outline-primary/35 outline-dashed">
              <span className="absolute -top-3 left-4 z-1 inline-flex items-center gap-1 rounded-md bg-primary px-1.5 py-0.5 text-[10px] font-extrabold tracking-wider text-primary-foreground uppercase">
                <GripVertical className="h-3 w-3" aria-hidden /> {c.entry.size}
              </span>
              <WidgetCard title={c.title} status={statuses.get(c.entry.widget)} actions={editTools(c)}>
                <div className="pointer-events-none select-none">
                  <CardBody card={c} fetch={fetch} onStatus={onStatus} />
                </div>
              </WidgetCard>
            </div>
          ) : (
            <WidgetCard title={c.nav ? <Link to={`/${c.plugin!.name}`}>{c.title}</Link> : c.title} status={statuses.get(c.entry.widget)}>
              <CardBody card={c} fetch={fetch} onStatus={onStatus} />
            </WidgetCard>
          )}
        </li>
      ))}
    </ul>
  );

  const editButton = layout && (
    <Button variant="outline" onClick={() => setDraft(overview.layout!)}>
      <Pencil className="h-4 w-4" aria-hidden /> Edit
    </Button>
  );
  const saveError = draft && overview.error && <Notice tone="down" title="Saving the layout failed">{overview.error.message}</Notice>;

  /* ---------- 2: Arrange board ---------- */
  if (draft && v === "2") {
    return (
      <div className="flex flex-col gap-6">
        <PageTitle
          kicker="Editing the Overview"
          title="Arrange Widgets"
          subtitle="Drag a tile to move it, pick its size, or remove it. Widgets show live again when you're done."
          actions={
            <>
              <Button variant="ghost" onClick={cancel}>
                Cancel
              </Button>
              <Button onClick={done} disabled={!changed}>
                <Check className="h-4 w-4" aria-hidden /> Save layout
              </Button>
            </>
          }
        />
        {saveError}
        <ul aria-label="Widgets" className="grid grid-flow-dense auto-rows-[6.5rem] grid-cols-1 gap-3 sm:grid-cols-2 xl:grid-cols-4">
          {cards.map((c) => (
            <li key={c.entry.widget} aria-label={c.title} className={cx(span[c.entry.size], "cursor-grab", dragging === c.entry.widget && "opacity-40")} {...dnd(c.entry.widget)}>
              <div className="flex h-full flex-col rounded-ui border border-border bg-card p-4 shadow-panel transition-shadow hover:shadow-hero">
                <div className="flex items-start gap-3">
                  <GripVertical className="mt-0.5 h-4 w-4 shrink-0 text-muted-foreground" aria-hidden />
                  <span className={cx("grid h-8 w-8 shrink-0 place-items-center rounded-lg", tintOf[statuses.get(c.entry.widget)?.tone ?? "idle"])}>
                    <PluginGlyph name={c.plugin?.icon} />
                  </span>
                  <div className="min-w-0 flex-1">
                    <div className="truncate text-sm font-extrabold">{c.title}</div>
                    <div className="truncate text-xs text-muted-foreground">{c.plugin?.title}</div>
                  </div>
                  <Button variant="ghost" size="sm" aria-label={`Remove ${c.title}`} onClick={() => remove(c.entry.widget)}>
                    <X className="h-4 w-4" aria-hidden />
                  </Button>
                </div>
                <div className="mt-auto flex items-end justify-between gap-2 pt-2">
                  {c.widget && c.widget.sizes.length > 1 ? (
                    <Segmented label="Size" value={c.entry.size} options={c.widget.sizes} onChange={(size) => resize(c.entry.widget, size)} />
                  ) : (
                    <Pill>{c.entry.size} only</Pill>
                  )}
                  <span className="hidden text-[10px] font-bold tracking-widest text-muted-foreground/60 uppercase sm:inline">{sizeName[c.entry.size]}</span>
                </div>
                {/* Mounted to keep the Widget's status reporting while arranging. */}
                <div hidden>
                  <CardBody card={c} fetch={fetch} onStatus={onStatus} />
                </div>
              </div>
            </li>
          ))}
          <li className="col-span-1 row-span-1">
            <button
              type="button"
              onClick={() => setAdding(true)}
              className="flex h-full w-full cursor-pointer flex-col items-center justify-center gap-1.5 rounded-ui border-2 border-dashed border-primary/35 text-sm font-bold text-primary transition-colors hover:border-primary hover:bg-primary/5"
            >
              <Plus className="h-5 w-5" aria-hidden /> Add Widget
            </button>
          </li>
        </ul>
        {adding && <AddWidgetGallery offer={offer} placed={draft.entries} fetch={fetch} onClose={() => setAdding(false)} onAdd={(key, w) => add(key, w)} />}
      </div>
    );
  }

  /* ---------- 3: Docked library ---------- */
  if (draft && v === "3") {
    return (
      <div className="flex flex-col gap-6">
        <OverviewHead
          headline={h.headline}
          problems={h.problems}
          tones={cards.map((c) => statuses.get(c.entry.widget)?.tone).filter((t): t is Tone => t !== undefined)}
          actions={
            <>
              <span className="lg:hidden">
                <Button variant="outline" onClick={() => setAdding(true)}>
                  <Plus className="h-4 w-4" aria-hidden /> Add Widget
                </Button>
              </span>
              <Button variant="ghost" onClick={cancel}>
                Cancel
              </Button>
              <Button onClick={done}>
                <Check className="h-4 w-4" aria-hidden /> Done
              </Button>
            </>
          }
        />
        {saveError}
        <div className="flex items-start gap-6">
          <div className="min-w-0 flex-1">{liveGrid}</div>
          <aside className="sticky top-6 hidden w-80 shrink-0 lg:block">
            <Panel kicker="Edit mode" title="Widget library" description="Add any Widget an installed Plugin offers.">
              <WidgetList offer={offer} placed={draft.entries} onAdd={add} />
            </Panel>
          </aside>
        </div>
        {adding && (
          <ProtoSheet kicker="Overview" title="Add Widget" onClose={() => setAdding(false)}>
            <WidgetList offer={offer} placed={draft.entries} onAdd={add} />
          </ProtoSheet>
        )}
      </div>
    );
  }

  /* ---------- 1: Edit in place (and the view mode) ---------- */
  return (
    <div className="flex flex-col gap-6">
      {draft && (
        <div className="sticky top-15 z-20 -mx-4 -mt-6 px-4 pt-3 pb-1 lg:top-0 sm:-mx-8 sm:px-8 lg:-mx-11 lg:-mt-9 lg:px-11 lg:pt-5">
          <div role="toolbar" aria-label="Edit the Overview" className="flex flex-wrap items-center gap-3 rounded-2xl bg-nav px-4 py-3 text-nav-strong shadow-hero sm:px-5">
            <span className="grid h-9 w-9 place-items-center rounded-lg bg-nav-active text-nav-accent">
              <Pencil className="h-4 w-4" aria-hidden />
            </span>
            <div className="min-w-0 flex-1">
              <div className="text-sm font-extrabold">Editing the Overview</div>
              <div className="hidden text-xs text-nav-foreground sm:block">Drag a card to move it, pick its size, or remove it. {changed ? "Unsaved changes." : "No changes yet."}</div>
            </div>
            <div className="flex w-full items-center justify-end gap-2 sm:w-auto">
            <button type="button" onClick={() => setAdding(true)} className="mr-auto inline-flex h-9 sm:mr-0 cursor-pointer items-center gap-1.5 rounded-lg border border-nav-border px-3 text-sm font-bold text-nav-strong hover:bg-nav-hover">
              <Plus className="h-4 w-4" aria-hidden /> Add Widget
            </button>
            <button type="button" onClick={cancel} className="inline-flex h-9 cursor-pointer items-center rounded-lg px-3 text-sm font-bold text-nav-foreground hover:text-nav-strong">
              Cancel
            </button>
            <Button onClick={done}>
              <Check className="h-4 w-4" aria-hidden /> Done
            </Button>
            </div>
          </div>
        </div>
      )}
      <OverviewHead
        headline={h.headline}
        problems={h.problems}
        tones={cards.map((c) => statuses.get(c.entry.widget)?.tone).filter((t): t is Tone => t !== undefined)}
        actions={!draft && editButton}
      />
      {saveError}
      {liveGrid}
      {draft && adding && (
        <ProtoSheet kicker="Overview" title="Add Widget" subtitle="Every Widget the installed Plugins offer. A Widget sits on the Overview at most once." onClose={() => setAdding(false)} footer={<FormActions status={`${draft.entries.length} Widgets on the Overview`}><Button variant="outline" onClick={() => setAdding(false)}>Close</Button></FormActions>}>
          <WidgetList offer={offer} placed={draft.entries} onAdd={add} search />
        </ProtoSheet>
      )}
    </div>
  );
}

const sizeName: Record<WidgetSize, string> = { S: "small", M: "wide", L: "large" };

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

/** Every Widget the installed Plugins offer, by Plugin. */
interface Offer {
  plugin: { name: string; title: string; icon: string };
  widgets: readonly Widget[];
  loaded?: LoadedPlugin;
}

function offerOf(plugins: Plugins): Offer[] {
  return (plugins.installed ?? []).flatMap((p) => {
    const loaded = plugins.loaded.get(p.name);
    const widgets = loaded?.definition?.widgets ?? [];
    return widgets.length ? [{ plugin: { name: p.name, title: p.title, icon: p.icon }, widgets, loaded }] : [];
  });
}

function PluginGlyph({ name }: { name?: string }) {
  const I = icon(name ?? "");
  return <I className="h-4 w-4" aria-hidden />;
}

/** The picker as a list, by Plugin; used by the Sheet (1) and the docked library (3). */
function WidgetList({ offer, placed, onAdd, search }: { offer: readonly Offer[]; placed: readonly Placed[]; onAdd: (key: string, w: Widget) => void; search?: boolean }) {
  const [q, setQ] = useState("");
  const match = (w: Widget, o: Offer) => !q || `${w.title} ${o.plugin.title}`.toLowerCase().includes(q.toLowerCase());
  const groups = offer.map((o) => ({ ...o, widgets: o.widgets.filter((w) => match(w, o)) })).filter((o) => o.widgets.length);
  return (
    <div className="flex flex-col gap-5">
      {search && <SearchField label="Search Widgets" value={q} onChange={setQ} placeholder="Search Widgets…" />}
      {groups.map(({ plugin, widgets }) => (
        <section key={plugin.name} aria-label={plugin.title}>
          <div className="mb-2 flex items-center gap-2 text-[10px] font-extrabold tracking-[0.14em] text-muted-foreground uppercase">
            <PluginGlyph name={plugin.icon} />
            {plugin.title}
          </div>
          <ul className="flex flex-col gap-2">
            {widgets.map((w) => {
              const key = widgetKey(plugin.name, w.id);
              const on = placed.some((e) => e.widget === key);
              return (
                <li key={key} className={cx("flex items-center gap-3 rounded-xl border border-border px-3.5 py-3", on ? "bg-muted/50" : "bg-card")}>
                  <div className="min-w-0 flex-1">
                    <div className="truncate text-sm font-bold">{w.title}</div>
                    <div className="mt-1 flex gap-1">
                      {w.sizes.map((s) => (
                        <span key={s} className={cx("rounded px-1.5 text-[10px] font-extrabold", s === w.defaultSize ? "bg-primary/12 text-primary" : "bg-muted text-muted-foreground")}>
                          {s}
                        </span>
                      ))}
                    </div>
                  </div>
                  {on ? (
                    <Pill tone="up" dot>
                      On the Overview
                    </Pill>
                  ) : (
                    <Button size="sm" variant="outline" aria-label={`Add ${w.title}`} onClick={() => onAdd(key, w)}>
                      <Plus className="h-3.5 w-3.5" aria-hidden /> Add
                    </Button>
                  )}
                </li>
              );
            })}
          </ul>
        </section>
      ))}
      {groups.length === 0 && (
        <EmptyState icon={Plus} title={q ? "No Widget matches" : "No Widgets on offer"}>
          {q ? "Try another name." : "No installed Plugin offers a Widget. Install one from Plugins."}
        </EmptyState>
      )}
    </div>
  );
}

/** The picker as a gallery of live previews, in a Dialog (2). */
function AddWidgetGallery({ offer, placed, fetch, onClose, onAdd }: { offer: readonly Offer[]; placed: readonly Placed[]; fetch?: Fetch; onClose: () => void; onAdd: (key: string, w: Widget) => void }) {
  const [tab, setTab] = useState("all");
  const shown = offer.filter((o) => tab === "all" || o.plugin.name === tab);
  const noop = useCallback(() => {}, []);
  return (
    <Dialog size="lg" kicker="Overview" title="Add Widget" subtitle="A live preview of every Widget the installed Plugins offer." onClose={onClose}>
      <div className="mb-5 overflow-x-auto">
        <Tabs
          label="Plugin"
          value={tab}
          onChange={setTab}
          options={[{ id: "all", label: "All", count: offer.reduce((n, o) => n + o.widgets.length, 0) }, ...offer.map((o) => ({ id: o.plugin.name, label: o.plugin.title, count: o.widgets.length }))]}
        />
      </div>
      <ul className="grid grid-cols-1 gap-4 sm:grid-cols-2">
        {shown.flatMap((o) =>
          o.widgets.map((w) => {
            const key = widgetKey(o.plugin.name, w.id);
            const on = placed.some((e) => e.widget === key);
            return (
              <li key={key} className="flex flex-col overflow-hidden rounded-ui border border-border bg-background">
                <div className="pointer-events-none relative h-40 overflow-hidden p-3 select-none" aria-hidden>
                  <div className="origin-top-left scale-[0.82]" style={{ width: "122%" }}>
                    <WidgetCard title={w.title}>
                      {o.loaded && <CardBody card={{ entry: { widget: `preview:${key}`, size: w.defaultSize }, title: w.title, plugin: o.loaded, widget: w, nav: false }} fetch={fetch} onStatus={noop} />}
                    </WidgetCard>
                  </div>
                  <div className="absolute inset-x-0 bottom-0 h-10 bg-linear-to-t from-background to-transparent" />
                </div>
                <div className="flex items-center gap-3 border-t border-border bg-card px-4 py-3">
                  <div className="min-w-0 flex-1">
                    <div className="truncate text-sm font-bold">{w.title}</div>
                    <div className="text-xs text-muted-foreground">
                      {o.plugin.title} · {w.sizes.join(" / ")}
                    </div>
                  </div>
                  {on ? (
                    <Pill tone="up" dot>
                      Placed
                    </Pill>
                  ) : (
                    <Button size="sm" aria-label={`Add ${w.title}`} onClick={() => onAdd(key, w)}>
                      <Plus className="h-3.5 w-3.5" aria-hidden /> Add
                    </Button>
                  )}
                </div>
              </li>
            );
          }),
        )}
      </ul>
    </Dialog>
  );
}
