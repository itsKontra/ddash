// The layout family: what a Page and a Widget are framed by.
import { useId, type ReactNode } from "react";
import { Link } from "react-router";
import { Skeleton, StatusDot } from "./atoms";
import { Icon } from "./icon";
import { Kicker } from "./kicker";
import type { IconName } from "./icon-names";
import { InFlush, OnHero } from "./surface";
import { cx, toneText, type Tone } from "./tone";

/**
 * The top of every Page: an optional back link and kicker over the title,
 * the subtitle under it, and the Page's actions to its right (below it on
 * phones). `back.href` is a router path, absolute or relative to the route.
 */
export function PageTitle({
  kicker,
  title,
  subtitle,
  actions,
  back,
}: {
  kicker?: string;
  title: ReactNode;
  subtitle?: ReactNode;
  actions?: ReactNode;
  back?: { href: string; label: string };
}) {
  return (
    <div className="mb-7 flex flex-col gap-4 sm:flex-row sm:items-end">
      <div className="min-w-0">
        {back && (
          <Link
            to={back.href}
            className="mb-4 inline-flex items-center gap-1.5 rounded-ui-sm text-xs font-bold text-muted-foreground hover:text-foreground"
          >
            <Icon name="chevron-left" size="sm" />
            {back.label}
          </Link>
        )}
        {kicker && (
          <div className="mb-2.5">
            <Kicker rule>{kicker}</Kicker>
          </div>
        )}
        <h1 className="text-3xl leading-tight font-extrabold tracking-tight sm:text-4xl">{title}</h1>
        {subtitle && <p className="mt-2 max-w-3xl text-sm leading-relaxed text-muted-foreground">{subtitle}</p>}
      </div>
      {actions && <div className="flex flex-wrap items-center gap-2 sm:ml-auto">{actions}</div>}
    </div>
  );
}

/**
 * A surface on a Page. With a `title` or `kicker` it is a section named by
 * them, its header carrying the description and actions; without either it
 * is a plain card. `toolbar` (search, selects, tabs) sits under the header
 * and wraps onto its own lines on phones. `flush` drops the body's padding,
 * for a table that runs to the edges; a Notice in it pads itself.
 */
export function Panel({
  kicker,
  title,
  description,
  actions,
  toolbar,
  children,
  flush,
}: {
  kicker?: string;
  title?: ReactNode;
  description?: ReactNode;
  actions?: ReactNode;
  toolbar?: ReactNode;
  children: ReactNode;
  flush?: boolean;
}) {
  const id = useId();
  const named = Boolean(title || kicker);
  const Surface = named ? "section" : "div";
  const head = named || actions;
  return (
    <Surface
      aria-labelledby={named ? id : undefined}
      className="min-w-0 overflow-hidden rounded-ui border border-border bg-card shadow-elevation"
    >
      {head && (
        <header className={cx("flex items-start gap-4 px-5", flush && !toolbar ? "border-b border-border py-4" : "pt-5")}>
          <div className="min-w-0">
            {kicker && (
              <Kicker id={title ? undefined : id} muted>
                {kicker}
              </Kicker>
            )}
            {title && (
              <h2 id={id} className="text-base font-extrabold tracking-tight">
                {title}
              </h2>
            )}
            {description && <p className="mt-1 text-xs text-muted-foreground">{description}</p>}
          </div>
          {actions && <div className="ml-auto flex shrink-0 items-center gap-2">{actions}</div>}
        </header>
      )}
      {toolbar && (
        <div
          className={cx(
            "flex flex-col gap-2 px-5 sm:flex-row sm:flex-wrap sm:items-center",
            head ? "pt-4" : "pt-5",
            flush ? "border-b border-border pb-4" : "",
          )}
        >
          {toolbar}
        </div>
      )}
      <div className={cx(!flush && "p-5")}>
        <InFlush value={Boolean(flush)}>{children}</InFlush>
      </div>
    </Surface>
  );
}

/** A heading between Panels, e.g. "Installed" over a grid of cards, with its kicker and actions. */
export function SectionHeading({ kicker, title, actions }: { kicker?: string; title: ReactNode; actions?: ReactNode }) {
  return (
    <div className="mt-2 mb-3 flex items-end gap-3">
      <div className="min-w-0">
        {kicker && (
          <div className="mb-1">
            <Kicker>{kicker}</Kicker>
          </div>
        )}
        <h2 className="text-lg font-extrabold tracking-tight">{title}</h2>
      </div>
      {actions && <div className="ml-auto flex items-center gap-2">{actions}</div>}
    </div>
  );
}

/**
 * One figure on a Page or in a Widget: its label, a big value and an
 * optional description. With an `icon`, `tone` tints the icon's chip;
 * without one, it colours the value. An undefined `value` is still
 * loading and draws a skeleton.
 */
export function MetricTile({
  label,
  value,
  icon,
  tone = "idle",
  desc,
}: {
  label: string;
  value: ReactNode;
  icon?: IconName;
  tone?: Tone;
  desc?: ReactNode;
}) {
  const id = useId();
  return (
    <div role="group" aria-labelledby={id} className="min-w-0 rounded-ui border border-border bg-card p-5 shadow-elevation">
      <div className="flex min-h-8 items-center justify-between gap-2 text-xs font-bold text-muted-foreground">
        <span id={id} className="truncate">
          {label}
        </span>
        {icon && <Icon name={icon} tone={tone} />}
      </div>
      {value === undefined ? (
        <div className="mt-1">
          <Skeleton height="sm" />
        </div>
      ) : (
        <div
          className={cx(
            "mt-1 truncate text-3xl leading-tight font-extrabold tracking-tight tabular-nums",
            !icon && tone !== "idle" && toneText[tone],
          )}
        >
          {value}
        </div>
      )}
      {desc && <div className="mt-1.5 text-xs text-muted-foreground">{desc}</div>}
    </div>
  );
}

/**
 * The dark gradient card at the top of the Overview, the only way onto the
 * hero surface: a kicker, the title (the status headline), a caption
 * clamped to three lines, a foot of counts and an aside such as a Ring.
 * A Ring in the aside and a StatusList in the caption draw themselves in
 * the hero's colours.
 */
export function HeroCard({
  kicker,
  title,
  caption,
  foot,
  aside,
}: {
  kicker: string;
  title: ReactNode;
  caption?: ReactNode;
  foot?: ReactNode;
  aside?: ReactNode;
}) {
  const id = useId();
  return (
    <section
      aria-labelledby={id}
      className="relative flex flex-col gap-6 overflow-hidden rounded-ui-lg bg-linear-to-br from-hero-from to-hero-to p-7 text-nav-foreground shadow-elevation-raised sm:flex-row sm:items-center sm:p-9"
    >
      {/* Two glows, mid-right and from the corner. */}
      <div
        aria-hidden="true"
        className="pointer-events-none absolute inset-0 bg-[radial-gradient(ellipse_at_64%_50%,color-mix(in_srgb,var(--nav-accent)_14%,transparent),transparent_49%),radial-gradient(circle_at_100%_0%,color-mix(in_srgb,var(--nav-accent)_12%,transparent),transparent_45%)]"
      />
      {/* Raised so the aside's halo, later in the DOM, stays behind the text. */}
      <div className="relative z-1 min-w-0 flex-1">
        <div className="mb-3">
          <OnHero value={true}>
            <Kicker>{kicker}</Kicker>
          </OnHero>
        </div>
        <h2 id={id} className="mb-3 text-2xl leading-tight font-extrabold tracking-tight text-nav-strong sm:text-3xl">
          {title}
        </h2>
        {caption && (
          <div className="line-clamp-3 max-w-xl text-xs leading-relaxed">
            <OnHero value={true}>{caption}</OnHero>
          </div>
        )}
        {foot && <div className="mt-5 flex flex-wrap items-center gap-x-4 gap-y-2 text-[11px]">{foot}</div>}
      </div>
      {aside && (
        // w-fit: as wide as the aside even in the phone's column, so the halo centres on it.
        <div className="relative w-fit">
          {/* The ring with two halos, centred on the aside whatever the card's height. */}
          <div
            aria-hidden="true"
            data-halo=""
            className="pointer-events-none absolute top-1/2 left-1/2 size-90 -translate-1/2 rounded-full border border-nav-accent/10 shadow-[0_0_0_44px_color-mix(in_srgb,var(--nav-accent)_1.8%,transparent),0_0_0_87px_color-mix(in_srgb,var(--nav-accent)_1.4%,transparent)]"
          />
          <OnHero value={true}>{aside}</OnHero>
        </div>
      )}
    </section>
  );
}

/**
 * The frame every Widget renders in, owned by the host: the title (a link
 * to the Plugin's Page, when it has one), the status dot, the summary when
 * the Widget isn't up, and the edit controls. The Plugin draws only the body.
 */
export function WidgetCard({
  title,
  status,
  actions,
  children,
}: {
  title: ReactNode;
  /** The Widget's last report; no dot until it first reports. */
  status?: { tone: Tone; summary: string };
  actions?: ReactNode;
  children?: ReactNode;
}) {
  return (
    <section className="flex h-full flex-col rounded-ui border border-border bg-card shadow-elevation">
      <header className="px-5 pt-4 pb-2">
        <div className="flex items-center gap-2">
          <h2 className="truncate text-sm font-extrabold tracking-tight">{title}</h2>
          {status && <StatusDot tone={status.tone} label={`${status.tone}: ${status.summary}`} />}
          {actions && <div className="ml-auto flex items-center gap-1">{actions}</div>}
        </div>
        {status && status.tone !== "up" && <p className="mt-0.5 truncate text-xs text-muted-foreground">{status.summary}</p>}
      </header>
      <div className="min-h-0 flex-1 px-5 pb-5">{children}</div>
    </section>
  );
}

