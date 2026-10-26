// The atoms: the smallest pieces, drawn inline.
import { useState, type ButtonHTMLAttributes, type ReactNode } from "react";
import { dashboardIconsCommit } from "./dashboard-icons";
import { Icon, type IconName } from "./icon";
import { cx, toneBg, toneTint, type Tone } from "./tone";

/**
 * A coloured dot for a status, in a halo of its tint, named for screen
 * readers by label or tone. pulse ripples it, unless motion is reduced.
 */
export function StatusDot({ tone, label, pulse }: { tone: Tone; label?: string; pulse?: boolean }) {
  return (
    <span
      role="img"
      aria-label={label ?? tone}
      className={cx("relative inline-flex h-3.5 w-3.5 shrink-0 items-center justify-center rounded-full", toneTint[tone])}
    >
      {pulse && <span className={cx("absolute inset-0 rounded-full opacity-60 motion-safe:animate-ping", toneBg[tone])} />}
      <span className={cx("relative inline-flex h-2 w-2 rounded-full", toneBg[tone])} />
    </span>
  );
}

/** A tinted pill naming a state or a count: "up", "3 new". With dot, it leads with a dot in its colour. */
export function Pill({ tone = "idle", dot, children }: { tone?: Tone; dot?: boolean; children: ReactNode }) {
  return (
    <span
      className={cx(
        "inline-flex max-w-full items-center gap-1.5 truncate rounded-full px-2.5 py-0.5 text-xs font-semibold whitespace-nowrap tabular-nums",
        toneTint[tone],
      )}
    >
      {dot && <span aria-hidden="true" className="h-1.5 w-1.5 shrink-0 rounded-full bg-current" />}
      {children}
    </span>
  );
}

/**
 * A button: default (primary), outline, ghost, or danger for a disruptive
 * Action, which is the down tint, never solid. icon leads the label as decoration.
 */
export function Button({
  variant = "default",
  size = "md",
  icon,
  type = "button",
  children,
  ...rest
}: Omit<ButtonHTMLAttributes<HTMLButtonElement>, "className"> & {
  variant?: "default" | "outline" | "ghost" | "danger";
  size?: "sm" | "md";
  icon?: IconName;
}) {
  return (
    <button
      type={type}
      {...rest}
      className={cx(
        buttonLook(size),
        variant === "default" && "bg-primary text-primary-foreground hover:opacity-90",
        variant === "outline" && "border border-border bg-card hover:bg-muted",
        variant === "ghost" && "hover:bg-muted",
        variant === "danger" && "border border-transparent bg-down-tint text-down-text hover:border-down",
      )}
    >
      {icon && <Icon name={icon} size={size} />}
      {children}
    </button>
  );
}

/** What a Button and a ToggleButton share: the shape, the sizes and the disabled look. */
const buttonLook = (size: "sm" | "md") =>
  cx(
    "inline-flex cursor-pointer items-center justify-center gap-1.5 rounded-ui font-medium transition-colors disabled:pointer-events-none disabled:opacity-40",
    size === "sm" ? "h-7 px-2.5 text-xs" : "h-9 px-3.5 text-sm",
  );

/**
 * A Button for a setting that's on or off, e.g. a Device's "Tunnelled" in
 * its row's strip: pressed (aria-pressed) while it's on, ticked and in the
 * primary tint. Like a Switch it acts at once; the Switch suits a form.
 */
export function ToggleButton({
  pressed,
  onChange,
  disabled,
  size = "md",
  children,
}: {
  pressed: boolean;
  onChange: (pressed: boolean) => void;
  disabled?: boolean;
  size?: "sm" | "md";
  children: ReactNode;
}) {
  return (
    <button
      type="button"
      aria-pressed={pressed}
      disabled={disabled}
      onClick={() => onChange(!pressed)}
      className={cx(
        buttonLook(size),
        "border",
        pressed ? "border-primary bg-primary-tint text-primary" : "border-border bg-card text-muted-foreground hover:bg-muted hover:text-foreground",
      )}
    >
      <Icon name={pressed ? "check" : "x"} size={size} />
      {children}
    </button>
  );
}

const skeletonHeight = { sm: "h-8", md: "h-16", lg: "h-32", xl: "h-64" } as const;

/**
 * A placeholder while something loads: lines of text (one by default), or a
 * block of a height from the fixed scale (sm 2rem, md 4rem, lg 8rem, xl 16rem).
 */
export function Skeleton({ lines, height }: { lines?: number; height?: keyof typeof skeletonHeight }) {
  const bar = "rounded-ui-sm bg-muted motion-safe:animate-pulse";
  const count = Math.max(1, lines ?? 1);
  return (
    <div role="status" aria-label="Loading" aria-busy="true" data-height={height} className="flex w-full flex-col gap-2">
      {height ? (
        <div className={cx(bar, skeletonHeight[height])} />
      ) : (
        Array.from({ length: count }, (_, i) => (
          <div key={i} data-skeleton-line="" className={cx(bar, "h-3", i > 0 && i === count - 1 ? "w-3/5" : "w-full")} />
        ))
      )}
    </div>
  );
}

/**
 * Where an icon of homarr-labs/dashboard-icons is served, by its name: as
 * SVG, or as PNG for the icons upstream has no SVG of. At the commit the
 * core's App icon search knows, so what it finds is what's drawn.
 */
export const dashboardIcon = (name: string, format: "svg" | "png" = "svg") =>
  `https://cdn.jsdelivr.net/gh/homarr-labs/dashboard-icons@${dashboardIconsCommit}/${format}/${name}.${format}`;

/** A dashboard-icons name read off an App's name: "Home Assistant" → "home-assistant". */
export const iconName = (name: string) =>
  name
    .toLowerCase()
    .trim()
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/^-|-$/g, "");

/**
 * An App's icon: the dashboard-icons icon named icon (else matched by the
 * App's name), as SVG else PNG, falling back to the favicon of its url,
 * then to the name's first letter.
 */
export function AppIcon({ name, icon, url, size = 36 }: { name: string; icon?: string; url?: string; size?: number }) {
  const sources: string[] = [];
  const slug = icon || iconName(name);
  if (slug) sources.push(dashboardIcon(slug), dashboardIcon(slug, "png"));
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
        className="inline-flex shrink-0 items-center justify-center rounded-ui-sm bg-primary-tint text-sm font-semibold text-primary"
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
      className="shrink-0 rounded-ui-sm object-contain"
    />
  );
}
