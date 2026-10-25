// @ddash/ui: the components every ddash UI is built from. The shell's
// stylesheet carries their Tailwind classes, so Plugins use them without
// generating any CSS for them.
import { useId, useState, type ButtonHTMLAttributes, type ReactNode } from "react";
import { Pill, useVariant } from "./proto";

/** A status: up, down, warn (attention), maint (Maintenance) or idle (unknown). */
export type Tone = "up" | "down" | "warn" | "maint" | "idle";

/** Joins class names, skipping falsy ones. */
export const cx = (...c: (string | false | null | undefined)[]) => c.filter(Boolean).join(" ");

export const toneBg: Record<Tone, string> = {
  up: "bg-up",
  down: "bg-down",
  warn: "bg-warn",
  maint: "bg-maint",
  idle: "bg-muted-foreground",
};

export const toneText: Record<Tone, string> = {
  up: "text-up",
  down: "text-down",
  warn: "text-warn",
  maint: "text-maint",
  idle: "text-muted-foreground",
};

/** A coloured dot for a status, named for screen readers by label or tone. */
export function StatusDot({ tone, label, pulse }: { tone: Tone; label?: string; pulse?: boolean }) {
  return (
    <span role="img" aria-label={label ?? tone} className="relative inline-flex h-2.5 w-2.5 shrink-0">
      {pulse && <span className={cx("absolute inset-0 animate-ping rounded-full opacity-60", toneBg[tone])} />}
      <span className={cx("relative inline-flex h-2.5 w-2.5 rounded-full", toneBg[tone])} />
    </span>
  );
}

/** A small pill, e.g. a count on a Nav Entry or a state in a list. */
export function Badge({ tone = "idle", children }: { tone?: Tone; children: ReactNode }) {
  return (
    <span
      className={cx(
        "inline-flex items-center gap-1 rounded-full px-2 py-0.5 text-xs font-semibold tabular-nums",
        tone === "idle" ? "bg-muted text-muted-foreground" : cx(toneBg[tone], "text-primary-foreground"),
      )}
    >
      {children}
    </span>
  );
}

/** The top of every Page: its title, an optional subtitle and actions. */
export function PageHeader({ title, subtitle, actions }: { title: string; subtitle?: ReactNode; actions?: ReactNode }) {
  return (
    <div className="mb-6 flex items-end gap-4">
      <div>
        <h1 className="text-2xl font-semibold tracking-tight">{title}</h1>
        {subtitle && <p className="text-sm text-muted-foreground">{subtitle}</p>}
      </div>
      {actions && <div className="ml-auto flex items-center gap-2">{actions}</div>}
    </div>
  );
}

/** A button: default (primary), outline, ghost, or danger for a disruptive Action. */
export function Button({
  variant = "default",
  size = "md",
  className,
  type = "button",
  ...rest
}: ButtonHTMLAttributes<HTMLButtonElement> & { variant?: "default" | "outline" | "ghost" | "danger"; size?: "sm" | "md" }) {
  return (
    <button
      type={type}
      {...rest}
      className={cx(
        "inline-flex cursor-pointer items-center justify-center gap-1.5 rounded-ui font-medium transition-colors disabled:pointer-events-none disabled:opacity-40",
        size === "sm" ? "h-7 px-2.5 text-xs" : "h-9 px-3.5 text-sm",
        variant === "default" && "bg-primary text-primary-foreground hover:opacity-90",
        variant === "outline" && "border border-border bg-card hover:bg-muted",
        variant === "ghost" && "hover:bg-muted",
        variant === "danger" && "bg-down text-primary-foreground hover:opacity-90",
        className,
      )}
    />
  );
}

/** What a confirm dialog asks. */
export interface ConfirmRequest {
  /** The question, e.g. "Stop container plex on media?". */
  text: string;
  /** The confirm button's label. Default "Confirm". */
  action?: string;
  /** Offers one checkbox with this label, e.g. "Also delete data". */
  checkbox?: string;
}

/** The Operator's answer to a confirm dialog; null when they cancelled. */
export type ConfirmAnswer = { checked: boolean } | null;

/**
 * The standard confirm dialog every disruptive Action shows. Cancel has the
 * focus, and Escape or a click outside cancels.
 */
export function ConfirmDialog({ request, onAnswer }: { request: ConfirmRequest; onAnswer: (answer: ConfirmAnswer) => void }) {
  const [checked, setChecked] = useState(false);
  const id = useId();
  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-foreground/40 p-4"
      onClick={() => onAnswer(null)}
      onKeyDown={(e) => e.key === "Escape" && onAnswer(null)}
    >
      <div
        role="alertdialog"
        aria-modal="true"
        aria-labelledby={`${id}-text`}
        className="w-full max-w-sm rounded-ui border border-border bg-card p-5 shadow-lg"
        onClick={(e) => e.stopPropagation()}
      >
        <p id={`${id}-text`} className="text-sm">
          {request.text}
        </p>
        {request.checkbox && (
          <label className="mt-4 flex items-center gap-2 text-sm">
            <input type="checkbox" checked={checked} onChange={(e) => setChecked(e.target.checked)} />
            {request.checkbox}
          </label>
        )}
        <div className="mt-5 flex justify-end gap-2">
          <Button variant="outline" autoFocus onClick={() => onAnswer(null)}>
            Cancel
          </Button>
          <Button variant="danger" onClick={() => onAnswer({ checked })}>
            {request.action ?? "Confirm"}
          </Button>
        </div>
      </div>
    </div>
  );
}

/** A number with a label under it, e.g. "12" over "running". */
export function Stat({ label, value, tone }: { label: string; value: ReactNode; tone?: Tone }) {
  return (
    <div>
      <div className={cx("text-2xl leading-tight font-semibold tabular-nums", tone && toneText[tone])}>{value}</div>
      <div className="text-xs text-muted-foreground">{label}</div>
    </div>
  );
}

/** Picks one of a few options, e.g. a Widget's size: a radio group drawn as joined buttons. */
export function Segmented<T extends string>({
  label,
  value,
  options,
  onChange,
}: {
  label: string;
  value: T;
  options: readonly T[];
  onChange: (value: T) => void;
}) {
  return (
    <div role="radiogroup" aria-label={label} className="inline-flex rounded-ui border border-border bg-muted p-0.5">
      {options.map((o) => (
        <button
          key={o}
          type="button"
          role="radio"
          aria-checked={o === value}
          onClick={() => onChange(o)}
          className={cx(
            "cursor-pointer rounded-ui px-2 py-0.5 text-xs font-medium",
            o === value ? "bg-card text-foreground shadow-sm" : "text-muted-foreground",
          )}
        >
          {o}
        </button>
      ))}
    </div>
  );
}

/**
 * The frame every Widget renders in, owned by the host: the title (a link
 * to the Plugin's Page, when it has one), the status dot and the edit
 * controls. The Plugin draws only the body.
 */
export function WidgetCard({
  title,
  status,
  actions,
  className,
  children,
}: {
  title: ReactNode;
  /** The Widget's last report; no dot until it first reports. */
  status?: { tone: Tone; summary: string };
  actions?: ReactNode;
  className?: string;
  children?: ReactNode;
}) {
  // PROTOTYPE: the frame per variant.
  const v = useVariant();
  const dot = status && <StatusDot tone={status.tone} label={`${status.tone}: ${status.summary}`} />;
  if (v === "B") {
    return (
      <section className={cx("flex h-full flex-col rounded-ui border border-border bg-card shadow-panel", className)}>
        <header className="flex items-center gap-2 border-b border-border px-4 py-2.5">
          {dot}
          <h2 className="truncate text-xs font-extrabold tracking-wide uppercase">{title}</h2>
          {actions && <div className="ml-auto flex items-center gap-1">{actions}</div>}
        </header>
        <div className="min-h-0 flex-1 p-4">{children}</div>
      </section>
    );
  }
  if (v === "C") {
    return (
      <section className={cx("flex h-full flex-col rounded-2xl bg-card p-5 ring-1 ring-border transition-shadow hover:shadow-panel", className)}>
        <header className="mb-3 flex items-center gap-2">
          <h2 className="truncate text-base font-extrabold tracking-tight">{title}</h2>
          {status && (
            <span className="ml-1">
              <Pill tone={status.tone} dot>
                {status.tone === "maint" ? "maintenance" : status.tone}
              </Pill>
            </span>
          )}
          {actions && <div className="ml-auto flex items-center gap-1">{actions}</div>}
        </header>
        <div className="min-h-0 flex-1">{children}</div>
      </section>
    );
  }
  return (
    <section className={cx("flex h-full flex-col rounded-ui border border-border bg-card shadow-panel", className)}>
      <header className="flex items-center gap-2 px-5 pt-4 pb-2">
        <h2 className="truncate text-sm font-extrabold tracking-tight">{title}</h2>
        {status && <span className="ml-1">{dot}</span>}
        {actions && <div className="ml-auto flex items-center gap-1">{actions}</div>}
      </header>
      {status && status.tone !== "up" && <p className="-mt-1 truncate px-5 pb-2 text-xs text-muted-foreground">{status.summary}</p>}
      <div className="min-h-0 flex-1 px-5 pb-5">{children}</div>
    </section>
  );
}

/** One line of the Hero: something that isn't up. */
export interface HeroProblem {
  title: string;
  tone: Tone;
  summary: string;
}

/** The answer to "is everything up?" at the top of the Overview. */
export function Hero({ tone, headline, problems }: { tone: Tone; headline: ReactNode; problems: readonly HeroProblem[] }) {
  return (
    <section aria-label="Status" className="rounded-ui border border-border bg-card p-5">
      <div className="flex items-center gap-3">
        <StatusDot tone={tone} pulse={tone !== "up" && tone !== "idle"} />
        <div className="text-xl font-semibold">{headline}</div>
      </div>
      {problems.length > 0 && (
        <ul className="mt-3 flex flex-col gap-1.5">
          {problems.map((p) => (
            <li key={p.title} className="flex items-center gap-2 text-sm">
              <StatusDot tone={p.tone} label={`${p.title} is ${p.tone}`} />
              <span className="font-medium">{p.title}</span>
              <span className="text-muted-foreground">{p.summary}</span>
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}

const sparkStroke = ["stroke-primary", "stroke-muted-foreground"];

/**
 * A small line chart filling its box's width, e.g. throughput over the
 * last hour. Every series shares one scale from zero to the largest value;
 * the first is drawn strong, the rest muted.
 */
export function Sparkline({ label, series, className }: { label: string; series: readonly (readonly number[])[]; className?: string }) {
  const W = 100;
  const H = 24;
  const max = Math.max(0, ...series.flat());
  const round = (v: number) => String(Math.round(v * 100) / 100);
  const points = (values: readonly number[]) =>
    values
      .map((v, i) => {
        const x = values.length > 1 ? (i / (values.length - 1)) * W : W;
        const y = max > 0 ? H - (Math.max(0, v) / max) * H : H;
        return `${round(x)},${round(y)}`;
      })
      .join(" ");
  return (
    <svg role="img" aria-label={label} viewBox={`0 0 ${W} ${H}`} preserveAspectRatio="none" className={cx("h-12 w-full overflow-visible", className)}>
      {series.map(
        (values, i) =>
          values.length > 0 && (
            <polyline
              key={i}
              points={points(values)}
              fill="none"
              vectorEffect="non-scaling-stroke"
              strokeWidth={i === 0 ? 1.5 : 1}
              strokeLinejoin="round"
              className={sparkStroke[Math.min(i, sparkStroke.length - 1)]}
            />
          ),
      )}
    </svg>
  );
}

/** Where an icon of homarr-labs/dashboard-icons is served, by its name. */
export const dashboardIcon = (name: string) => `https://cdn.jsdelivr.net/gh/homarr-labs/dashboard-icons/svg/${name}.svg`;

/** A dashboard-icons name read off an App's name: "Home Assistant" → "home-assistant". */
export const iconName = (name: string) =>
  name
    .toLowerCase()
    .trim()
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/^-|-$/g, "");

/**
 * An App's icon: the dashboard-icons icon named icon (else matched by the
 * App's name), falling back to the favicon of its url, then to the name's
 * first letter.
 */
export function AppIcon({ name, icon, url, size = 36 }: { name: string; icon?: string; url?: string; size?: number }) {
  const sources: string[] = [];
  const slug = icon || iconName(name);
  if (slug) sources.push(dashboardIcon(slug));
  try {
    if (url) sources.push(new URL("/favicon.ico", url).href);
  } catch {
    // not a URL: no favicon to try
  }
  const [failed, setFailed] = useState<readonly string[]>([]);
  const src = sources.find((s) => !failed.includes(s));
  if (!src)
    return (
      <span
        aria-hidden="true"
        style={{ width: size, height: size }}
        className="inline-flex shrink-0 items-center justify-center rounded-ui bg-muted text-sm font-semibold text-muted-foreground"
      >
        {name.charAt(0).toUpperCase()}
      </span>
    );
  return (
    <img
      src={src}
      onError={() => setFailed((f) => [...f, src])}
      width={size}
      height={size}
      alt=""
      loading="lazy"
      referrerPolicy="no-referrer"
      className="shrink-0 object-contain"
    />
  );
}

/** A drawer from the right, e.g. the Overview's Add Widget picker. Escape or a click outside closes it. */
export function Sheet({ title, onClose, children }: { title: string; onClose: () => void; children: ReactNode }) {
  const id = useId();
  return (
    <div
      className="fixed inset-0 z-40 flex justify-end bg-foreground/40"
      onClick={onClose}
      onKeyDown={(e) => e.key === "Escape" && onClose()}
    >
      <div
        role="dialog"
        aria-modal="true"
        aria-labelledby={id}
        className="h-full w-full max-w-md overflow-y-auto border-l border-border bg-background p-5"
        onClick={(e) => e.stopPropagation()}
      >
        <div className="mb-4 flex items-center">
          <h2 id={id} className="text-lg font-semibold">
            {title}
          </h2>
          <Button variant="ghost" size="sm" className="ml-auto" autoFocus onClick={onClose}>
            Close
          </Button>
        </div>
        {children}
      </div>
    </div>
  );
}

const toneFill: Record<Tone, string> = {
  up: "fill-up",
  down: "fill-down",
  warn: "fill-warn",
  maint: "fill-maint",
  idle: "fill-muted",
};

/** One bar of UptimeBars: a bucket's tone, idle for a bucket without beats, and what hovering it says. */
export interface UptimeBar {
  tone: Tone;
  title?: string;
}

/**
 * History, Kuma style: one bar per bucket, oldest first, stretched to the
 * full width. Up to 100 bars are drawn apart; more run together as a strip.
 */
export function UptimeBars({ label, bars }: { label: string; bars: readonly UptimeBar[] }) {
  const gap = bars.length <= 100 ? 0.25 : 0;
  return (
    <svg role="img" aria-label={label} viewBox={`0 0 ${Math.max(bars.length, 1)} 1`} preserveAspectRatio="none" className="block h-6 w-full">
      {bars.map((b, i) => (
        <rect key={i} data-tone={b.tone} x={i + gap / 2} y={0} width={1 - gap} height={1} className={toneFill[b.tone]}>
          {b.title && <title>{b.title}</title>}
        </rect>
      ))}
    </svg>
  );
}

/** An on/off switch that acts at once (no confirm), e.g. a Device's Direct toggle; label names it for screen readers. */
export function Switch({ id, checked, onChange, label, disabled }: { id?: string; checked: boolean; onChange: (checked: boolean) => void; label: string; disabled?: boolean }) {
  return (
    <button
      id={id}
      type="button"
      role="switch"
      aria-checked={checked}
      aria-label={label}
      disabled={disabled}
      onClick={() => onChange(!checked)}
      className={cx(
        "relative inline-flex h-5 w-9 shrink-0 cursor-pointer items-center rounded-full transition-colors disabled:pointer-events-none disabled:opacity-40",
        checked ? "bg-primary" : "bg-muted",
      )}
    >
      <span className={cx("inline-block h-4 w-4 rounded-full bg-card shadow transition-transform", checked ? "translate-x-4.5" : "translate-x-0.5")} />
    </button>
  );
}

/** One entry of a Menu; danger marks a disruptive Action. */
export interface MenuItem {
  label: string;
  onSelect: () => void;
  disabled?: boolean;
  danger?: boolean;
}

/**
 * A row's menu, e.g. a Workload's power Actions: a "⋯" button named by
 * label that opens the items under it. Picking one, Escape or a click
 * outside closes it.
 */
export function Menu({ label, items, disabled }: { label: string; items: readonly MenuItem[]; disabled?: boolean }) {
  const [open, setOpen] = useState(false);
  return (
    <div className="relative inline-block">
      <Button variant="ghost" size="sm" aria-label={label} aria-haspopup="menu" aria-expanded={open} disabled={disabled} onClick={() => setOpen(!open)}>
        ⋯
      </Button>
      {open && (
        <>
          <div className="fixed inset-0 z-30" onClick={() => setOpen(false)} />
          <div
            role="menu"
            aria-label={label}
            className="absolute right-0 z-40 mt-1 flex min-w-32 flex-col rounded-ui border border-border bg-card p-1 shadow-lg"
            onKeyDown={(e) => e.key === "Escape" && setOpen(false)}
          >
            {items.map((item, i) => (
              <button
                key={item.label}
                type="button"
                role="menuitem"
                autoFocus={i === 0}
                disabled={item.disabled}
                onClick={() => {
                  setOpen(false);
                  item.onSelect();
                }}
                className={cx(
                  "cursor-pointer rounded-ui px-2.5 py-1.5 text-left text-sm hover:bg-muted disabled:pointer-events-none disabled:opacity-40",
                  item.danger && "text-down",
                )}
              >
                {item.label}
              </button>
            ))}
          </div>
        </>
      )}
    </div>
  );
}

// PROTOTYPE: the redesign candidates (prototype/ui-redesign only).
export * from "./proto";
