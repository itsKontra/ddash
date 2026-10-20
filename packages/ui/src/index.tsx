// @ddash/ui: the components every ddash UI is built from. The shell's
// stylesheet carries their Tailwind classes, so Plugins use them without
// generating any CSS for them.
import { useId, useState, type ButtonHTMLAttributes, type ReactNode } from "react";

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
