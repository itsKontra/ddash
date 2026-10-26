// The data family: tables and their action strips, tabs, status lists and facts, and the search and URL state an explorer keeps.
import { useCallback, useEffect, useId, useMemo, useRef, useState, type ReactNode, type RefObject } from "react";
import { useSearchParams } from "react-router";
import { Pill, StatusDot } from "./atoms";
import { Icon } from "./icon";
import { usePresence } from "./presence";
import { useOnHero } from "./surface";
import { cx, type Tone } from "./tone";

const mac = /^[0-9a-f]{2}([:-][0-9a-f]{2}){5}$/i;
const separators = /[:-]/g;

/**
 * Whether a search query matches a row's fields, e.g. a Device's name, IP
 * and MAC: every word of the query, ignoring case, appears in some field.
 * A MAC also matches without its separators, so "aabb" finds aa:bb:…. An
 * empty query matches everything; undefined fields are skipped.
 */
export function matches(query: string, ...fields: readonly (string | undefined)[]): boolean {
  const haystacks = fields.flatMap((f) => {
    if (f === undefined) return [];
    const lower = f.toLowerCase();
    return mac.test(f) ? [lower, lower.replace(separators, "")] : [lower];
  });
  return query
    .toLowerCase()
    .split(/\s+/)
    .filter(Boolean)
    .every((word) => {
      const bare = word.replace(separators, "");
      return haystacks.some((h) => h.includes(word) || (bare !== "" && h.includes(bare)));
    });
}

/**
 * Every table's empty state for a search that found nothing, e.g.
 * noMatch("Devices", " nas ") is 'No Devices match "nas"'. things is
 * plural, as the table's rows are.
 */
export const noMatch = (things: string, query: string) => `No ${things} match "${query.trim()}"`;

/**
 * One URL search param as state, e.g. an explorer's search, tab or sort, so
 * it survives a reload and Back and can be linked. The param is absent
 * while the value is fallback. A write replaces the history entry rather
 * than adding one, so typing in a search doesn't fill up Back.
 */
export function useQueryState(key: string, fallback: string): [string, (value: string) => void] {
  const [params, setParams] = useSearchParams();
  const set = useCallback(
    (value: string) =>
      setParams(
        (prev) => {
          const next = new URLSearchParams(prev);
          if (value === fallback) next.delete(key);
          else next.set(key, value);
          return next;
        },
        { replace: true, preventScrollReset: true },
      ),
    [key, fallback, setParams],
  );
  return [params.get(key) ?? fallback, set];
}

/** One tab of Tabs; count, when given, follows the label, e.g. "HTTP 7". */
export interface TabOption<T extends string> {
  id: T;
  label: string;
  count?: number;
}

const tabStep: Record<string, number> = { ArrowRight: 1, ArrowLeft: -1 };

/**
 * Switches a view, e.g. an explorer's kinds: a tablist (where Segmented, a
 * radio group, picks a value). It takes one tab stop; the arrow keys, Home
 * and End move along it and switch.
 */
export function Tabs<T extends string>({
  label,
  value,
  options,
  onChange,
}: {
  label: string;
  value: T;
  options: readonly TabOption<T>[];
  onChange: (value: T) => void;
}) {
  return (
    <div role="tablist" aria-label={label} className="inline-flex flex-wrap gap-1">
      {options.map((o, i) => (
        <button
          key={o.id}
          type="button"
          role="tab"
          aria-selected={o.id === value}
          tabIndex={o.id === value ? 0 : -1}
          onClick={() => onChange(o.id)}
          onKeyDown={(e) => {
            const n = options.length;
            const next = e.key === "Home" ? 0 : e.key === "End" ? n - 1 : tabStep[e.key] === undefined ? undefined : (i + tabStep[e.key] + n) % n;
            if (next === undefined) return;
            e.preventDefault();
            (e.currentTarget.parentElement?.children[next] as HTMLElement | undefined)?.focus();
            onChange(options[next].id);
          }}
          className={cx(
            "inline-flex cursor-pointer items-center gap-1.5 rounded-ui-sm px-2.5 py-1 text-xs font-semibold transition-colors",
            o.id === value ? "bg-primary-tint text-primary" : "text-muted-foreground hover:bg-muted hover:text-foreground",
          )}
        >
          {o.label}
          {o.count !== undefined && <span className={cx("tabular-nums", o.id !== value && "opacity-70")}>{o.count}</span>}
        </button>
      ))}
    </div>
  );
}

/** One entry of a StatusList: a status, what it is about, and its details. */
export interface StatusItem {
  tone: Tone;
  title: ReactNode;
  /** A line under the title, e.g. "timed out after 5s". */
  meta?: ReactNode;
  /** Right of the title, e.g. "2m ago". */
  aside?: ReactNode;
  /** Under the meta, e.g. a hop's request in a CodeBlock. */
  body?: ReactNode;
}

/**
 * A list of statuses, e.g. a config's warnings or the Overview's problems,
 * each led by its StatusDot. timeline makes it an ordered list joined by a
 * rail, e.g. an Action's hops. Titles take one line, truncated, unless
 * wrap lets them wrap, e.g. a Plan's steps.
 */
export function StatusList({ items, timeline, wrap }: { items: readonly StatusItem[]; timeline?: boolean; wrap?: boolean }) {
  const List = timeline ? "ol" : "ul";
  // On the hero the details take the caption's colour (Theme rule: hero text reuses the nav set).
  const hero = useOnHero();
  const quiet = hero ? undefined : "text-muted-foreground";
  return (
    <List
      data-timeline={timeline || undefined}
      className={cx(
        "relative flex flex-col",
        timeline ? "gap-4 before:absolute before:top-2 before:bottom-2 before:left-[0.4375rem] before:w-px before:bg-border" : "gap-3",
      )}
    >
      {items.map((it, i) => (
        <li key={i} className="relative flex min-w-0 gap-3">
          <span className={cx("mt-0.5 flex shrink-0", timeline && "rounded-full bg-card")}>
            <StatusDot tone={it.tone} />
          </span>
          <div className="min-w-0 flex-1">
            <div className="flex items-baseline justify-between gap-3">
              <div className={cx("min-w-0 text-sm font-semibold", wrap ? "break-words" : "truncate", hero && "text-nav-strong")}>{it.title}</div>
              {it.aside !== undefined && <div className={cx("shrink-0 text-xs tabular-nums", quiet)}>{it.aside}</div>}
            </div>
            {it.meta !== undefined && <div className={cx("mt-0.5 text-xs", quiet)}>{it.meta}</div>}
            {it.body !== undefined && <div className="mt-2 min-w-0">{it.body}</div>}
          </div>
        </li>
      ))}
    </List>
  );
}

/** One label and its value in Facts; mono sets the value in the monospace font, e.g. an IP. */
export interface Fact {
  label: string;
  value: ReactNode;
  mono?: boolean;
}

/** Labelled values, e.g. a Device's IP, MAC and lease: a description list, labels left of values. */
export function Facts({ items }: { items: readonly Fact[] }) {
  return (
    <dl className="grid grid-cols-[minmax(0,auto)_minmax(0,1fr)] gap-x-6 gap-y-2 text-sm">
      {items.map((f) => (
        <div key={f.label} className="contents">
          <dt className="text-muted-foreground">{f.label}</dt>
          <dd data-mono={f.mono || undefined} className={cx("min-w-0 break-words", f.mono && "font-mono text-xs leading-5")}>
            {f.value}
          </dd>
        </div>
      ))}
    </dl>
  );
}

/**
 * What a DataTable row's strip holds, e.g. a Workload's: a summary line on
 * the left, announced as it changes (role="status", e.g. "Proxmox is
 * restarting it", then "running, up 1 min"), its compact controls on the
 * right, wrapping under it on a phone, and a Notice underneath, e.g. an
 * Action's error.
 */
export function ActionStrip({ summary, notice, children }: { summary: ReactNode; notice?: ReactNode; children?: ReactNode }) {
  return (
    <div className="flex flex-col gap-2">
      <div className="flex flex-wrap items-center justify-between gap-x-6 gap-y-2">
        <div role="status" className="min-w-0 text-xs text-muted-foreground">
          {summary}
        </div>
        {children && <div className="flex flex-wrap items-center gap-2">{children}</div>}
      </div>
      {notice}
    </div>
  );
}

/** One column of a DataTable. */
export interface Column<R> {
  id: string;
  header: ReactNode;
  cell: (row: R) => ReactNode;
  align?: "left" | "right";
  /** Sets the cells in the monospace font, e.g. a target or an IP. */
  mono?: boolean;
  /**
   * Makes this column's cells the rows' headers. Put it first: the first
   * column stays put while the rest scroll sideways on a phone. A bare link
   * or button in it gets the row-header link styling.
   */
  rowHeader?: boolean;
  /** Sorts the table by this column on a click of its header; needs sortValue. */
  sortable?: boolean;
  sortValue?: (row: R) => string | number;
  /** Hides the column below the sm breakpoint, for a secondary detail. */
  hideBelow?: "sm";
}

/** What a DataTable takes. */
export type DataTableProps<R> = TableProps<R> & StripProps<R>;

interface TableProps<R> {
  rows: readonly R[];
  rowKey: (row: R) => string;
  columns: readonly Column<R>[];
  /** Makes each row clickable and keyboard-operable, e.g. to open its detail Sheet. */
  onRowClick?: (row: R) => void;
  /** Shown in place of the rows when there are none. Default "Nothing here yet". */
  empty?: ReactNode;
  /** Draws skeleton rows instead of rows (and instead of empty). */
  loading?: boolean;
  density?: "comfortable" | "compact";
  /** Keeps the sort in this URL search param (via useQueryState) instead of in the table. */
  sortKey?: string;
  /**
   * The sort while none is chosen, e.g. "name" or "-when" (see parseSort).
   * With sortKey the URL keeps no param for it, so it stays clean until a
   * header is clicked.
   */
  defaultSort?: string;
}

/** A row's inline action strip; rowLabel and busy come with it. */
type StripProps<R> =
  | { strip?: undefined; rowLabel?: undefined; busy?: undefined }
  | {
      /**
       * The row's inline action strip, e.g. an ActionStrip, opened by the
       * arrow at the row's end; null gives the row no arrow, e.g. a
       * template Workload. close closes it. Only the open row's strip is
       * mounted, so it can be a component with state of its own.
       */
      strip: (row: R, close: () => void) => ReactNode | null;
      /** Names the row's arrow and strip, "Actions for <rowLabel>", e.g. a Workload's name. */
      rowLabel: (row: R) => string;
      /**
       * What the row is busy with, e.g. "restarting": a maint Pill beside
       * its arrow, which can't open the strip meanwhile (an open one still closes).
       */
      busy?: (row: R) => string | undefined;
    };

const skeletonRows = 3;

/**
 * The table every list of things is drawn with, e.g. an explorer's Checks.
 * It owns the look: a header row (always), hover and keyboard rows,
 * sorting by a sortable column's header, skeleton rows while loading, the
 * empty state, and sideways scrolling on a phone with the first column
 * sticky. No pagination and no selection: the Plugin filters its own rows.
 *
 * With strip, each row ends in an arrow that opens its inline action strip
 * under it, one at a time (see StripRow). Which one is open is the
 * table's own, kept by rowKey: it survives a re-sort and a refetch, and is
 * forgotten once its row leaves rows.
 */
export function DataTable<R>(props: DataTableProps<R>) {
  return props.sortKey === undefined ? <LocalSort {...props} /> : <UrlSort {...props} sortKey={props.sortKey} />;
}

function LocalSort<R>(props: DataTableProps<R>) {
  const [sort, setSort] = useState<string>();
  return <Table {...props} sort={sort ?? props.defaultSort ?? ""} onSort={setSort} />;
}

function UrlSort<R>(props: DataTableProps<R> & { sortKey: string }) {
  const [sort, setSort] = useQueryState(props.sortKey, props.defaultSort ?? "");
  return <Table {...props} sort={sort} onSort={setSort} />;
}

/**
 * A sort as one string, as it sits in the URL: "<column id>" ascending,
 * "-<column id>" descending. "" is the rows' own order, only until a
 * header is clicked when there's no defaultSort: a click flips between
 * ascending and descending, never back.
 */
function parseSort(sort: string): { id: string; descending: boolean } {
  return sort.startsWith("-") ? { id: sort.slice(1), descending: true } : { id: sort, descending: false };
}

const compare = (a: string | number, b: string | number) =>
  typeof a === "number" && typeof b === "number" ? a - b : String(a).localeCompare(String(b), undefined, { numeric: true });

// The tint is translucent: laid as an image over bg-card, a sticky cell stays opaque.
const stickyCell = "z-1 bg-card group-hover:bg-linear-to-r group-hover:from-primary-tint group-hover:to-primary-tint";

function Table<R>(props: DataTableProps<R> & { sort: string; onSort: (sort: string) => void }) {
  const { rows, rowKey, columns, onRowClick, empty, loading, density = "comfortable", sort, onSort } = props;
  const strips = props.strip && { strip: props.strip, rowLabel: props.rowLabel, busy: props.busy };
  const [openKey, setOpenKey] = useState<string>();
  if (openKey !== undefined && !rows.some((r) => rowKey(r) === openKey)) setOpenKey(undefined);
  const scroller = useStripWidth(!!strips);
  const by = parseSort(sort);
  const sortColumn = columns.find((c) => c.id === by.id && c.sortable && c.sortValue);
  const shown = useMemo(() => {
    const value = sortColumn?.sortValue;
    if (!value) return rows;
    const sign = by.descending ? -1 : 1;
    return [...rows].sort((a, b) => sign * compare(value(a), value(b)));
  }, [rows, sortColumn, by.descending]);
  const pad = cx("px-3 first:pl-4 last:pr-4", density === "compact" ? "py-1.5" : "py-2.5");
  const span = columns.length + (strips ? 1 : 0);
  const place = (c: Column<R>, i: number) =>
    cx(
      pad,
      "whitespace-nowrap",
      c.align === "right" ? "text-right" : "text-left",
      c.hideBelow === "sm" && "hidden sm:table-cell",
      i === 0 && cx("sticky left-0", stickyCell),
    );
  return (
    <div ref={scroller} className="w-full overflow-x-auto">
      <table className="min-w-full text-sm">
        <thead>
          <tr className="border-b border-border text-xs text-muted-foreground">
            {columns.map((c, i) => {
              const sorted = c === sortColumn ? (by.descending ? "descending" : "ascending") : undefined;
              return (
                <th key={c.id} scope="col" aria-sort={sorted} className={cx(place(c, i), "font-semibold")}>
                  {c.sortable && c.sortValue ? (
                    <button
                      type="button"
                      onClick={() => onSort(sorted === "ascending" ? `-${c.id}` : c.id)}
                      className={cx(
                        "inline-flex cursor-pointer items-center gap-1 font-semibold hover:text-foreground",
                        c.align === "right" && "flex-row-reverse",
                        sorted && "text-foreground",
                      )}
                    >
                      {c.header}
                      <span className={cx("flex", !sorted && "opacity-40")}>
                        <Icon name={sorted === "ascending" ? "chevron-up" : sorted === "descending" ? "chevron-down" : "chevrons-up-down"} size="sm" />
                      </span>
                    </button>
                  ) : (
                    c.header
                  )}
                </th>
              );
            })}
            {strips && (
              <th scope="col" className={cx(pad, "sticky right-0 z-1 w-px bg-card")}>
                <span className="sr-only">Actions</span>
              </th>
            )}
          </tr>
        </thead>
        <tbody aria-busy={loading || undefined}>
          {loading ? (
            Array.from({ length: skeletonRows }, (_, r) => (
              <tr key={r} className="border-b border-border last:border-b-0">
                {columns.map((c, i) => (
                  <td key={c.id} className={place(c, i)}>
                    <span role={r === 0 && i === 0 ? "status" : undefined} aria-label={r === 0 && i === 0 ? "Loading" : undefined} className="block">
                      <span aria-hidden="true" className="block h-3 w-full min-w-12 rounded-ui-sm bg-muted motion-safe:animate-pulse" />
                    </span>
                  </td>
                ))}
                {strips && <td className={pad} />}
              </tr>
            ))
          ) : shown.length === 0 ? (
            <tr>
              <td colSpan={span} className="px-4 py-8 text-center text-sm text-muted-foreground">
                {empty ?? "Nothing here yet"}
              </td>
            </tr>
          ) : (
            shown.map((row) => {
              const key = rowKey(row);
              return (
                <BodyRow
                  key={key}
                  onOpen={onRowClick && (() => onRowClick(row))}
                  cells={columns.map((c, i) => {
                    const Cell = c.rowHeader ? "th" : "td";
                    return (
                      <Cell
                        key={c.id}
                        scope={c.rowHeader ? "row" : undefined}
                        className={cx(
                          place(c, i),
                          c.mono && "font-mono text-xs",
                          c.rowHeader
                            ? "font-semibold [&>a]:text-foreground [&>a:hover]:underline [&>button]:cursor-pointer [&>button]:text-left [&>button]:font-semibold [&>button:hover]:underline"
                            : "font-normal",
                        )}
                      >
                        {c.cell(row)}
                      </Cell>
                    );
                  })}
                  strip={
                    strips && {
                      render: (close) => strips.strip(row, close),
                      label: strips.rowLabel(row),
                      busy: strips.busy?.(row) || undefined,
                      open: openKey === key,
                      onOpen: (open) => setOpenKey((k) => (open ? key : k === key ? undefined : k)),
                    }
                  }
                  pad={pad}
                  span={span}
                />
              );
            })
          )}
        </tbody>
      </table>
    </div>
  );
}

/**
 * The scroller's width as --strip-width, which a strip's content takes so
 * it stays put, the scroller's width, while the table scrolls sideways.
 */
function useStripWidth(on: boolean) {
  const scroller = useRef<HTMLDivElement>(null);
  useEffect(() => {
    const el = scroller.current;
    if (!on || !el || typeof ResizeObserver === "undefined") return;
    const watch = new ResizeObserver(() => el.style.setProperty("--strip-width", `${el.clientWidth}px`));
    watch.observe(el);
    return () => watch.disconnect();
  }, [on]);
  return scroller;
}

/** One row's strip, as Table hands it to BodyRow. */
interface RowStrip {
  render: (close: () => void) => ReactNode | null;
  label: string;
  busy?: string;
  open: boolean;
  onOpen: (open: boolean) => void;
}

/**
 * One row of a DataTable: its cells, and with a strip, an end cell with
 * the strip's arrow and the strip's own row under it.
 */
function BodyRow({
  onOpen,
  cells,
  strip,
  pad,
  span,
}: {
  onOpen?: () => void;
  cells: ReactNode;
  strip?: RowStrip;
  pad: string;
  span: number;
}) {
  const id = useId();
  const arrow = useRef<HTMLButtonElement>(null);
  // Closing from inside the strip leaves the focus on the arrow, not on the inert strip.
  // By id, not by ref: the strip gets close while rendering.
  const close = () => {
    if (document.getElementById(`${id}-strip`)?.contains(document.activeElement)) document.getElementById(`${id}-arrow`)?.focus();
    strip?.onOpen(false);
  };
  const content = strip ? strip.render(close) : null;
  const open = !!strip?.open && content !== null;
  // Busy holds a closed strip shut; aria-disabled, not disabled, so the arrow keeps the focus.
  const held = strip?.busy !== undefined && !open;
  return (
    <>
      <tr
        tabIndex={onOpen ? 0 : undefined}
        onClick={onOpen}
        onKeyDown={
          onOpen &&
          ((e) => {
            if (e.target !== e.currentTarget || (e.key !== "Enter" && e.key !== " ")) return;
            e.preventDefault();
            onOpen();
          })
        }
        className={cx("group border-b border-border transition-colors last:border-b-0 hover:bg-primary-tint", onOpen && "cursor-pointer")}
      >
        {cells}
        {strip && (
          <td className={cx(pad, "sticky right-0 text-right", stickyCell)} onClick={(e) => e.stopPropagation()}>
            <span className="inline-flex items-center justify-end gap-2">
              {strip.busy !== undefined && (
                <span id={`${id}-busy`} className="flex">
                  <Pill tone="maint">{strip.busy}</Pill>
                </span>
              )}
              {content !== null && (
                <button
                  ref={arrow}
                  id={`${id}-arrow`}
                  type="button"
                  aria-label={`Actions for ${strip.label}`}
                  aria-expanded={open}
                  aria-controls={`${id}-strip`}
                  aria-disabled={held || undefined}
                  aria-describedby={strip.busy !== undefined ? `${id}-busy` : undefined}
                  onClick={() => held || strip.onOpen(!open)}
                  onKeyDown={(e) => {
                    if (e.key !== "Escape" || !open) return;
                    e.stopPropagation();
                    close();
                  }}
                  className="inline-grid size-7 cursor-pointer place-items-center rounded-ui-sm text-muted-foreground transition-colors hover:bg-muted hover:text-foreground aria-disabled:cursor-not-allowed aria-disabled:opacity-50 aria-expanded:text-foreground"
                >
                  <span className={cx("flex motion-safe:transition-transform motion-safe:duration-(--motion-slide)", open && "rotate-180")}>
                    <Icon name="chevron-down" size="sm" />
                  </span>
                </button>
              )}
            </span>
          </td>
        )}
      </tr>
      {strip && (
        <StripRow id={`${id}-strip`} label={`Actions for ${strip.label}`} span={span} arrow={arrow} onClose={close}>
          {open && content}
        </StripRow>
      )}
    </>
  );
}

/*
 * The strip's slide (tokens.css): a <tr> can't animate its height, so a
 * grid inside its cell goes from 0fr to 1fr as it enters (starting:) and
 * back as it leaves (in-data-[state=closed]:, set by the strip's
 * Presence). Under reduced motion it doesn't move at all.
 */
const stripMotion =
  "grid-rows-[1fr] starting:grid-rows-[0fr] in-data-[state=closed]:grid-rows-[0fr] motion-safe:transition-[grid-template-rows] motion-safe:duration-(--motion-slide) motion-safe:ease-(--motion-slide-ease)";

/**
 * A row's strip: a full-width, tinted row right under it, a group named
 * like its arrow so Tab enters it next. Its own Presence keeps it while it
 * slides shut (a <div> can't sit in a <tbody>, so the <tr> is Presence's
 * wrapper), and its content stays put, the scroller's width, while the
 * table scrolls sideways. Escape inside closes it, unless something
 * inside handled the Escape first (a SearchField, an overlay); and when
 * the focused control inside goes away or turns disabled, the focus moves
 * to the row's arrow.
 */
function StripRow({
  id,
  label,
  span,
  arrow,
  onClose,
  children,
}: {
  id: string;
  label: string;
  span: number;
  arrow: RefObject<HTMLButtonElement | null>;
  onClose: () => void;
  children: ReactNode;
}) {
  const { present, leaving, wrapper, content } = usePresence<HTMLTableRowElement>(children);
  const group = useRef<HTMLDivElement>(null);
  useFocusFallback(group, arrow, present && !leaving);
  if (!present) return null;
  return (
    <tr {...wrapper} className="border-b border-border last:border-b-0">
      <td colSpan={span} className="bg-primary-tint/50 p-0">
        <div className="sticky left-0 w-(--strip-width)">
          <div className={cx("grid", stripMotion)}>
            <div className="min-h-0 overflow-clip">
              <div
                ref={group}
                id={id}
                role="group"
                aria-label={label}
                onKeyDown={(e) => {
                  if (e.key !== "Escape" || e.defaultPrevented) return;
                  e.stopPropagation();
                  onClose();
                }}
                className="px-4 py-2.5"
              >
                {content}
              </div>
            </div>
          </div>
        </div>
      </td>
    </tr>
  );
}

/**
 * While on, watches group for the focused control inside it being removed
 * or disabled (a submit swapped for the summary, a confirmed Stop), which
 * drops the focus to the page, and puts the focus on arrow instead.
 */
function useFocusFallback(group: RefObject<HTMLElement | null>, arrow: RefObject<HTMLElement | null>, on: boolean) {
  useEffect(() => {
    const el = group.current;
    if (!on || !el) return;
    let focused: Element | null = null;
    const onIn = (e: FocusEvent) => (focused = e.target as Element);
    // A move elsewhere is the Operator's; a drop to nowhere is checked below.
    const onOut = (e: FocusEvent) => e.relatedTarget && (focused = null);
    const watch = new MutationObserver(() => {
      if (!focused || (focused.isConnected && !focused.matches(":disabled"))) return;
      const at = document.activeElement;
      if (at && at !== document.body && at !== focused) return;
      focused = null;
      arrow.current?.focus();
    });
    el.addEventListener("focusin", onIn);
    el.addEventListener("focusout", onOut);
    watch.observe(el, { subtree: true, childList: true, attributes: true, attributeFilter: ["disabled"] });
    return () => {
      el.removeEventListener("focusin", onIn);
      el.removeEventListener("focusout", onOut);
      watch.disconnect();
    };
  }, [group, arrow, on]);
}
