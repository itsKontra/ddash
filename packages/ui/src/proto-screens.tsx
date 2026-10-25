// PROTOTYPE (wayfinder: "What do the remaining screens look like in the
// Control room language?"): rough cuts of the decided @ddash/ui components
// the remaining screens are built from (Notice, EmptyState, CodeBlock, the
// form family, DataTable, StatusList, Facts, SectionHeading, Dialog, Sheet
// with a foot). Throwaway; lives on prototype/ui-screens only.
import { AlertTriangle, ArrowLeft, ArrowDown, ArrowUp, Check, ChevronsUpDown, Copy, Info, OctagonAlert, X, type LucideIcon } from "lucide-react";
import { createContext, useContext, useId, useState, type InputHTMLAttributes, type ReactNode, type SelectHTMLAttributes, type TextareaHTMLAttributes } from "react";
import { Button, cx, Menu, type MenuItem, type Tone } from "./index";
import { Pill } from "./proto";

export const tintOf: Record<Tone, string> = {
  up: "bg-up/12 text-up",
  down: "bg-down/12 text-down",
  warn: "bg-warn/12 text-warn",
  maint: "bg-maint/12 text-maint",
  idle: "bg-muted text-muted-foreground",
};

/* ---------- feedback ---------- */

const noticeIcon: Record<"info" | "warn" | "down", LucideIcon> = { info: Info, warn: AlertTriangle, down: OctagonAlert };
const noticeTint = { info: "border-info/25 bg-info/8 text-info", warn: "border-warn/25 bg-warn/10 text-warn", down: "border-down/25 bg-down/8 text-down" };

/** An inline message: info, a partial answer (warn), or a failure (down). */
export function Notice({ tone, title, children }: { tone: "info" | "warn" | "down"; title?: ReactNode; children?: ReactNode }) {
  const I = noticeIcon[tone];
  return (
    <div role={tone === "down" ? "alert" : "status"} className={cx("flex gap-3 rounded-ui border px-4 py-3 text-sm", noticeTint[tone])}>
      <I className="mt-0.5 h-4 w-4 shrink-0" aria-hidden />
      <div className="min-w-0 flex-1">
        {title && <div className="font-bold">{title}</div>}
        {children && <div className={cx("text-foreground/80", !!title && "mt-0.5")}>{children}</div>}
      </div>
    </div>
  );
}

/** Nothing to show yet, and what to do about it. */
export function EmptyState({ icon: I, title, children, action }: { icon?: LucideIcon; title: ReactNode; children?: ReactNode; action?: ReactNode }) {
  return (
    <div className="flex flex-col items-center px-6 py-10 text-center">
      {I && (
        <span className="mb-3 grid h-11 w-11 place-items-center rounded-xl bg-primary/10 text-primary">
          <I className="h-5 w-5" aria-hidden />
        </span>
      )}
      <div className="text-sm font-extrabold">{title}</div>
      {children && <div className="mt-1 max-w-sm text-xs leading-relaxed text-muted-foreground">{children}</div>}
      {action && <div className="mt-4">{action}</div>}
    </div>
  );
}

/** A monospace block that scrolls sideways, with an optional Copy. */
export function CodeBlock({ children, label, copy }: { children: string; label?: string; copy?: boolean }) {
  const [copied, setCopied] = useState(false);
  return (
    <div className="relative min-w-0">
      <pre aria-label={label} className="max-h-80 overflow-auto rounded-ui border border-border bg-muted/60 p-3 pr-12 font-mono text-xs leading-relaxed text-foreground/90">
        {children}
      </pre>
      {copy && (
        <button
          type="button"
          aria-label={copied ? "Copied" : "Copy"}
          onClick={() => void navigator.clipboard?.writeText(children).then(() => setCopied(true))}
          className="absolute top-2 right-2 grid h-7 w-7 cursor-pointer place-items-center rounded-md border border-border bg-card text-muted-foreground hover:text-foreground"
        >
          {copied ? <Check className="h-3.5 w-3.5" aria-hidden /> : <Copy className="h-3.5 w-3.5" aria-hidden />}
        </button>
      )}
    </div>
  );
}

/* ---------- layout ---------- */

/** A heading between Panels, with its kicker. */
export function SectionHeading({ kicker, title, actions }: { kicker?: string; title: ReactNode; actions?: ReactNode }) {
  return (
    <div className="mt-2 mb-3 flex items-end gap-3">
      <div>
        {kicker && <div className="mb-1 text-[10px] font-extrabold tracking-[0.14em] text-primary uppercase">{kicker}</div>}
        <h2 className="text-lg font-extrabold tracking-tight">{title}</h2>
      </div>
      {actions && <div className="ml-auto flex items-center gap-2">{actions}</div>}
    </div>
  );
}

/* ---------- forms ---------- */

const FieldContext = createContext<{ id: string; describedBy?: string; invalid: boolean } | null>(null);
const useFieldWiring = () => {
  const f = useContext(FieldContext);
  return f ? { id: f.id, "aria-describedby": f.describedBy, "aria-invalid": f.invalid || undefined } : {};
};

/** A label over its control, with hint and error wired in. */
export function Field({ label, hint, error, required, aside, children }: { label: ReactNode; hint?: ReactNode; error?: string; required?: boolean; aside?: ReactNode; children: ReactNode }) {
  const id = useId();
  const describedBy = [hint && `${id}-hint`, error && `${id}-error`].filter(Boolean).join(" ") || undefined;
  return (
    <FieldContext.Provider value={{ id, describedBy, invalid: !!error }}>
      <div>
        <div className="mb-1.5 flex items-center gap-2">
          <label htmlFor={id} className="text-xs font-bold text-foreground/85">
            {label}
          </label>
          {required && <span className="text-[10px] font-bold tracking-wide text-muted-foreground uppercase">required</span>}
          {aside && <span className="ml-auto">{aside}</span>}
        </div>
        {children}
        {hint && !error && (
          <p id={`${id}-hint`} className="mt-1.5 text-xs leading-relaxed text-muted-foreground">
            {hint}
          </p>
        )}
        {error && (
          <p id={`${id}-error`} className="mt-1.5 text-xs font-semibold text-down">
            {error}
          </p>
        )}
      </div>
    </FieldContext.Provider>
  );
}

const control =
  "w-full rounded-lg border border-border bg-card px-3 text-sm text-foreground outline-none transition-colors placeholder:text-muted-foreground hover:border-foreground/25 focus:border-primary focus:ring-3 focus:ring-primary/15 aria-invalid:border-down aria-invalid:ring-down/15";

export function TextInput(props: Omit<InputHTMLAttributes<HTMLInputElement>, "className">) {
  return <input type="text" {...useFieldWiring()} {...props} className={cx(control, "h-10")} />;
}

export function TextArea(props: Omit<TextareaHTMLAttributes<HTMLTextAreaElement>, "className">) {
  return <textarea {...useFieldWiring()} {...props} className={cx(control, "min-h-24 py-2")} />;
}

/** A native select; with label and no Field around it, the compact toolbar select. */
export function Select({ label, children, ...props }: Omit<SelectHTMLAttributes<HTMLSelectElement>, "className"> & { label?: string }) {
  const wiring = useFieldWiring();
  const select = (
    <span className="relative block min-w-0">
      <select {...wiring} aria-label={label} {...props} className={cx(control, "h-10 cursor-pointer appearance-none pr-9 font-semibold", label && "text-muted-foreground")}>
        {children}
      </select>
      <ChevronsUpDown className="pointer-events-none absolute top-1/2 right-3 h-3.5 w-3.5 -translate-y-1/2 text-muted-foreground" aria-hidden />
    </span>
  );
  return select;
}

export function Checkbox({ label, checked, onChange }: { label: ReactNode; checked: boolean; onChange: (checked: boolean) => void }) {
  return (
    <label className="inline-flex cursor-pointer items-center gap-2.5 text-sm">
      <span className={cx("grid h-4.5 w-4.5 place-items-center rounded-[5px] border transition-colors", checked ? "border-primary bg-primary text-primary-foreground" : "border-foreground/30 bg-card")}>
        {checked && <Check className="h-3 w-3" strokeWidth={3} aria-hidden />}
      </span>
      <input type="checkbox" className="sr-only" checked={checked} onChange={(e) => onChange(e.target.checked)} />
      {label}
    </label>
  );
}

/** The right-aligned Cancel/Save foot of a form. */
export function FormActions({ children, status }: { children: ReactNode; status?: ReactNode }) {
  return (
    <div className="flex flex-wrap items-center gap-2">
      {status && <div className="min-w-0 flex-1 text-xs text-muted-foreground">{status}</div>}
      <div className="ml-auto flex items-center gap-2">{children}</div>
    </div>
  );
}

/* ---------- data ---------- */

export interface Column<R> {
  id: string;
  header: string;
  cell: (row: R) => ReactNode;
  align?: "end";
  mono?: boolean;
  rowHeader?: boolean;
  sortValue?: (row: R) => string | number;
  hideBelow?: "sm";
}

/** The explorer table: sticky row header on phones, sorting, ⋯ actions. */
export function DataTable<R>({
  label,
  rows,
  rowKey,
  columns,
  onRowClick,
  actions,
  empty,
  density = "comfortable",
}: {
  label: string;
  rows: readonly R[];
  rowKey: (r: R) => string;
  columns: readonly Column<R>[];
  onRowClick?: (r: R) => void;
  actions?: (r: R) => MenuItem[];
  empty?: ReactNode;
  density?: "comfortable" | "compact";
}) {
  const [sort, setSort] = useState<{ id: string; dir: 1 | -1 } | null>(null);
  const col = sort && columns.find((c) => c.id === sort.id);
  const sorted = col?.sortValue ? [...rows].sort((a, b) => (col.sortValue!(a) > col.sortValue!(b) ? 1 : -1) * sort!.dir) : rows;
  const py = density === "compact" ? "py-2" : "py-3.5";
  const hide = (c: Column<R>) => c.hideBelow && "hidden sm:table-cell";
  return (
    <div className="overflow-x-auto">
      <table aria-label={label} className="w-full text-sm whitespace-nowrap">
        <thead className="bg-muted/50 text-left text-[11px] font-bold text-muted-foreground">
          <tr className="border-b border-border">
            {columns.map((c, i) => (
              <th
                key={c.id}
                scope="col"
                aria-sort={sort?.id === c.id ? (sort.dir === 1 ? "ascending" : "descending") : undefined}
                className={cx("py-3 font-bold", i === 0 ? "sticky left-0 z-1 bg-muted pl-5 pr-3 sm:bg-transparent" : "px-3", c.align === "end" && "text-right", hide(c))}
              >
                {c.sortValue ? (
                  <button type="button" className="inline-flex cursor-pointer items-center gap-1 hover:text-foreground" onClick={() => setSort((s) => (s?.id === c.id ? (s.dir === 1 ? { id: c.id, dir: -1 } : null) : { id: c.id, dir: 1 }))}>
                    {c.header}
                    {sort?.id === c.id ? sort.dir === 1 ? <ArrowUp className="h-3 w-3" /> : <ArrowDown className="h-3 w-3" /> : <ChevronsUpDown className="h-3 w-3 opacity-50" />}
                  </button>
                ) : (
                  c.header
                )}
              </th>
            ))}
            {actions && <th className="w-12 pr-5" />}
          </tr>
        </thead>
        <tbody>
          {sorted.map((r) => (
            <tr
              key={rowKey(r)}
              tabIndex={onRowClick ? 0 : undefined}
              onClick={onRowClick && (() => onRowClick(r))}
              onKeyDown={onRowClick && ((e) => e.key === "Enter" && onRowClick(r))}
              className={cx("group border-b border-border last:border-b-0", onRowClick && "cursor-pointer outline-none hover:bg-primary/5 focus-visible:bg-primary/5")}
            >
              {columns.map((c, i) => {
                const Cell = c.rowHeader ? "th" : "td";
                return (
                  <Cell
                    key={c.id}
                    scope={c.rowHeader ? "row" : undefined}
                    className={cx(
                      py,
                      "text-left align-middle",
                      i === 0 ? "sticky left-0 z-1 bg-card pr-3 pl-5 group-hover:bg-[color-mix(in_srgb,var(--primary)_5%,var(--card))] sm:bg-transparent" : "px-3",
                      c.rowHeader && "font-bold",
                      c.mono && "font-mono text-xs text-muted-foreground",
                      c.align === "end" && "text-right tabular-nums",
                      hide(c),
                    )}
                  >
                    {c.cell(r)}
                  </Cell>
                );
              })}
              {actions && (
                <td className={cx(py, "pr-5 text-right")} onClick={(e) => e.stopPropagation()}>
                  <Menu label="Actions" items={actions(r)} />
                </td>
              )}
            </tr>
          ))}
        </tbody>
      </table>
      {sorted.length === 0 && empty}
    </div>
  );
}

export interface StatusItem {
  tone: Tone;
  title: ReactNode;
  meta?: ReactNode;
  aside?: ReactNode;
  icon?: LucideIcon;
  /** PROTOTYPE: content under the item, e.g. a hop's request. */
  body?: ReactNode;
}

/** A list of things with a status; timeline draws the connecting rail. */
export function StatusList({ items, timeline }: { items: readonly StatusItem[]; timeline?: boolean }) {
  return (
    <ol className={cx("relative flex flex-col", timeline ? "gap-5 before:absolute before:top-3 before:bottom-3 before:left-3.5 before:w-px before:bg-border" : "divide-y divide-border")}>
      {items.map((it, i) => {
        const I = it.icon;
        return (
          <li key={i} className={cx("relative flex gap-3.5", !timeline && "py-3 first:pt-0 last:pb-0")}>
            {timeline ? (
              <span className={cx("relative z-1 grid h-7 w-7 shrink-0 place-items-center rounded-full border-3 border-card outline-1 outline-border", tintOf[it.tone])}>
                {I ? <I className="h-3.5 w-3.5" aria-hidden /> : <span className="h-1.5 w-1.5 rounded-full bg-current" />}
              </span>
            ) : (
              <span className={cx("mt-1.5 h-2 w-2 shrink-0 rounded-full", { up: "bg-up", down: "bg-down", warn: "bg-warn", maint: "bg-maint", idle: "bg-muted-foreground/50" }[it.tone])} />
            )}
            <div className={cx("min-w-0 flex-1", timeline && "pt-0.5")}>
              <div className="flex items-center justify-between gap-3">
                <div className="min-w-0 truncate text-sm font-bold">{it.title}</div>
                {it.aside && <div className="flex shrink-0 items-center gap-2 text-xs font-bold text-muted-foreground tabular-nums">{it.aside}</div>}
              </div>
              {it.meta && <div className="mt-0.5 text-xs text-muted-foreground">{it.meta}</div>}
              {it.body && <div className="mt-2">{it.body}</div>}
            </div>
          </li>
        );
      })}
    </ol>
  );
}

/** Label/value pairs, a <dl>. */
export function Facts({ items }: { items: readonly { label: string; value: ReactNode; mono?: boolean }[] }) {
  return (
    <dl className="grid grid-cols-[auto_minmax(0,1fr)] gap-x-6 gap-y-2.5 text-sm">
      {items.map((f) => (
        <div key={f.label} className="contents">
          <dt className="text-xs font-bold text-muted-foreground">{f.label}</dt>
          <dd className={cx("min-w-0 truncate", f.mono && "font-mono text-xs")}>{f.value}</dd>
        </div>
      ))}
    </dl>
  );
}

/* ---------- overlays ---------- */

/** A drawer from the right (a bottom sheet on phones), with a sticky foot. */
export function ProtoSheet({
  kicker,
  title,
  subtitle,
  onClose,
  back,
  footer,
  wide,
  children,
}: {
  kicker?: string;
  title: ReactNode;
  subtitle?: ReactNode;
  onClose: () => void;
  back?: { label: string; onClick: () => void };
  footer?: ReactNode;
  wide?: boolean;
  children: ReactNode;
}) {
  const id = useId();
  return (
    <div className="fixed inset-0 z-50 flex items-end justify-end bg-[#060b16]/55 backdrop-blur-[2px] sm:items-stretch" onClick={onClose} onKeyDown={(e) => e.key === "Escape" && onClose()}>
      <div
        role="dialog"
        aria-modal="true"
        aria-labelledby={id}
        className={cx("flex max-h-[92vh] w-full flex-col rounded-t-2xl border-border bg-card shadow-hero sm:h-full sm:max-h-none sm:rounded-none sm:border-l", wide ? "sm:max-w-2xl" : "sm:max-w-lg")}
        onClick={(e) => e.stopPropagation()}
      >
        <div className="mx-auto mt-2.5 h-1 w-10 rounded-full bg-border sm:hidden" />
        <header className="flex items-start gap-3 border-b border-border px-6 pt-5 pb-4">
          <div className="min-w-0">
            {back && (
              <button type="button" onClick={back.onClick} className="mb-2 inline-flex cursor-pointer items-center gap-1.5 text-xs font-bold text-muted-foreground hover:text-foreground">
                <ArrowLeft className="h-3.5 w-3.5" aria-hidden /> {back.label}
              </button>
            )}
            {kicker && <div className="mb-1.5 text-[10px] font-extrabold tracking-[0.15em] text-primary uppercase">{kicker}</div>}
            <h2 id={id} className="text-xl leading-tight font-extrabold tracking-tight">
              {title}
            </h2>
            {subtitle && <p className="mt-1 text-xs leading-relaxed text-muted-foreground">{subtitle}</p>}
          </div>
          <button type="button" aria-label="Close" autoFocus onClick={onClose} className="ml-auto grid h-8 w-8 shrink-0 cursor-pointer place-items-center rounded-lg text-muted-foreground hover:bg-muted hover:text-foreground">
            <X className="h-4 w-4" aria-hidden />
          </button>
        </header>
        <div className="min-h-0 flex-1 overflow-y-auto px-6 py-5">{children}</div>
        {footer && <footer className="border-t border-border bg-background/60 px-6 py-3.5">{footer}</footer>}
      </div>
    </div>
  );
}

/** A centred dialog sharing the Sheet's overlay; a bottom sheet on phones. */
export function Dialog({ kicker, title, subtitle, onClose, footer, size = "md", icon, children }: { kicker?: string; title: ReactNode; subtitle?: ReactNode; onClose: () => void; footer?: ReactNode; size?: "sm" | "md" | "lg"; icon?: { tone: Tone; icon: LucideIcon }; children?: ReactNode }) {
  const id = useId();
  const I = icon?.icon;
  return (
    <div className="fixed inset-0 z-50 flex items-end justify-center bg-[#060b16]/55 backdrop-blur-[2px] sm:items-center sm:p-6" onClick={onClose} onKeyDown={(e) => e.key === "Escape" && onClose()}>
      <div
        role="dialog"
        aria-modal="true"
        aria-labelledby={id}
        className={cx("flex max-h-[92vh] w-full flex-col rounded-t-2xl border border-border bg-card shadow-hero sm:max-h-[86vh] sm:rounded-2xl", { sm: "sm:max-w-md", md: "sm:max-w-xl", lg: "sm:max-w-4xl" }[size])}
        onClick={(e) => e.stopPropagation()}
      >
        <header className="flex items-start gap-4 px-6 pt-6 pb-2">
          {I && (
            <span className={cx("grid h-10 w-10 shrink-0 place-items-center rounded-xl", tintOf[icon.tone])}>
              <I className="h-5 w-5" aria-hidden />
            </span>
          )}
          <div className="min-w-0 flex-1">
            {kicker && <div className="mb-1.5 text-[10px] font-extrabold tracking-[0.15em] text-primary uppercase">{kicker}</div>}
            <h2 id={id} className="text-lg leading-tight font-extrabold tracking-tight">
              {title}
            </h2>
            {subtitle && <p className="mt-1.5 text-sm leading-relaxed text-muted-foreground">{subtitle}</p>}
          </div>
          <button type="button" aria-label="Close" onClick={onClose} className="-mt-1 -mr-2 grid h-8 w-8 shrink-0 cursor-pointer place-items-center rounded-lg text-muted-foreground hover:bg-muted hover:text-foreground">
            <X className="h-4 w-4" aria-hidden />
          </button>
        </header>
        {children && <div className="min-h-0 flex-1 overflow-y-auto px-6 py-4">{children}</div>}
        {footer && <footer className={cx("px-6 pt-2 pb-5", children ? "border-t border-border pt-4" : "pt-4")}>{footer}</footer>}
      </div>
    </div>
  );
}

/** PROTOTYPE: Pill tones by a Component's state. */
export const stateTone = (state: string): Tone =>
  ({ ok: "up", degraded: "warn", unconfigured: "warn", "needs config": "warn", down: "down", crashed: "down", "update pending": "maint" } as Record<string, Tone>)[state] ?? "idle";

export const StatePill = ({ state }: { state: string }) => (
  <Pill tone={stateTone(state)} dot>
    {state}
  </Pill>
);

