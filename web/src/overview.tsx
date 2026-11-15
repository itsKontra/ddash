import { CoreService } from "@ddash/proto/ddash/core/core_pb";
import { PluginScope, useAction, useQuery, type Action, type Fetch, type QueryClient, type Status, type Widget, type WidgetSize } from "@ddash/plugin";
import {
  Button,
  cx,
  EmptyState,
  ErrorFrame,
  FormActions,
  HeroCard,
  Icon,
  noMatch,
  Notice,
  PageTitle,
  Pill,
  Presence,
  SearchField,
  Segmented,
  Sheet,
  Skeleton,
  StatusDot,
  WidgetCard,
  type IconName,
} from "@ddash/ui";
import { Fragment, useCallback, useEffect, useMemo, useRef, useState, useSyncExternalStore } from "react";
import { Link } from "react-router";
import { ErrorBoundary } from "./boundary";
import { hero, oldestRead, readAgo, type HeroState } from "./hero";
import { reconcile, widgetKey, type Layout, type Placed } from "./layout";
import { NeedsYou, useConcernList } from "./needs-you";
import type { LoadedPlugin, Plugins } from "./plugins";
import { useReach } from "./reach";
import { Recently } from "./recently";

/** The Overview layout as the shell shows it, and how to save a new one. */
export interface OverviewLayout {
  /** Undefined until the core answered with the layout and the installed Plugins. */
  layout?: Layout;
  /** Saves layout; resolves to whether the core took it. */
  save: (layout: Layout) => Promise<boolean>;
  /** Why the last save failed. */
  error?: Action<typeof CoreService.method.saveLayout>["error"];
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

// On phones the grid has one column, so every size fits in it.
const span: Record<WidgetSize, string> = {
  S: "col-span-1 row-span-1",
  M: "col-span-1 row-span-1 sm:col-span-2",
  L: "col-span-1 row-span-2 sm:col-span-2",
};

/** /: the hero and "Needs you" over the grid of placed Widgets (1, 2 or 4 columns by width), with Edit mode, and the last Actions under it. */
export function Overview({ plugins, overview, fetch, client }: { plugins: Plugins; overview: OverviewLayout; fetch?: Fetch; client: QueryClient }) {
  const reach = useReach();
  // While editing, the Operator's unsaved layout.
  const [draft, setDraft] = useState<Layout | null>(null);
  const [adding, setAdding] = useState(false);
  const [dragging, setDragging] = useState<string | null>(null);
  const [statuses, setStatuses] = useState<ReadonlyMap<string, Status>>(new Map());
  const { listed: concerns } = useConcernList();
  const onStatus = useCallback((key: string, status: Status | undefined) => {
    setStatuses((prev) => {
      const old = prev.get(key);
      if (JSON.stringify(old) === JSON.stringify(status)) return prev;
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
    cards.map((c) => ({ plugin: pluginOf(c.entry.widget), title: c.title, status: statuses.get(c.entry.widget) })),
    concerns,
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
    if (changed && !(await overview.save(draft))) return;
    setDraft(null);
    setAdding(false);
  };
  const cancel = () => {
    setDraft(null);
    setAdding(false);
  };
  const changed = !!draft && JSON.stringify(draft) !== JSON.stringify(overview.layout);
  const resize = (key: string, size: WidgetSize) => edit((l) => ({ ...l, entries: l.entries.map((e) => (e.widget === key ? { ...e, size } : e)) }));
  const remove = (key: string) => edit((l) => ({ ...l, entries: l.entries.filter((e) => e.widget !== key) }));

  return (
    <div className="flex flex-col gap-6">
      {draft && <EditBar changed={changed} onAdd={() => setAdding(true)} onCancel={cancel} onDone={done} />}
      <PageTitle
        kicker="Main"
        title="Overview"
        actions={
          layout &&
          !draft && (
            <Button variant="outline" icon="pencil" onClick={() => setDraft(overview.layout!)}>
              Edit
            </Button>
          )
        }
      />
      {draft && overview.error && (
        <Notice tone="down" title="Saving the layout failed">
          {overview.error.rawMessage}
        </Notice>
      )}
      <OverviewHero state={h} client={client} />
      <NeedsYou />
      <ul aria-label="Widgets" className="grid auto-rows-[minmax(9rem,auto)] grid-cols-1 gap-4 grid-flow-dense sm:grid-cols-2 xl:grid-cols-4">
        {cards.map((c) => {
          const key = c.entry.widget;
          const body = <CardBody card={c} fetch={fetch} onStatus={onStatus} />;
          return (
            <li
              key={key}
              aria-label={c.title}
              className={cx(span[c.entry.size], dragging === key && "opacity-40", draft && "cursor-grab")}
              draggable={draft ? true : undefined}
              onDragStart={(e) => {
                e.dataTransfer?.setData("text/plain", key);
                setDragging(key);
              }}
              onDragOver={(e) => {
                if (!dragging) return;
                e.preventDefault();
                if (dragging !== key) move(dragging, key);
              }}
              onDrop={(e) => e.preventDefault()}
              onDragEnd={() => setDragging(null)}
            >
              {draft ? (
                <div className="relative h-full rounded-ui outline-2 outline-offset-2 outline-primary/35 outline-dashed">
                  <span
                    data-size-tab
                    className="absolute -top-3 left-4 z-1 inline-flex items-center gap-1 rounded-ui-sm bg-primary px-1.5 py-0.5 text-[10px] font-extrabold tracking-wider text-primary-foreground uppercase"
                  >
                    <Icon name="grip-vertical" size="sm" />
                    {c.entry.size}
                  </span>
                  <WidgetCard
                    title={c.title}
                    status={statuses.get(key)}
                    actions={
                      <>
                        {c.widget && c.widget.sizes.length > 1 && (
                          <Segmented label="Size" value={c.entry.size} options={c.widget.sizes} onChange={(size) => resize(key, size)} />
                        )}
                        <Button variant="ghost" size="sm" icon="x" aria-label={`Remove ${c.title}`} onClick={() => remove(key)} />
                      </>
                    }
                  >
                    {/* Live, but out of reach: a click or Tab can't act on the Widget while arranging. */}
                    <div inert className="select-none">
                      {body}
                    </div>
                  </WidgetCard>
                </div>
              ) : (
                <WidgetCard title={c.nav ? <Link to={`/${c.plugin!.name}`}>{c.title}</Link> : c.title} status={statuses.get(key)}>
                  {body}
                </WidgetCard>
              )}
            </li>
          );
        })}
      </ul>
      <Recently />
      <Presence>
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
            }}
          />
        )}
      </Presence>
    </div>
  );
}

/**
 * Edit mode's bar, pinned under the top bar: what's being edited, whether
 * the draft differs from the saved layout, and Add Widget, Cancel and Done.
 * On phones the title takes a line and the buttons wrap below it.
 */
function EditBar({ changed, onAdd, onCancel, onDone }: { changed: boolean; onAdd: () => void; onCancel: () => void; onDone: () => void }) {
  return (
    <div className="sticky top-15 z-20 -mx-4 -mt-6 px-4 pt-3 pb-1 sm:-mx-8 sm:px-8 lg:top-0 lg:-mx-11 lg:-mt-9 lg:px-11 lg:pt-5">
      <div
        role="toolbar"
        aria-label="Edit the Overview"
        className="flex flex-wrap items-center gap-3 rounded-ui-lg bg-nav px-4 py-3 text-nav-strong shadow-elevation-raised sm:px-5"
      >
        <span className="grid h-9 w-9 place-items-center rounded-ui-sm bg-nav-active text-nav-accent">
          <Icon name="pencil" />
        </span>
        <div className="min-w-0 flex-1">
          <div className="text-sm font-extrabold">Editing the Overview</div>
          <div className="text-xs text-nav-foreground">
            <span className="hidden sm:inline">Drag a card to move it, pick its size, or remove it. </span>
            {changed ? "Unsaved changes." : "No changes yet."}
          </div>
        </div>
        <div className="flex w-full items-center justify-end gap-2 sm:w-auto">
          <button
            type="button"
            onClick={onAdd}
            className="mr-auto inline-flex h-9 cursor-pointer items-center gap-1.5 rounded-ui-sm border border-nav-border px-3 text-sm font-bold text-nav-strong hover:bg-nav-hover sm:mr-0"
          >
            <Icon name="plus" /> Add Widget
          </button>
          <button
            type="button"
            onClick={onCancel}
            className="inline-flex h-9 cursor-pointer items-center rounded-ui-sm px-3 text-sm font-bold text-nav-foreground hover:text-nav-strong"
          >
            Cancel
          </button>
          <Button icon="check" onClick={onDone}>
            Done
          </Button>
        </div>
      </div>
    </div>
  );
}

/**
 * The hero: today's date and the time, the sentence about the network,
 * then the overall tone's dot, the Widgets' facts and how long ago the
 * oldest data on screen was read.
 */
function OverviewHero({ state, client }: { state: HeroState; client: QueryClient }) {
  const now = useNow();
  const readAt = useOldestRead(client);
  const date = new Date(now);
  return (
    <HeroCard
      kicker={`${date.toLocaleDateString(undefined, { weekday: "long", day: "numeric", month: "long" })} · ${date.toLocaleTimeString(undefined, { hour: "2-digit", minute: "2-digit" })}`}
      title={
        <span className="block max-w-[24ch] text-balance sm:text-4xl">
          {state.sentence.map((p, i) => (
            <Fragment key={i}>
              {i > 0 && " "}
              <span className={cx(p.quiet && "font-bold text-nav-foreground")}>{p.text}</span>
            </Fragment>
          ))}
        </span>
      }
      foot={
        <>
          <StatusDot tone={state.tone} label={`Overall: ${state.tone}`} pulse={state.tone !== "up" && state.tone !== "idle"} />
          {state.facts.map((f, i) => (
            <span key={i} className="text-sm">
              <b className="font-bold text-nav-strong tabular-nums">{f.value}</b> {f.label}
            </span>
          ))}
          {readAt !== undefined && <span className="text-sm">{readAgo(now - readAt)}</span>}
        </>
      }
    />
  );
}

/** The time now, ticking every second. */
function useNow(): number {
  const [now, setNow] = useState(Date.now);
  useEffect(() => {
    const id = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(id);
  }, []);
  return now;
}

/** When the oldest data on screen was read: over the shell's QueryClient's mounted queries that refetch on a tier. */
function useOldestRead(client: QueryClient): number | undefined {
  const cache = client.getQueryCache();
  return useSyncExternalStore(
    useCallback((listener) => cache.subscribe(listener), [cache]),
    () =>
      oldestRead(
        cache.findAll({ type: "active" }).map((q) => ({
          readAt: q.state.dataUpdatedAt,
          refetches: q.observers.some((o) => typeof o.options.refetchInterval === "number" && o.options.refetchInterval > 0),
        })),
      ),
  );
}

/** The Plugin a Widget key ("docker.containers") belongs to. */
const pluginOf = (key: string) => key.slice(0, key.indexOf("."));

const count = (n: number, noun: string) => `${n} ${noun}${n === 1 ? "" : "s"}`;

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
  if (!card.plugin || !card.widget) return <Skeleton lines={3} />;
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
  const value = status?.fact?.value;
  const label = status?.fact?.label;
  useEffect(() => {
    if (tone === undefined) onStatus(widget, undefined);
    else onStatus(widget, { tone, summary: summary ?? "", ...(value !== undefined && label !== undefined && { fact: { value, label } }) });
    return () => onStatus(widget, undefined);
  }, [widget, tone, summary, value, label, onStatus]);
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
  const [q, setQ] = useState("");
  const offer = (plugins.installed ?? []).flatMap((p) => {
    const widgets = plugins.loaded.get(p.name)?.definition?.widgets ?? [];
    return widgets.length ? [{ plugin: p, widgets }] : [];
  });
  const needle = q.trim().toLowerCase();
  const groups = offer
    .map((o) => ({ ...o, widgets: o.widgets.filter((w) => `${w.title} ${o.plugin.title}`.toLowerCase().includes(needle)) }))
    .filter((o) => o.widgets.length);
  return (
    <Sheet
      kicker="Overview"
      title="Add Widget"
      subtitle="Every Widget the installed Plugins offer. A Widget sits on the Overview at most once."
      onClose={onClose}
      footer={
        <FormActions status={`${count(placed.length, "Widget")} on the Overview`}>
          <Button variant="outline" onClick={onClose}>
            Close
          </Button>
        </FormActions>
      }
    >
      {offer.length === 0 ? (
        <EmptyState icon="layout-grid" title="No Plugin offers a Widget">
          Install a Plugin with Widgets from the Catalogue.
        </EmptyState>
      ) : (
        <div className="flex flex-col gap-5">
          <SearchField label="Search Widgets" value={q} onChange={setQ} placeholder="Search Widgets…" />
          {groups.map(({ plugin, widgets }) => (
            <div key={plugin.name} role="group" aria-label={plugin.title}>
              <div className="mb-2 flex items-center gap-2 text-[10px] font-extrabold tracking-[0.14em] text-muted-foreground uppercase">
                <Icon name={plugin.icon as IconName} size="sm" />
                {plugin.title}
              </div>
              <ul className="flex flex-col gap-2">
                {widgets.map((w) => (
                  <OfferRow key={w.id} widget={w} widgetKey={widgetKey(plugin.name, w.id)} placed={placed} onAdd={onAdd} />
                ))}
              </ul>
            </div>
          ))}
          {groups.length === 0 && <EmptyState icon="search-x" title={noMatch("Widgets", q)} />}
        </div>
      )}
    </Sheet>
  );
}

/** One offered Widget: its title and sizes (the default tinted), then Add, or a Pill once it's placed. */
function OfferRow({
  widget: w,
  widgetKey: key,
  placed,
  onAdd,
}: {
  widget: Widget;
  widgetKey: string;
  placed: readonly Placed[];
  onAdd: (key: string, w: Widget) => void;
}) {
  const on = placed.some((e) => e.widget === key);
  return (
    <li className={cx("flex items-center gap-3 rounded-ui border border-border px-3.5 py-3", on ? "bg-muted" : "bg-card")}>
      <div className="min-w-0 flex-1">
        <div data-row-part className="truncate text-sm font-bold">
          {w.title}
        </div>
        <div data-row-part aria-label={`Sizes ${w.sizes.join(", ")}`} className="mt-1 flex gap-1">
          {w.sizes.map((s) => (
            <span
              key={s}
              className={cx("rounded-ui-sm px-1.5 text-[10px] font-extrabold", s === w.defaultSize ? "bg-primary-tint text-primary" : "bg-muted text-muted-foreground")}
            >
              {s}
            </span>
          ))}
        </div>
      </div>
      <div data-row-part className="shrink-0">
        {on ? (
          <Pill tone="up" dot>
            On the Overview
          </Pill>
        ) : (
          <Button size="sm" variant="outline" icon="plus" aria-label={`Add ${w.title}`} onClick={() => onAdd(key, w)}>
            Add
          </Button>
        )}
      </div>
    </li>
  );
}
