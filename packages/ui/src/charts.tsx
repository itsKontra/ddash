// The charts family: hand-rolled SVG, coloured by Theme classes.
import { cx, toneFill, type Tone } from "./tone";

const sparkStroke = ["stroke-primary", "stroke-muted-foreground"];

/**
 * A small line chart filling its box's width, e.g. throughput over the
 * last hour. Every series shares one scale from zero to the largest value;
 * the first is drawn strong, the rest muted.
 */
export function Sparkline({ label, series, className }: { label: string; series: readonly (readonly number[])[]; className?: string }) {
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
    <svg role="img" aria-label={label} viewBox={`0 0 ${W} ${H}`} preserveAspectRatio="none" className={cx("h-12 w-full overflow-visible", className)}>
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
    <svg role="img" aria-label={label} viewBox={`0 0 ${Math.max(bars.length, 1)} 1`} preserveAspectRatio="none" className="block h-6 w-full">
      {bars.map((b, i) => (
        <rect key={i} data-tone={b.tone} x={i + gap / 2} y={0} width={1 - gap} height={1} className={toneFill[b.tone]}>
          {b.title && <title>{b.title}</title>}
        </rect>
      ))}
    </svg>
  );
}
