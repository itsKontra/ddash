// The layout family: what a Page and a Widget are framed by.
import { useId, type ButtonHTMLAttributes, type ReactNode } from "react";
import { Link } from "react-router";
import { Skeleton, StatusDot } from "./atoms";
import { EmptyState, Notice } from "./feedback";
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

/** One figure in a StatRow: its label and value; an undefined value is still loading. */
export interface StatRowItem {
  label: string;
  value: ReactNode;
}

/**
 * A few related figures side by side in one card, e.g. a Check's uptime
 * over 24 h, 30 d and 365 d: smaller than MetricTiles, so a Sheet or a
 * phone shows each value in full ("99.99 %", "999 ms"), never truncated.
 * Each figure is a group named by its label.
 */
export function StatRow({ items }: { items: readonly StatRowItem[] }) {
  return (
    <div className="grid auto-cols-fr grid-flow-col divide-x divide-border rounded-ui border border-border bg-card">
      {items.map((s) => (
        <StatCell key={s.label} {...s} />
      ))}
    </div>
  );
}

function StatCell({ label, value }: StatRowItem) {
  const id = useId();
  return (
    <div role="group" aria-labelledby={id} className="px-3 py-2.5">
      <div id={id} className="text-xs font-bold text-muted-foreground">
        {label}
      </div>
      {value === undefined ? (
        <div className="mt-0.5 flex h-6 items-center">
          <Skeleton />
        </div>
      ) : (
        <div className="mt-0.5 text-lg leading-6 font-extrabold tracking-tight whitespace-nowrap tabular-nums">{value}</div>
      )}
    </div>
  );
}

/**
 * A small Widget's figures, one per row, divided: the label (after its
 * icon) left, the value right. Nothing is cut: the label and the value
 * each keep one line in full. Under 16rem (an S Widget in the Overview's
 * grid of four) the icons drop out, under 18rem the Units; a value that
 * still doesn't fit moves under its label, and only a free-form name in a
 * value truncates (truncate). The rows are a size container for both.
 */
export function Rows({ children }: { children: ReactNode }) {
  return <div className="@container flex flex-col divide-y divide-border">{children}</div>;
}

/** One figure in Rows, a group named by its label; an undefined value is still loading. `tone` colours the value, as a MetricTile's without an icon. */
export function Row({ icon, label, value, tone = "idle", truncate }: { icon?: IconName; label: string; value: ReactNode; tone?: Tone; truncate?: boolean }) {
  const id = useId();
  return (
    <div role="group" aria-labelledby={id} className="flex min-h-10 flex-wrap items-center gap-x-2 gap-y-1 py-1.5 text-sm">
      <span className="flex shrink-0 items-center gap-2 whitespace-nowrap text-muted-foreground">
        {icon && (
          <span className="leading-none @max-3xs:hidden">
            <Icon name={icon} size="sm" />
          </span>
        )}
        <span id={id}>{label}</span>
      </span>
      {value === undefined ? (
        <span className="ml-auto w-16">
          <Skeleton />
        </span>
      ) : (
        <span className={cx("ml-auto flex items-center justify-end gap-1.5 font-semibold whitespace-nowrap tabular-nums", tone !== "idle" && toneText[tone], truncate && "min-w-0")}>
          {value}
        </span>
      )}
    </div>
  );
}

/**
 * The part of a value a narrow Widget drops, e.g. " of 61,122" after "4%"
 * or " online" after "12 of 14": hidden while its size container (Rows,
 * or an @container of the Widget's own) is under 18rem, as an S Widget
 * is in the Overview's grid of four.
 */
export function Unit({ children }: { children: ReactNode }) {
  return <span className="@max-2xs:hidden">{children}</span>;
}

/**
 * The dark gradient card at the top of the Overview, the only way onto the
 * hero surface: a kicker, the title (the status headline), a caption
 * clamped to three lines, a foot of counts and an aside such as a Ring.
 * A Ring in the aside and a StatusList in the caption draw themselves in
 * the hero's colours. `corner` sits at the end of the kicker's line, e.g.
 * a HeroButton, without making that line taller.
 */
export function HeroCard({
  kicker,
  title,
  caption,
  foot,
  aside,
  corner,
}: {
  kicker: string;
  title: ReactNode;
  caption?: ReactNode;
  foot?: ReactNode;
  aside?: ReactNode;
  corner?: ReactNode;
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
        <div className="mb-3 flex items-center justify-between gap-3">
          <OnHero value={true}>
            <Kicker>{kicker}</Kicker>
          </OnHero>
          {corner && <div className="-my-1.5 flex shrink-0 items-center gap-2">{corner}</div>}
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

/** A small outlined button in the hero's colours, for a HeroCard's corner: e.g. the Overview's Edit. */
export function HeroButton({ icon, children, ...rest }: Omit<ButtonHTMLAttributes<HTMLButtonElement>, "className" | "type"> & { icon?: IconName }) {
  return (
    <button
      type="button"
      {...rest}
      className="inline-flex h-7 cursor-pointer items-center gap-1.5 rounded-ui-sm border border-nav-strong/15 px-2.5 text-xs font-bold text-nav-strong transition-colors hover:bg-nav-strong/10"
    >
      {icon && <Icon name={icon} size="sm" />}
      {children}
    </button>
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


/**
 * A Component that has no config yet, with the way to give it one: its
 * settings on the Plugins page. title says what to set up ("Set up
 * energy-api"), children what it needs and what the Operator gets for it.
 * compact draws it as a Notice, for a place too small for the Panel.
 */
export function SetUp({ icon, title, children, compact }: { icon?: IconName; title: ReactNode; children: ReactNode; compact?: boolean }) {
  if (compact) {
    return (
      <Notice tone="info" title={title}>
        {children}{" "}
        <Link to="/plugins" className="font-semibold text-primary hover:underline">
          Configure it in Plugins
        </Link>
        .
      </Notice>
    );
  }
  return (
    <Panel>
      <EmptyState
        icon={icon}
        title={title}
        action={
          <Link to="/plugins" className="text-sm font-semibold text-primary hover:underline">
            Configure it in Plugins
          </Link>
        }
      >
        {children}
      </EmptyState>
    </Panel>
  );
}
