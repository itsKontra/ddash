// @ddash/ui: the components every ddash UI is built from. The shell's
// stylesheet carries their Tailwind classes, so Plugins use them without
// generating any CSS for them.
import type { ReactNode } from "react";

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
