// PROTOTYPE: the proposed first component set of @ddash/ui.
// Plugins build only from these and the Theme tokens in index.css.
import { useState, type ReactNode } from "react";
import { ChevronDown, X } from "lucide-react";

export type Tone = "up" | "down" | "warn" | "maint" | "idle";
export type Health = { tone: Tone; summary: string };

const cx = (...c: (string | false | undefined)[]) => c.filter(Boolean).join(" ");

const toneBg: Record<Tone, string> = {
  up: "bg-up",
  down: "bg-down",
  warn: "bg-warn",
  maint: "bg-maint",
  idle: "bg-muted-foreground",
};
const toneText: Record<Tone, string> = {
  up: "text-up",
  down: "text-down",
  warn: "text-warn",
  maint: "text-maint",
  idle: "text-muted-foreground",
};

export function Button({
  children,
  variant = "default",
  size = "md",
  className,
  ...rest
}: React.ButtonHTMLAttributes<HTMLButtonElement> & {
  variant?: "default" | "outline" | "ghost" | "danger";
  size?: "sm" | "md" | "icon";
}) {
  return (
    <button
      {...rest}
      className={cx(
        "inline-flex items-center justify-center gap-1.5 rounded-ui font-medium transition-colors disabled:opacity-40 disabled:pointer-events-none cursor-pointer",
        size === "sm" && "h-7 px-2.5 text-xs",
        size === "md" && "h-9 px-3.5 text-sm",
        size === "icon" && "h-8 w-8",
        variant === "default" && "bg-primary text-primary-foreground hover:opacity-90",
        variant === "outline" && "border bg-card hover:bg-muted",
        variant === "ghost" && "hover:bg-muted",
        variant === "danger" && "bg-down text-white hover:opacity-90",
        className,
      )}
    >
      {children}
    </button>
  );
}

export function StatusDot({ tone, pulse }: { tone: Tone; pulse?: boolean }) {
  return (
    <span className="relative inline-flex h-2.5 w-2.5 shrink-0">
      {pulse && <span className={cx("absolute inset-0 rounded-full opacity-60 animate-ping", toneBg[tone])} />}
      <span className={cx("relative inline-flex h-2.5 w-2.5 rounded-full", toneBg[tone])} />
    </span>
  );
}

export function Badge({ tone = "idle", children }: { tone?: Tone; children: ReactNode }) {
  return (
    <span
      className={cx(
        "inline-flex items-center gap-1 rounded-full px-2 py-0.5 text-[11px] font-semibold tabular-nums",
        tone === "idle" ? "bg-muted text-muted-foreground" : cx(toneBg[tone], "text-white"),
      )}
    >
      {children}
    </span>
  );
}

export function Stat({ label, value, tone }: { label: string; value: ReactNode; tone?: Tone }) {
  return (
    <div>
      <div className={cx("text-2xl font-semibold tabular-nums leading-tight", tone && toneText[tone])}>{value}</div>
      <div className="text-xs text-muted-foreground">{label}</div>
    </div>
  );
}

// Uptime bars, Kuma style: one bar per bucket.
export function UptimeBars({ buckets }: { buckets: Tone[] }) {
  return (
    <div className="flex h-6 items-end gap-[2px]">
      {buckets.map((t, i) => (
        <div key={i} className={cx("h-full flex-1 rounded-[2px]", toneBg[t], t === "up" && "opacity-80")} />
      ))}
    </div>
  );
}

export type WidgetSize = "S" | "M" | "L";

// The frame every Widget renders inside. The host owns the frame (title, health, edit chrome);
// the Plugin only renders the body.
export function WidgetCard({
  title,
  health,
  children,
  actions,
  className,
}: {
  title: string;
  health?: Health;
  children?: ReactNode;
  actions?: ReactNode;
  className?: string;
}) {
  return (
    <section className={cx("flex flex-col rounded-ui border bg-card shadow-sm", className)}>
      <header className="flex items-center gap-2 px-4 pt-3 pb-2">
        {health && <StatusDot tone={health.tone} />}
        <h3 className="truncate text-sm font-semibold">{title}</h3>
        <div className="ml-auto flex items-center gap-1">{actions}</div>
      </header>
      <div className="min-h-0 flex-1 px-4 pb-4">{children}</div>
    </section>
  );
}

// The "is everything up?" answer at the top of a startpage.
export function Hero({ problems, total }: { problems: { title: string; health: Health }[]; total: number }) {
  const down = problems.filter((p) => p.health.tone === "down").length;
  const tone: Tone = down ? "down" : problems.length ? "warn" : "up";
  return (
    <div className={cx("rounded-ui border p-5 bg-card")}>
      <div className="flex items-center gap-3">
        <StatusDot tone={tone} pulse={tone !== "up"} />
        <div className="text-xl font-semibold">
          {tone === "up" ? "Everything is up" : down ? `${down} down` : `${problems.length} need attention`}
        </div>
        <div className="ml-auto text-sm text-muted-foreground">{total} sources reporting</div>
      </div>
      {problems.length > 0 && (
        <ul className="mt-3 space-y-1.5">
          {problems.map((p) => (
            <li key={p.title} className="flex items-center gap-2 text-sm">
              <StatusDot tone={p.health.tone} />
              <span className="font-medium">{p.title}</span>
              <span className="text-muted-foreground">{p.health.summary}</span>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}

export function ExpandableCard({
  title,
  health,
  open,
  onToggle,
  children,
  aside,
}: {
  title: string;
  health?: Health;
  open: boolean;
  onToggle: () => void;
  children: ReactNode;
  aside?: ReactNode;
}) {
  return (
    <section className="rounded-ui border bg-card">
      <div className="flex items-center gap-3 px-4 py-3">
        <button onClick={onToggle} className="flex min-w-0 flex-1 items-center gap-3 text-left cursor-pointer">
          {health && <StatusDot tone={health.tone} />}
          <span className="w-44 shrink-0 truncate text-sm font-semibold">{title}</span>
          {health && <span className="truncate text-sm text-muted-foreground">{health.summary}</span>}
          <ChevronDown className={cx("ml-auto h-4 w-4 shrink-0 transition-transform", open && "rotate-180")} />
        </button>
        {aside}
      </div>
      {open && <div className="border-t px-4 py-4">{children}</div>}
    </section>
  );
}

export function Sheet({ open, onClose, title, children }: { open: boolean; onClose: () => void; title: string; children: ReactNode }) {
  if (!open) return null;
  return (
    <div className="fixed inset-0 z-40 flex justify-end bg-black/40" onClick={onClose}>
      <div className="h-full w-[420px] max-w-full overflow-y-auto border-l bg-background p-5" onClick={(e) => e.stopPropagation()}>
        <div className="mb-4 flex items-center">
          <h2 className="text-lg font-semibold">{title}</h2>
          <Button variant="ghost" size="icon" className="ml-auto" onClick={onClose}>
            <X className="h-4 w-4" />
          </Button>
        </div>
        {children}
      </div>
    </div>
  );
}

export function Segmented<T extends string>({ value, options, onChange }: { value: T; options: T[]; onChange: (v: T) => void }) {
  return (
    <div className="inline-flex rounded-ui border bg-muted p-0.5">
      {options.map((o) => (
        <button
          key={o}
          onClick={() => onChange(o)}
          className={cx("rounded-ui px-2 py-0.5 text-xs font-medium cursor-pointer", o === value ? "bg-card shadow-sm" : "text-muted-foreground")}
        >
          {o}
        </button>
      ))}
    </div>
  );
}

export function Switch({ checked, onChange }: { checked: boolean; onChange: (v: boolean) => void }) {
  return (
    <button
      onClick={() => onChange(!checked)}
      className={cx("relative h-5 w-9 rounded-full transition-colors cursor-pointer", checked ? "bg-primary" : "bg-muted border")}
    >
      <span className={cx("absolute top-0.5 h-4 w-4 rounded-full bg-white shadow transition-all", checked ? "left-[18px]" : "left-0.5")} />
    </button>
  );
}

export function PageHeader({ title, subtitle, actions }: { title: string; subtitle?: string; actions?: ReactNode }) {
  return (
    <div className="mb-6 flex items-end gap-4">
      <div>
        <h1 className="text-2xl font-semibold tracking-tight">{title}</h1>
        {subtitle && <p className="text-sm text-muted-foreground">{subtitle}</p>}
      </div>
      <div className="ml-auto flex items-center gap-2">{actions}</div>
    </div>
  );
}

export function AppIcon({ slug, name, size = 36 }: { slug: string; name: string; size?: number }) {
  const [failed, setFailed] = useState(false);
  if (failed)
    return (
      <div
        style={{ width: size, height: size }}
        className="flex items-center justify-center rounded-ui bg-muted text-sm font-semibold text-muted-foreground"
      >
        {name[0]}
      </div>
    );
  return (
    <img
      src={`https://cdn.jsdelivr.net/gh/homarr-labs/dashboard-icons/svg/${slug}.svg`}
      onError={() => setFailed(true)}
      width={size}
      height={size}
      alt=""
    />
  );
}

export { cx, toneBg, toneText };
