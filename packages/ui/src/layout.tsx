// The layout family: what a Page and a Widget are framed by.
import type { ReactNode } from "react";
import { StatusDot } from "./atoms";
import { cx, toneText, type Tone } from "./tone";

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

/** A number with a label under it, e.g. "12" over "running". */
export function Stat({ label, value, tone }: { label: string; value: ReactNode; tone?: Tone }) {
  return (
    <div>
      <div className={cx("text-2xl leading-tight font-semibold tabular-nums", tone && toneText[tone])}>{value}</div>
      <div className="text-xs text-muted-foreground">{label}</div>
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
  return (
    <section className={cx("flex h-full flex-col rounded-ui border border-border bg-card shadow-sm", className)}>
      <header className="flex items-center gap-2 px-4 pt-3 pb-2">
        {status && <StatusDot tone={status.tone} label={`${status.tone}: ${status.summary}`} />}
        <h2 className="truncate text-sm font-semibold">{title}</h2>
        {actions && <div className="ml-auto flex items-center gap-1">{actions}</div>}
      </header>
      <div className="min-h-0 flex-1 px-4 pb-4">{children}</div>
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
