// The charts family: hand-rolled SVG, coloured by Theme classes.
import { useOnHero } from "./surface";
import { cx, toneFill, type Tone } from "./tone";

const sparkStroke = ["stroke-primary", "stroke-muted-foreground"];

/**
 * A small line chart filling its box's width, e.g. throughput over the
 * last hour. Every series shares one scale from zero to the largest value;
 * the first is drawn strong, the rest muted.
 */
export function Sparkline({ label, series }: { label: string; series: readonly (readonly number[])[] }) {
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
    <svg role="img" aria-label={label} viewBox={`0 0 ${W} ${H}`} preserveAspectRatio="none" className="h-12 w-full overflow-visible">
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

const clamp01 = (ratio: number) => (Number.isFinite(ratio) ? Math.max(0, Math.min(1, ratio)) : 0);

/**
 * A progress ring, e.g. how many Widgets are up: `ratio` (0..1) fills the
 * ring and `value` is written inside it and read out as the meter's text.
 */
export function Ring({ ratio, value, label }: { ratio: number; value: string; label: string }) {
  const r = 42;
  const c = 2 * Math.PI * r;
  const filled = clamp01(ratio);
  // On the hero the ring reuses the nav set (Theme rule: ring = nav-accent).
  const hero = useOnHero();
  return (
    <div
      role="meter"
      aria-label={label}
      aria-valuenow={Math.round(filled * 100)}
      aria-valuemin={0}
      aria-valuemax={100}
      aria-valuetext={value}
      className="relative grid size-32 shrink-0 place-items-center"
    >
      <svg viewBox="0 0 100 100" aria-hidden="true" className="absolute inset-0 -rotate-90">
        <circle cx="50" cy="50" r={r} fill="none" strokeWidth="8" className={hero ? "stroke-nav-strong/15" : "stroke-muted"} />
        <circle
          cx="50"
          cy="50"
          r={r}
          fill="none"
          strokeWidth="8"
          strokeLinecap="round"
          strokeDasharray={`${c * filled} ${c}`}
          data-colour={hero ? "nav-accent" : "primary"}
          className={cx(
            hero ? "stroke-nav-accent" : "stroke-primary",
            "motion-safe:transition-[stroke-dasharray] motion-safe:duration-500",
          )}
        />
      </svg>
      <span className={cx("relative text-3xl leading-none font-extrabold tracking-tight tabular-nums", hero && "text-nav-strong")}>
        {value}
      </span>
    </div>
  );
}

/**
 * One slice of a Donut or bar of Bars. An item with a `tone` is status data
 * and drawn in that tone; one without is a category.
 */
export interface ChartItem {
  label: string;
  value: number;
  tone?: Tone;
}

const chartColours = ["chart-1", "chart-2", "chart-3", "chart-4", "chart-5", "chart-6"] as const;
type Colour = (typeof chartColours)[number] | Tone | "muted";

// Literal class names, so the shell's Tailwind build finds them. idle and
// "Other" are both muted, as in UptimeBars.
const colourStroke: Record<Colour, string> = {
  "chart-1": "stroke-chart-1",
  "chart-2": "stroke-chart-2",
  "chart-3": "stroke-chart-3",
  "chart-4": "stroke-chart-4",
  "chart-5": "stroke-chart-5",
  "chart-6": "stroke-chart-6",
  up: "stroke-up",
  down: "stroke-down",
  warn: "stroke-warn",
  maint: "stroke-maint",
  info: "stroke-info",
  idle: "stroke-muted",
  muted: "stroke-muted",
};
const colourFill: Record<Colour, string> = {
  "chart-1": "fill-chart-1",
  "chart-2": "fill-chart-2",
  "chart-3": "fill-chart-3",
  "chart-4": "fill-chart-4",
  "chart-5": "fill-chart-5",
  "chart-6": "fill-chart-6",
  ...toneFill,
  muted: "fill-muted",
};
const colourBg: Record<Colour, string> = {
  "chart-1": "bg-chart-1",
  "chart-2": "bg-chart-2",
  "chart-3": "bg-chart-3",
  "chart-4": "bg-chart-4",
  "chart-5": "bg-chart-5",
  "chart-6": "bg-chart-6",
  up: "bg-up",
  down: "bg-down",
  warn: "bg-warn",
  maint: "bg-maint",
  info: "bg-info",
  idle: "bg-muted",
  muted: "bg-muted",
};

interface Coloured {
  label: string;
  value: number;
  colour: Colour;
}

/**
 * Colours a chart's items: tones keep their tone, categories take chart-1…6
 * in order, and the 7th category on is summed into one "Other", last.
 */
function colourItems(items: readonly ChartItem[]): Coloured[] {
  const out: Coloured[] = [];
  let categories = 0;
  let other = 0;
  for (const { label, value, tone } of items) {
    if (tone) out.push({ label, value, colour: tone });
    else if (categories < chartColours.length) out.push({ label, value, colour: chartColours[categories++] });
    else {
      categories++;
      other += value;
    }
  }
  if (categories > chartColours.length) out.push({ label: "Other", value: other, colour: "muted" });
  return out;
}

function Legend({ label, items }: { label: string; items: readonly Coloured[] }) {
  return (
    <ul aria-label={`${label} legend`} className="min-w-40 flex-1 text-xs">
      {items.map((item, i) => (
        <li key={i} className="my-2 flex items-center gap-2.5 text-muted-foreground">
          <span aria-hidden="true" data-colour={item.colour} className={cx("size-2.5 shrink-0 rounded-[3px]", colourBg[item.colour])} />
          <span className="flex-1 truncate">{item.label}</span>
          <span className="font-bold text-foreground tabular-nums">{item.value}</span>
        </li>
      ))}
    </ul>
  );
}

/**
 * A donut of parts of a whole, e.g. containers by host, with `total`
 * written in the middle and an HTML legend carrying every value.
 */
export function Donut({ segments, total, label }: { segments: readonly ChartItem[]; total: string; label: string }) {
  const items = colourItems(segments);
  const sum = items.reduce((a, s) => a + Math.max(0, s.value), 0);
  const r = 40;
  const c = 2 * Math.PI * r;
  let at = 0;
  return (
    <div className="flex flex-wrap items-center justify-center gap-8">
      <div role="img" aria-label={label} className="relative grid size-44 shrink-0 place-items-center">
        <svg viewBox="0 0 100 100" className="absolute inset-0 -rotate-90">
          <circle cx="50" cy="50" r={r} fill="none" strokeWidth="11" className="stroke-muted" />
          {sum > 0 &&
            items.map((s, i) => {
              const len = (Math.max(0, s.value) / sum) * c;
              const offset = at;
              at += len;
              return (
                len > 0 && (
                  <circle
                    key={i}
                    data-colour={s.colour}
                    cx="50"
                    cy="50"
                    r={r}
                    fill="none"
                    strokeWidth="11"
                    strokeDasharray={`${len} ${c - len}`}
                    strokeDashoffset={-offset}
                    className={colourStroke[s.colour]}
                  />
                )
              );
            })}
        </svg>
        <span className="relative text-3xl leading-none font-extrabold tracking-tight tabular-nums">{total}</span>
      </div>
      <Legend label={label} items={items} />
    </div>
  );
}

const round2 = (v: number) => Math.round(v * 100) / 100;

/**
 * Horizontal bars with an HTML legend carrying every value: one bar per
 * item against the largest (e.g. top blocked domains), or `stacked` end to
 * end in one bar as shares of their sum (e.g. queries allowed vs blocked).
 */
export function Bars({ items, stacked = false, label }: { items: readonly ChartItem[]; stacked?: boolean; label: string }) {
  const coloured = colourItems(items);
  const values = coloured.map((b) => Math.max(0, b.value));
  const grow = "motion-safe:transition-[width,x] motion-safe:duration-500";
  let chart;
  if (stacked) {
    const sum = values.reduce((a, v) => a + v, 0);
    let at = 0;
    chart = (
      <svg role="img" aria-label={label} viewBox="0 0 100 1" preserveAspectRatio="none" className="block h-3 w-full overflow-hidden rounded-full">
        <rect x={0} y={0} width={100} height={1} className="fill-muted" />
        {sum > 0 &&
          coloured.map((b, i) => {
            const width = round2((values[i] / sum) * 100);
            const x = round2(at);
            at += width;
            return width > 0 && <rect key={i} data-colour={b.colour} x={x} y={0} width={width} height={1} className={cx(colourFill[b.colour], grow)} />;
          })}
      </svg>
    );
  } else {
    const max = Math.max(0, ...values);
    const row = 10;
    chart = (
      <svg
        role="img"
        aria-label={label}
        viewBox={`0 0 100 ${Math.max(coloured.length, 1) * row}`}
        preserveAspectRatio="none"
        style={{ height: `${Math.max(coloured.length, 1) * 0.875}rem` }}
        className="block w-full"
      >
        {coloured.map((b, i) => (
          <g key={i}>
            <rect x={0} y={i * row + 2} width={100} height={6} rx={1} className="fill-muted" />
            <rect
              data-colour={b.colour}
              x={0}
              y={i * row + 2}
              width={max > 0 ? round2((values[i] / max) * 100) : 0}
              height={6}
              rx={1}
              className={cx(colourFill[b.colour], grow)}
            />
          </g>
        ))}
      </svg>
    );
  }
  return (
    <div className="flex flex-col gap-2">
      {chart}
      <Legend label={label} items={coloured} />
    </div>
  );
}

/** A thin bar filled to `ratio` (0..1) in a status tone, e.g. disk use. */
export function Meter({ ratio, tone, label }: { ratio: number; tone: Tone; label: string }) {
  const filled = clamp01(ratio);
  return (
    <div
      role="meter"
      aria-label={label}
      aria-valuenow={Math.round(filled * 100)}
      aria-valuemin={0}
      aria-valuemax={100}
      className="h-1.5 w-full overflow-hidden rounded-full bg-muted"
    >
      <div
        data-colour={tone}
        className={cx("h-full rounded-full motion-safe:transition-[width] motion-safe:duration-500", colourBg[tone])}
        style={{ width: `${round2(filled * 100)}%` }}
      />
    </div>
  );
}

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
    <svg role="img" aria-label={label} viewBox={`0 0 ${Math.max(bars.length, 1)} 1`} preserveAspectRatio="none" className="block h-6 w-full overflow-hidden rounded-ui-sm">
      {bars.map((b, i) => (
        <rect key={i} data-tone={b.tone} x={i + gap / 2} y={0} width={1 - gap} height={1} className={toneFill[b.tone]}>
          {b.title && <title>{b.title}</title>}
        </rect>
      ))}
    </svg>
  );
}
