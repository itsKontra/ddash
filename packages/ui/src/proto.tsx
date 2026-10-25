// PROTOTYPE (wayfinder: "What should the shell, Overview and a Plugin Page
// look like in the new language?"): the variant switch and the candidate
// components the variants are built from. Throwaway; lives on
// prototype/ui-redesign only.
import {
  Activity,
  AlertTriangle,
  ArrowDownCircle,
  CheckCircle2,
  ChevronRight,
  Gauge,
  type LucideIcon,
  Search,
  Wrench,
} from "lucide-react";
import { useSyncExternalStore, type ReactNode } from "react";
import { cx, type Tone } from "./index";

// PROTOTYPE (wayfinder: "What do the remaining screens look like in the
// Control room language?"): the shell is variant A for good; ?variant= now
// picks between 1, 2 and 3, which each screen names for itself.
export type Variant = "1" | "2" | "3";
export const variants: readonly Variant[] = ["1", "2", "3"];

const key = "ddash.prototype.screen-variant";
const listeners = new Set<() => void>();
const valid = (v: string | null): v is Variant => v === "1" || v === "2" || v === "3";
function read(): Variant {
  const q = new URLSearchParams(window.location.search).get("variant");
  if (valid(q)) return q;
  try {
    const s = localStorage.getItem(key);
    if (valid(s)) return s;
  } catch {}
  return "1";
}
let current: Variant = typeof window === "undefined" ? "1" : read();
if (typeof window !== "undefined")
  window.addEventListener("popstate", () => {
    const v = read();
    if (v !== current) setVariant(v);
  });

export function setVariant(v: Variant) {
  current = v;
  try {
    localStorage.setItem(key, v);
  } catch {}
  listeners.forEach((l) => l());
}

/** The screen variant in use, shared by the shell and every Plugin. */
export function useVariant(): Variant {
  return useSyncExternalStore(
    (l) => {
      listeners.add(l);
      return () => listeners.delete(l);
    },
    () => current,
  );
}

/** Icons by name, so Plugins can draw them through @ddash/ui. */
export const icons = { activity: Activity, alert: AlertTriangle, down: ArrowDownCircle, up: CheckCircle2, gauge: Gauge, wrench: Wrench, search: Search, chevron: ChevronRight };
export type IconName = keyof typeof icons;
export function Icon({ name, className }: { name: IconName; className?: string }) {
  const I: LucideIcon = icons[name];
  return <I className={cx("h-4 w-4 shrink-0", className)} aria-hidden strokeWidth={1.9} />;
}

const tint: Record<Tone, string> = {
  up: "bg-up/10 text-up",
  down: "bg-down/10 text-down",
  warn: "bg-warn/12 text-warn",
  maint: "bg-maint/10 text-maint",
  idle: "bg-muted text-muted-foreground",
};

/** The small uppercase label over a heading, with its accent rule. */
export function Kicker({ children, rule = true, className }: { children: ReactNode; rule?: boolean; className?: string }) {
  return (
    <div className={cx("flex items-center gap-2.5 text-[10px] font-extrabold tracking-[0.17em] text-primary uppercase", className)}>
      {rule && <span className="block h-0.5 w-5 bg-primary" />}
      {children}
    </div>
  );
}

/** A Page's top: kicker, big title, subtitle and actions. */
export function PageTitle({ kicker, title, subtitle, actions, size = "lg", back }: { kicker?: string; title: ReactNode; subtitle?: ReactNode; actions?: ReactNode; size?: "lg" | "xl"; back?: { href: string; label: string } }) {
  return (
    <div className="mb-7 flex flex-col gap-4 sm:flex-row sm:items-end">
      <div className="min-w-0">
        {back && (
          <a href={back.href} className="mb-4 inline-flex items-center gap-1.5 text-xs font-bold text-muted-foreground hover:text-foreground">
            <ChevronRight className="h-3.5 w-3.5 rotate-180" aria-hidden /> {back.label}
          </a>
        )}
        {kicker && <Kicker className="mb-2.5">{kicker}</Kicker>}
        <h1 className={cx("leading-tight font-extrabold tracking-tight", size === "xl" ? "text-4xl sm:text-5xl" : "text-3xl sm:text-4xl")}>{title}</h1>
        {subtitle && <p className="mt-2 max-w-3xl text-sm leading-relaxed text-muted-foreground">{subtitle}</p>}
      </div>
      {actions && <div className="flex flex-wrap items-center gap-2 sm:ml-auto">{actions}</div>}
    </div>
  );
}

/** A tinted status pill: "● up", "flapping". */
export function Pill({ tone = "idle", dot, children }: { tone?: Tone; dot?: boolean; children: ReactNode }) {
  return (
    <span className={cx("inline-flex max-w-full items-center gap-1.5 truncate rounded-full px-2.5 py-0.5 text-[11px] font-bold whitespace-nowrap", tint[tone])}>
      {dot && <span className="h-1.5 w-1.5 rounded-full bg-current" />}
      {children}
    </span>
  );
}

/** A metric tile: label and tinted icon chip, a big number, a description. */
export function MetricTile({ label, value, icon, tone = "idle", desc, compact }: { label: string; value: ReactNode; icon: IconName; tone?: Tone; desc?: ReactNode; compact?: boolean }) {
  return (
    <div className={cx("rounded-ui border border-border bg-card shadow-panel", compact ? "p-4" : "p-5")}>
      <div className="flex items-center justify-between gap-2 text-xs font-bold text-muted-foreground">
        {label}
        <span className={cx("grid h-8 w-8 place-items-center rounded-lg", tint[tone === "idle" ? "idle" : tone])}>
          <Icon name={icon} />
        </span>
      </div>
      <div className={cx("mt-1 leading-tight font-extrabold tracking-tight tabular-nums", compact ? "text-2xl" : "text-3xl")}>{value}</div>
      {desc && <div className="mt-1.5 text-xs text-muted-foreground">{desc}</div>}
    </div>
  );
}

/** A soft-shadow panel with a kicker/title header. */
export function Panel({ kicker, title, description, actions, children, className, flush }: { kicker?: string; title?: ReactNode; description?: ReactNode; actions?: ReactNode; children?: ReactNode; className?: string; flush?: boolean }) {
  return (
    <section className={cx("min-w-0 overflow-hidden rounded-ui border border-border bg-card shadow-panel", className)}>
      {(title || kicker || actions) && (
        <header className={cx("flex items-start gap-4", flush ? "border-b border-border px-5 py-4" : "px-5 pt-5")}>
          <div className="min-w-0">
            {kicker && <div className="text-[10px] font-extrabold tracking-[0.12em] text-muted-foreground uppercase">{kicker}</div>}
            {title && <h2 className="text-base font-extrabold tracking-tight">{title}</h2>}
            {description && <p className="mt-1 text-xs text-muted-foreground">{description}</p>}
          </div>
          {actions && <div className="ml-auto flex shrink-0 items-center gap-2">{actions}</div>}
        </header>
      )}
      <div className={cx(!flush && "p-5")}>{children}</div>
    </section>
  );
}

/** A progress ring: ratio 0..1, with a value and a label inside. */
export function Ring({ ratio, value, label, size = 128, track = "stroke-hero-foreground/15", bar = "stroke-hero-accent", inner }: { ratio: number; value: ReactNode; label: string; size?: number; track?: string; bar?: string; inner?: string }) {
  const r = 42;
  const c = 2 * Math.PI * r;
  return (
    <div role="meter" aria-valuenow={Math.round(ratio * 100)} aria-valuemin={0} aria-valuemax={100} aria-label={label} className="relative grid shrink-0 place-items-center" style={{ width: size, height: size }}>
      <svg viewBox="0 0 100 100" className="absolute inset-0 -rotate-90">
        <circle cx="50" cy="50" r={r} fill="none" strokeWidth="8" className={track} />
        <circle cx="50" cy="50" r={r} fill="none" strokeWidth="8" strokeLinecap="round" strokeDasharray={`${c * ratio} ${c}`} className={cx(bar, "transition-all motion-reduce:transition-none")} />
      </svg>
      <div className={cx("relative text-center", inner)}>
        <div className="text-3xl leading-none font-extrabold tracking-tight">{value}</div>
        <div className="mt-1.5 text-[9px] font-bold tracking-widest uppercase opacity-70">{label}</div>
      </div>
    </div>
  );
}

const toneStroke: Record<Tone, string> = { up: "stroke-up", down: "stroke-down", warn: "stroke-warn", maint: "stroke-maint", idle: "stroke-muted" };
const toneSwatch: Record<Tone, string> = { up: "bg-up", down: "bg-down", warn: "bg-warn", maint: "bg-maint", idle: "bg-muted-foreground/40" };

/** A donut with an HTML legend; segments by tone. */
export function Donut({ segments, total, caption, size = 180 }: { segments: readonly { tone: Tone; label: string; value: number }[]; total: ReactNode; caption: string; size?: number }) {
  const sum = segments.reduce((a, s) => a + s.value, 0) || 1;
  const r = 40;
  const c = 2 * Math.PI * r;
  let at = 0;
  return (
    <div className="flex flex-wrap items-center justify-center gap-8">
      <div role="img" aria-label={`${caption}: ${segments.map((s) => `${s.value} ${s.label}`).join(", ")}`} className="relative grid shrink-0 place-items-center" style={{ width: size, height: size }}>
        <svg viewBox="0 0 100 100" className="absolute inset-0 -rotate-90">
          <circle cx="50" cy="50" r={r} fill="none" strokeWidth="11" className="stroke-muted" />
          {segments.map((s) => {
            const len = (s.value / sum) * c;
            const el = s.value > 0 && <circle key={s.label} cx="50" cy="50" r={r} fill="none" strokeWidth="11" strokeDasharray={`${len} ${c - len}`} strokeDashoffset={-at} className={toneStroke[s.tone]} />;
            at += len;
            return el;
          })}
        </svg>
        <div className="relative text-center">
          <div className="text-3xl leading-none font-extrabold tracking-tight">{total}</div>
          <div className="mt-1 text-[10px] text-muted-foreground">{caption}</div>
        </div>
      </div>
      <ul className="min-w-40 flex-1 text-xs">
        {segments.map((s) => (
          <li key={s.label} className="my-2 flex items-center gap-2.5 text-muted-foreground">
            <span className={cx("h-2.5 w-2.5 rounded-[3px]", toneSwatch[s.tone])} />
            <span className="flex-1">{s.label}</span>
            <span className="font-bold text-foreground tabular-nums">{s.value}</span>
          </li>
        ))}
      </ul>
    </div>
  );
}

/** A thin bar 0..1. */
export function Meter({ ratio, tone = "up" }: { ratio: number; tone?: Tone }) {
  return (
    <div className="h-1 overflow-hidden rounded-full bg-muted">
      <div className={cx("h-full rounded-full", toneSwatch[tone])} style={{ width: `${Math.max(0, Math.min(1, ratio)) * 100}%` }} />
    </div>
  );
}

/** Tabs with counts, e.g. All / Down / Maintenance. */
export function Tabs<T extends string>({ label, value, options, onChange, chips }: { label: string; value: T; options: readonly { id: T; label: string; count?: number }[]; onChange: (v: T) => void; chips?: boolean }) {
  return (
    <div role="tablist" aria-label={label} className="flex flex-wrap gap-1.5">
      {options.map((o) => (
        <button
          key={o.id}
          role="tab"
          type="button"
          aria-selected={o.id === value}
          onClick={() => onChange(o.id)}
          className={cx(
            "inline-flex cursor-pointer items-center gap-1.5 px-3 py-1.5 text-xs font-bold transition-colors",
            chips ? "rounded-full border" : "rounded-lg",
            o.id === value
              ? chips
                ? "border-primary bg-primary text-primary-foreground"
                : "bg-primary/10 text-primary"
              : chips
                ? "border-border bg-card text-muted-foreground hover:text-foreground"
                : "text-muted-foreground hover:bg-muted hover:text-foreground",
          )}
        >
          {o.label}
          {o.count !== undefined && <span className={cx("tabular-nums", o.id === value ? "opacity-90" : "opacity-60")}>{o.count}</span>}
        </button>
      ))}
    </div>
  );
}

/** A search box with its icon. */
export function SearchField({ value, onChange, placeholder, label }: { value: string; onChange: (v: string) => void; placeholder?: string; label: string }) {
  return (
    <label className="relative block min-w-0 flex-1">
      <span className="sr-only">{label}</span>
      <Search className="pointer-events-none absolute top-1/2 left-3 h-4 w-4 -translate-y-1/2 text-muted-foreground" aria-hidden />
      <input
        type="search"
        value={value}
        placeholder={placeholder}
        onChange={(e) => onChange(e.target.value)}
        className="h-10 w-full rounded-lg border border-border bg-background pr-3 pl-9 text-sm outline-none placeholder:text-muted-foreground focus:border-primary focus:bg-card focus:ring-3 focus:ring-primary/15"
      />
    </label>
  );
}

/** A native select drawn like the search field. */
export function SelectField<T extends string>({ label, value, options, onChange }: { label: string; value: T; options: readonly { id: T; label: string }[]; onChange: (v: T) => void }) {
  return (
    <select aria-label={label} value={value} onChange={(e) => onChange(e.target.value as T)} className="h-10 min-w-32 rounded-lg border border-border bg-card px-3 text-sm font-semibold text-muted-foreground outline-none focus:border-primary">
      {options.map((o) => (
        <option key={o.id} value={o.id}>
          {o.label}
        </option>
      ))}
    </select>
  );
}

/** The dark gradient hero card. */
export function HeroCard({ kicker, title, caption, foot, aside }: { kicker: string; title: ReactNode; caption?: ReactNode; foot?: ReactNode; aside?: ReactNode }) {
  return (
    <section aria-label="Status" className="relative flex flex-col gap-6 overflow-hidden rounded-2xl bg-linear-to-br from-hero-from to-hero-to p-7 text-hero-foreground shadow-hero sm:flex-row sm:items-center sm:p-9">
      <div className="pointer-events-none absolute -top-20 -right-16 h-90 w-90 rounded-full border border-hero-accent/10 shadow-[0_0_0_44px_rgb(100_215_206/0.02),0_0_0_88px_rgb(100_215_206/0.015)]" />
      <div className="pointer-events-none absolute inset-0 bg-[radial-gradient(ellipse_at_64%_50%,rgb(30_166_150/0.16),transparent_50%)]" />
      <div className="relative min-w-0 flex-1">
        <div className="mb-3 text-[10px] font-extrabold tracking-[0.15em] text-hero-accent uppercase">{kicker}</div>
        <div className="mb-3 text-2xl leading-tight font-extrabold tracking-tight sm:text-3xl">{title}</div>
        {caption && <div className="line-clamp-3 max-w-xl text-xs leading-relaxed text-hero-muted">{caption}</div>}
        {foot && <div className="mt-5 flex flex-wrap items-center gap-x-4 gap-y-2 text-[11px] text-hero-muted">{foot}</div>}
      </div>
      {aside && <div className="relative">{aside}</div>}
    </section>
  );
}

/** A vertical timeline of events, as in the inspiration's phase list. */
export function Timeline({ items }: { items: readonly { tone: Tone; title: ReactNode; meta?: ReactNode; aside?: ReactNode }[] }) {
  return (
    <ol className="relative flex flex-col gap-5 before:absolute before:top-3 before:bottom-3 before:left-3.5 before:w-px before:bg-border">
      {items.map((it, i) => (
        <li key={i} className="relative flex gap-4">
          <span className={cx("relative z-1 grid h-7 w-7 shrink-0 place-items-center rounded-full border-3 border-card outline-1 outline-border", tint[it.tone])}>
            <Icon name={it.tone === "up" ? "up" : it.tone === "down" ? "down" : it.tone === "maint" ? "wrench" : "alert"} className="h-3.5 w-3.5" />
          </span>
          <div className="min-w-0 flex-1 pt-0.5">
            <div className="flex items-center justify-between gap-3">
              <div className="truncate text-sm font-bold">{it.title}</div>
              {it.aside && <div className="shrink-0 text-xs font-bold text-muted-foreground tabular-nums">{it.aside}</div>}
            </div>
            {it.meta && <div className="mt-0.5 text-xs text-muted-foreground">{it.meta}</div>}
          </div>
        </li>
      ))}
    </ol>
  );
}
