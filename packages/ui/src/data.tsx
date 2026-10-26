// The data family: tables, tabs, status lists and facts, and the URL state an explorer keeps.
import { useCallback, useMemo, useState, type ReactNode } from "react";
import { useSearchParams } from "react-router";
import { StatusDot } from "./atoms";
import { Icon } from "./icon";
import { Menu, type MenuItem } from "./overlays";
import { useOnHero } from "./surface";
import { cx, type Tone } from "./tone";

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
 * rail, e.g. an Action's hops.
 */
export function StatusList({ items, timeline }: { items: readonly StatusItem[]; timeline?: boolean }) {
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
              <div className={cx("min-w-0 truncate text-sm font-semibold", hero && "text-nav-strong")}>{it.title}</div>
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
export interface DataTableProps<R> {
  rows: readonly R[];
  rowKey: (row: R) => string;
  columns: readonly Column<R>[];
  /** Makes each row clickable and keyboard-operable, e.g. to open its detail Sheet. */
  onRowClick?: (row: R) => void;
  /** The row's Actions, in its ⋯ Menu. */
  actions?: (row: R) => readonly MenuItem[];
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

const skeletonRows = 3;

/**
 * The table every list of things is drawn with, e.g. an explorer's Checks.
 * It owns the look: a header row (always), hover and keyboard rows,
 * sorting by a sortable column's header, skeleton rows while loading, the
 * empty state, and sideways scrolling on a phone with the first column
 * sticky. No pagination and no selection: the Plugin filters its own rows.
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

function Table<R>({
  rows,
  rowKey,
  columns,
  onRowClick,
  actions,
  empty,
  loading,
  density = "comfortable",
  sort,
  onSort,
}: DataTableProps<R> & { sort: string; onSort: (sort: string) => void }) {
  const by = parseSort(sort);
  const sortColumn = columns.find((c) => c.id === by.id && c.sortable && c.sortValue);
  const shown = useMemo(() => {
    const value = sortColumn?.sortValue;
    if (!value) return rows;
    const sign = by.descending ? -1 : 1;
    return [...rows].sort((a, b) => sign * compare(value(a), value(b)));
  }, [rows, sortColumn, by.descending]);
  const pad = cx("px-3 first:pl-4 last:pr-4", density === "compact" ? "py-1.5" : "py-2.5");
  const span = columns.length + (actions ? 1 : 0);
  const place = (c: Column<R>, i: number) =>
    cx(
      pad,
      "whitespace-nowrap",
      c.align === "right" ? "text-right" : "text-left",
      c.hideBelow === "sm" && "hidden sm:table-cell",
      // The tint is translucent: laid as an image over bg-card, the sticky cell stays opaque.
      i === 0 && "sticky left-0 z-1 bg-card group-hover:bg-linear-to-r group-hover:from-primary-tint group-hover:to-primary-tint",
    );
  return (
    <div className="w-full overflow-x-auto">
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
            {actions && (
              <th scope="col" className={cx(pad, "w-px")}>
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
                {actions && <td className={pad} />}
              </tr>
            ))
          ) : shown.length === 0 ? (
            <tr>
              <td colSpan={span} className="px-4 py-8 text-center text-sm text-muted-foreground">
                {empty ?? "Nothing here yet"}
              </td>
            </tr>
          ) : (
            shown.map((row) => (
              <tr
                key={rowKey(row)}
                tabIndex={onRowClick ? 0 : undefined}
                onClick={onRowClick && (() => onRowClick(row))}
                onKeyDown={
                  onRowClick &&
                  ((e) => {
                    if (e.target !== e.currentTarget || (e.key !== "Enter" && e.key !== " ")) return;
                    e.preventDefault();
                    onRowClick(row);
                  })
                }
                className={cx("group border-b border-border transition-colors last:border-b-0 hover:bg-primary-tint", onRowClick && "cursor-pointer")}
              >
                {columns.map((c, i) => {
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
                {actions && (
                  <td className={cx(pad, "text-right")} onClick={(e) => e.stopPropagation()} onKeyDown={(e) => e.stopPropagation()}>
                    <Menu label={`Actions for ${rowKey(row)}`} items={actions(row)} />
                  </td>
                )}
              </tr>
            ))
          )}
        </tbody>
      </table>
    </div>
  );
}
