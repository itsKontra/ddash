// The atoms: the smallest pieces, drawn inline.
import { useState, type ButtonHTMLAttributes, type ReactNode } from "react";
import { cx, toneBg, type Tone } from "./tone";

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
