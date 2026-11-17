// The charts with axes: values over time, and stacked columns. SVG drawn
// at the box's pixel width so text stays crisp, coloured by Theme classes,
// each with an HTML legend and a read-out of the point under the pointer.
// With two series or more a legend entry shows or hides its series.
import { useLayoutEffect, useRef, useState } from "react";
import { chartColours, colourBg, colourFill, colourStroke, type Colour } from "./chart-colours";
import { cx, type Tone } from "./tone";

/** A chart colour: chart-1…6 for a category, or a status tone. */
export type ChartColour = (typeof chartColours)[number] | Tone;

/** The box's width in px, followed as it resizes (fallback before layout and without ResizeObserver). */
function useWidth(fallback = 640) {
  const ref = useRef<HTMLDivElement>(null);
  const [width, setWidth] = useState(fallback);
  useLayoutEffect(() => {
    const el = ref.current;
    if (!el) return;
    if (el.clientWidth > 0) setWidth(el.clientWidth);
    if (typeof ResizeObserver === "undefined") return;
    const ro = new ResizeObserver(([e]) => e.contentRect.width > 0 && setWidth(e.contentRect.width));
    ro.observe(el);
    return () => ro.disconnect();
  }, []);
  return [ref, width] as const;
}

/** About n round ticks covering [lo, hi], e.g. 0, 2, 4, 6. */
function niceTicks(lo: number, hi: number, n = 4): number[] {
  if (!(hi > lo)) hi = lo + 1;
  const raw = (hi - lo) / n;
  const mag = 10 ** Math.floor(Math.log10(raw));
  const step = [1, 2, 2.5, 5, 10].map((m) => m * mag).find((s) => s >= raw) ?? 10 * mag;
  const out: number[] = [];
  for (let v = Math.floor(lo / step) * step; v <= hi + step * 1e-9; v += step) out.push(Math.round(v / step) * step);
  if (out[out.length - 1] < hi - step * 1e-9) out.push(out[out.length - 1] + step);
  return out;
}

const round = (v: number) => Math.round(v * 10) / 10;

/** Which series a chart hides, by label: the chart's own, unless a Page holds them. */
export interface HiddenSeries {
  /** The labels of the series hidden; a label not in the series is kept for when it returns. */
  hidden?: readonly string[];
  /** Holds hidden outside the chart, e.g. for a Page that swaps charts in and out. */
  onHiddenChange?: (hidden: string[]) => void;
}

/**
 * The series hidden among labels, and the toggle of one (none for a single
 * series). The last one shown can't be hidden; a set that would hide every
 * series, such as one kept from other data, hides none.
 */
function useHidden(labels: readonly string[], { hidden, onHiddenChange }: HiddenSeries) {
  const [own, setOwn] = useState<readonly string[]>([]);
  const kept = hidden ?? own;
  const off = new Set(kept.filter((l) => labels.includes(l)));
  if (off.size >= labels.length) off.clear();
  const toggle = (label: string) => {
    if (!off.has(label) && off.size >= labels.length - 1) return;
    const next = new Set(off);
    if (!next.delete(label)) next.add(label);
    (onHiddenChange ?? setOwn)([...kept.filter((l) => !labels.includes(l)), ...labels.filter((l) => next.has(l))]);
  };
  return [off, labels.length > 1 ? toggle : undefined] as const;
}

/** Each series' swatch and label; with onToggle each is a button, pressed while its series shows. */
function Legend({
  label,
  items,
  off,
  onToggle,
}: {
  label: string;
  items: readonly { label: string; colour: Colour }[];
  off: ReadonlySet<string>;
  onToggle?: (label: string) => void;
}) {
  const last = items.length - off.size <= 1;
  return (
    <ul aria-label={`${label} legend`} className="flex flex-wrap gap-x-4 gap-y-1 text-xs text-muted-foreground">
      {items.map((s) => {
        const hidden = off.has(s.label);
        const entry = (
          <>
            <span
              aria-hidden="true"
              data-colour={s.colour}
              className={cx("size-2.5 shrink-0 rounded-[3px]", hidden ? "border border-muted-foreground bg-transparent" : colourBg[s.colour])}
            />
            {s.label}
          </>
        );
        return (
          <li key={s.label} className="flex items-center gap-1.5">
            {onToggle ? (
              <button
                type="button"
                aria-pressed={!hidden}
                aria-disabled={(!hidden && last) || undefined}
                onClick={() => onToggle(s.label)}
                className={cx(
                  "-mx-1 flex items-center gap-1.5 rounded-ui-sm px-1",
                  hidden && "line-through",
                  !hidden && last ? "cursor-default" : "cursor-pointer hover:text-foreground",
                )}
              >
                {entry}
              </button>
            ) : (
              entry
            )}
          </li>
        );
      })}
    </ul>
  );
}

/** The read-out of the point under the pointer: its heading, then each series' swatch, label and value. */
function ReadOut({ x, width, heading, rows }: { x: number; width: number; heading: string; rows: { label: string; colour: Colour; value: string }[] }) {
  const left = x > width / 2;
  return (
    <div
      role="status"
      className="pointer-events-none absolute top-1 z-10 min-w-36 rounded-ui-sm border border-border bg-card px-3 py-2 text-xs shadow-elevation"
      style={left ? { right: width - x + 10 } : { left: x + 10 }}
    >
      <div className="mb-1 font-bold text-foreground">{heading}</div>
      {rows.map((r) => (
        <div key={r.label} className="flex items-center gap-2 text-muted-foreground">
          <span aria-hidden="true" className={cx("size-2 shrink-0 rounded-[2px]", colourBg[r.colour])} />
          <span className="flex-1">{r.label}</span>
          <span className="font-bold text-foreground tabular-nums">{r.value}</span>
        </div>
      ))}
    </div>
  );
}

/** Every so many of items, so that each keeps at least gap px of width. */
function thin<T>(items: T[], width: number, gap: number): T[] {
  const every = Math.max(1, Math.ceil((items.length * gap) / Math.max(width, 1)));
  return items.filter((_, i) => i % every === 0);
}

const pad = { top: 10, right: 10, bottom: 24, left: 48 };

/** The horizontal grid with its labels, and the zero line drawn stronger when the range crosses it. */
function YAxis({ ticks, y, width, format }: { ticks: number[]; y: (v: number) => number; width: number; format: (v: number) => string }) {
  return (
    <g aria-hidden="true">
      {ticks.map((t) => (
        <g key={t}>
          <line x1={pad.left} x2={width - pad.right} y1={round(y(t))} y2={round(y(t))} strokeWidth={1} className={t === 0 ? "stroke-border" : "stroke-border/50"} strokeDasharray={t === 0 ? undefined : "3 4"} />
          <text x={pad.left - 8} y={round(y(t))} dy="0.32em" textAnchor="end" className="fill-muted-foreground text-[10px] tabular-nums">
            {format(t)}
          </text>
        </g>
      ))}
    </g>
  );
}

/** One series of a LineChart. */
export interface LineSeries {
  label: string;
  /** One value per time; null or undefined is a gap. */
  values: readonly (number | null | undefined)[];
  colour: ChartColour;
  /** An area is filled down to zero under its line. Default line. */
  shape?: "line" | "area";
}

const hourFormat = new Intl.DateTimeFormat(undefined, { hour: "2-digit", minute: "2-digit" });
const dayFormat = new Intl.DateTimeFormat(undefined, { weekday: "short", day: "numeric" });
const dayTimeFormat = new Intl.DateTimeFormat(undefined, { weekday: "short", day: "numeric", hour: "2-digit", minute: "2-digit" });

/** The x ticks: every 3 hours of a day or less, else each midnight. */
function timeTicks(from: Date, to: Date): { at: Date; label: string }[] {
  const span = to.getTime() - from.getTime();
  const out: { at: Date; label: string }[] = [];
  if (span <= 36 * 3600_000) {
    const t = new Date(from);
    t.setMinutes(0, 0, 0);
    for (; t <= to; t.setHours(t.getHours() + 1)) if (t >= from && t.getHours() % 3 === 0) out.push({ at: new Date(t), label: hourFormat.format(t) });
  } else {
    const t = new Date(from.getFullYear(), from.getMonth(), from.getDate());
    for (; t <= to; t.setDate(t.getDate() + 1)) if (t >= from && t < to) out.push({ at: new Date(t), label: dayFormat.format(t) });
  }
  return out;
}

/**
 * Values over time, e.g. a day's power: areas and lines on one scale from
 * `from` to `to` (a day not over yet stays empty to its end), with a grid
 * labelled by `format`. A gap in the times (over 2.5 steps) or a missing
 * value breaks a line. Hovering reads out every series shown at that time.
 * A legend entry hides its series, and the scale fits the rest.
 */
export function LineChart({
  label,
  times,
  series,
  from,
  to,
  format,
  valueFormat = format,
  height = 240,
  yMin,
  yMax,
  ...shown
}: HiddenSeries & {
  label: string;
  times: readonly Date[];
  series: readonly LineSeries[];
  from: Date;
  to: Date;
  /** The axis's labels. */
  format: (v: number) => string;
  /** The read-out's values; format when unset. */
  valueFormat?: (v: number) => string;
  height?: number;
  /** Fixes the scale's ends, e.g. 0 and 100 for a percentage. */
  yMin?: number;
  yMax?: number;
}) {
  const [ref, width] = useWidth();
  const [hover, setHover] = useState<number | null>(null);
  const [off, toggle] = useHidden(series.map((s) => s.label), shown);
  const coloured = series.map((s) => ({ ...s, colour: s.colour as Colour }));
  const visible = coloured.filter((s) => !off.has(s.label));
  const t0 = from.getTime();
  const t1 = Math.max(to.getTime(), t0 + 1);
  const x = (t: number) => pad.left + ((t - t0) / (t1 - t0)) * (width - pad.left - pad.right);
  const all = visible.flatMap((s) => s.values.filter((v): v is number => v != null && Number.isFinite(v)));
  const ticks = niceTicks(yMin ?? Math.min(0, ...all), yMax ?? Math.max(0, ...all));
  const lo = ticks[0];
  const hi = ticks[ticks.length - 1];
  const y = (v: number) => pad.top + (1 - (v - lo) / (hi - lo)) * (height - pad.top - pad.bottom);
  const ms = times.map((t) => t.getTime());
  const diffs = ms.slice(1).map((t, i) => t - ms[i]).sort((a, b) => a - b);
  const step = diffs.length ? diffs[Math.floor(diffs.length / 2)] : Infinity;

  // Runs of consecutive points with values, split at gaps.
  const runs = (values: LineSeries["values"]) => {
    const out: [number, number][][] = [];
    let run: [number, number][] = [];
    values.forEach((v, i) => {
      const gap = i > 0 && ms[i] - ms[i - 1] > 2.5 * step;
      if (v == null || !Number.isFinite(v) || gap) {
        if (run.length) out.push(run);
        run = [];
      }
      if (v != null && Number.isFinite(v)) run.push([round(x(ms[i])), round(y(v))]);
    });
    if (run.length) out.push(run);
    return out;
  };
  const zero = round(y(Math.max(lo, Math.min(hi, 0))));

  const onMove = (e: React.PointerEvent<SVGSVGElement>) => {
    if (!ms.length) return;
    const box = e.currentTarget.getBoundingClientRect();
    const t = t0 + ((e.clientX - box.left - pad.left) / (width - pad.left - pad.right)) * (t1 - t0);
    let best = 0;
    for (let i = 1; i < ms.length; i++) if (Math.abs(ms[i] - t) < Math.abs(ms[best] - t)) best = i;
    setHover(Math.abs(ms[best] - t) <= Math.max(step, (t1 - t0) / 50) ? best : null);
  };

  const at = hover === null ? null : times[hover];
  const span = t1 - t0;
  return (
    <div className="flex flex-col gap-3">
      <div ref={ref} className="relative">
        <svg role="img" aria-label={label} width={width} height={height} className="block touch-pan-y overflow-visible" onPointerMove={onMove} onPointerLeave={() => setHover(null)}>
          <YAxis ticks={ticks} y={y} width={width} format={format} />
          <g aria-hidden="true">
            {thin(timeTicks(from, to), width - pad.left - pad.right, 64).map((t) => (
              <text key={t.at.getTime()} x={round(x(t.at.getTime()))} y={height - 6} textAnchor="middle" className="fill-muted-foreground text-[10px] tabular-nums">
                {t.label}
              </text>
            ))}
          </g>
          {visible.map((s) =>
            runs(s.values).map((run, i) => {
              const line = run.map(([px, py]) => `${px},${py}`).join(" ");
              return (
                <g key={`${s.label}-${i}`} data-series={s.label} data-colour={s.colour}>
                  {s.shape === "area" && (
                    <polygon points={`${run[0][0]},${zero} ${line} ${run[run.length - 1][0]},${zero}`} className={cx(colourFill[s.colour], "opacity-20")} />
                  )}
                  <polyline points={line} fill="none" strokeWidth={s.shape === "area" ? 1.5 : 2} strokeLinejoin="round" strokeLinecap="round" className={colourStroke[s.colour]} />
                </g>
              );
            }),
          )}
          {hover !== null && (
            <g aria-hidden="true">
              <line x1={round(x(ms[hover]))} x2={round(x(ms[hover]))} y1={pad.top} y2={height - pad.bottom} className="stroke-muted-foreground/60" strokeWidth={1} />
              {visible.map((s) => {
                const v = s.values[hover];
                return v != null && Number.isFinite(v) && <circle key={s.label} cx={round(x(ms[hover]))} cy={round(y(v))} r={3.5} strokeWidth={2} className={cx(colourStroke[s.colour], "fill-card")} />;
              })}
            </g>
          )}
        </svg>
        {hover !== null && at && (
          <ReadOut
            x={x(ms[hover])}
            width={width}
            heading={(span > 36 * 3600_000 ? dayTimeFormat : hourFormat).format(at)}
            rows={visible.flatMap((s) => {
              const v = s.values[hover];
              return v != null && Number.isFinite(v) ? [{ label: s.label, colour: s.colour, value: valueFormat(v) }] : [];
            })}
          />
        )}
      </div>
      <Legend label={label} items={coloured} off={off} onToggle={toggle} />
    </div>
  );
}

/** One series of a ColumnChart: a value per column, stacked up when positive and down when negative. */
export interface ColumnSeries {
  label: string;
  values: readonly number[];
  colour: ChartColour;
}

/**
 * Stacked columns, e.g. each day's energy by source: positive values stack
 * up from zero in series order, negative ones down. Hovering a column reads
 * out its values; with `onSelect` a column is a button, e.g. to open that
 * day. Labels thin out to fit. A legend entry hides its series: the
 * columns stack and scale the rest.
 */
export function ColumnChart({
  label,
  categories,
  series,
  format,
  valueFormat = format,
  height = 240,
  onSelect,
  selectLabel,
  heading,
  ...shown
}: HiddenSeries & {
  label: string;
  /** Each column's label under the axis, e.g. "14". */
  categories: readonly string[];
  series: readonly ColumnSeries[];
  /** The axis's labels. */
  format: (v: number) => string;
  /** The read-out's values, each as its size (a negative one too); format when unset. */
  valueFormat?: (v: number) => string;
  height?: number;
  onSelect?: (index: number) => void;
  /** What selecting a column does, for screen readers, e.g. "Show 14 November". */
  selectLabel?: (index: number) => string;
  /** The read-out's heading for a column; its label when unset. */
  heading?: (index: number) => string;
}) {
  const [ref, width] = useWidth();
  const [hover, setHover] = useState<number | null>(null);
  const [off, toggle] = useHidden(series.map((s) => s.label), shown);
  const coloured = series.map((s) => ({ ...s, colour: s.colour as Colour }));
  const visible = coloured.filter((s) => !off.has(s.label));
  const n = categories.length;
  const up = categories.map((_, i) => visible.reduce((a, s) => a + Math.max(0, s.values[i] ?? 0), 0));
  const down = categories.map((_, i) => visible.reduce((a, s) => a + Math.min(0, s.values[i] ?? 0), 0));
  const ticks = niceTicks(Math.min(0, ...down), Math.max(0, ...up));
  const lo = ticks[0];
  const hi = ticks[ticks.length - 1];
  const y = (v: number) => pad.top + (1 - (v - lo) / (hi - lo)) * (height - pad.top - pad.bottom);
  const slot = (width - pad.left - pad.right) / Math.max(n, 1);
  const bar = Math.max(2, Math.min(slot * 0.68, 44));
  const every = Math.max(1, Math.ceil(28 / slot));

  return (
    <div className="flex flex-col gap-3">
      <div ref={ref} className="relative">
        <svg role="img" aria-label={label} width={width} height={height} className="block overflow-visible" onPointerLeave={() => setHover(null)}>
          <YAxis ticks={ticks} y={y} width={width} format={format} />
          {categories.map((c, i) => {
            const cx0 = pad.left + slot * i + slot / 2;
            let top = 0;
            let bottom = 0;
            return (
              <g key={i} onPointerEnter={() => setHover(i)} data-column={c}>
                <rect x={round(pad.left + slot * i)} y={pad.top} width={round(slot)} height={height - pad.top - pad.bottom} className={cx("fill-transparent", hover === i && "fill-muted/60")} />
                {visible.map((s) => {
                  const v = s.values[i] ?? 0;
                  if (!v) return null;
                  const from = v > 0 ? top : bottom;
                  const to = from + v;
                  if (v > 0) top = to;
                  else bottom = to;
                  const yA = y(Math.max(from, to));
                  const yB = y(Math.min(from, to));
                  return <rect key={s.label} data-colour={s.colour} x={round(cx0 - bar / 2)} y={round(yA)} width={round(bar)} height={Math.max(0.5, round(yB - yA))} className={colourFill[s.colour]} />;
                })}
                {i % every === 0 && (
                  <text x={round(cx0)} y={height - 6} textAnchor="middle" aria-hidden="true" className="fill-muted-foreground text-[10px] tabular-nums">
                    {c}
                  </text>
                )}
              </g>
            );
          })}
        </svg>
        {onSelect && (
          // Buttons over the columns, so each is reachable by keyboard.
          <div className="absolute inset-y-0 flex" style={{ left: pad.left, right: pad.right, top: pad.top, bottom: pad.bottom }}>
            {categories.map((c, i) => (
              <button
                key={i}
                type="button"
                aria-label={selectLabel?.(i) ?? c}
                onClick={() => onSelect(i)}
                onPointerEnter={() => setHover(i)}
                onFocus={() => setHover(i)}
                onBlur={() => setHover(null)}
                className="h-full flex-1 cursor-pointer rounded-ui-sm focus-visible:outline-2 focus-visible:outline-primary"
              />
            ))}
          </div>
        )}
        {hover !== null && (
          <ReadOut
            x={pad.left + slot * hover + slot / 2}
            width={width}
            heading={heading?.(hover) ?? categories[hover]}
            rows={visible.flatMap((s) => (s.values[hover] ? [{ label: s.label, colour: s.colour, value: valueFormat(Math.abs(s.values[hover])) }] : []))}
          />
        )}
      </div>
      <Legend label={label} items={coloured} off={off} onToggle={toggle} />
    </div>
  );
}
