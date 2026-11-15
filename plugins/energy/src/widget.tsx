import { timestampDate } from "@bufbuild/protobuf/wkt";
import type { Status, Widget, WidgetSize } from "@ddash/plugin";
import type { GetFlowResponse, GetPeriodResponse } from "@ddash/proto/energy-api/energy_api_pb";
import { cx, Icon, Meter, Skeleton, StatRow, type IconName } from "@ddash/ui";
import { Link } from "react-router";
import { colours, eur, isFresh, kw, kwh, pct, shares, Span, useFlow, usePeriod } from "./energy";
import { gridText } from "./flow";
import { batteryLine, headline, idle } from "./now";

const clock = (d: Date) => d.toLocaleTimeString(undefined, { hour: "2-digit", minute: "2-digit" });

/** Up with solar and the battery while readings come in; warn when they stopped or energy-api can't read them. */
function useStatus(): Status | undefined {
  const { data, error, notSetUp } = useFlow();
  if (notSetUp) return { tone: "idle", summary: "Not set up" };
  if (error) return { tone: "warn", summary: `energy-api unavailable: ${error.rawMessage}` };
  if (!data) return undefined;
  if (!isFresh(data)) return { tone: "warn", summary: data.at ? `No reading since ${clock(timestampDate(data.at))}` : "No reading in the last 5 minutes" };
  return { tone: "up", summary: summary(data) };
}

function summary(flow: GetFlowResponse): string {
  const parts = [];
  if (flow.power?.solar !== undefined) parts.push(`Solar ${kw(flow.power.solar)}`);
  if (flow.battery?.socPct !== undefined) parts.push(`Battery ${Math.round(flow.battery.socPct)} %`);
  return parts.join(" · ") || "Reading";
}

/** What the readings add up to, then the battery: its charge, what it does, and how long that lasts. */
function Lead({ flow }: { flow: GetFlowResponse }) {
  const title = isFresh(flow) ? headline(flow.power) : "No current reading";
  const battery = batteryLine(flow.battery, flow.power?.battery);
  const soc = flow.battery?.socPct;
  return (
    <div>
      {title && <p className="text-xl leading-tight font-extrabold tracking-tight text-balance">{title}</p>}
      {battery && <p className="mt-0.5 text-sm text-muted-foreground tabular-nums">{battery}</p>}
      {soc !== undefined && (
        <div className="mt-3">
          <Meter ratio={soc / 100} tone={soc < 20 ? "warn" : "info"} label="Battery state of charge" />
        </div>
      )}
    </div>
  );
}

// Literal classes, so the shell's Tailwind build finds them.
const text = {
  "chart-1": "text-chart-1",
  "chart-2": "text-chart-2",
  "chart-4": "text-chart-4",
  "chart-5": "text-chart-5",
} as const;

/**
 * The four readings, each small; one at zero is dimmed. A list in a narrow
 * Widget (an S beside three others), two by two in a wider one, else in a row.
 */
function Readings({ flow }: { flow: GetFlowResponse }) {
  const p = flow.power;
  const g = gridText(p?.grid);
  const readings: { label: string; icon: IconName; colour: keyof typeof text; power?: number; value?: string }[] = [
    { label: "Solar", icon: "sun", colour: colours.solar, power: p?.solar },
    { label: "House", icon: "house", colour: colours.house, power: p?.house },
    { label: "Heat pump", icon: "heater", colour: colours.heatPump, power: p?.heatPump },
    { label: g.detail === "Balanced" || !g.detail ? "Grid" : g.detail, icon: "utility-pole", colour: colours.grid, power: p?.grid, value: g.value },
  ];
  return (
    <dl className="grid gap-1.5 @xs:grid-cols-2 @xs:gap-2 @sm:grid-cols-4">
      {readings.map((r) => {
        const zero = r.power === undefined || Math.abs(r.power) < idle;
        return (
          <div key={r.label} data-zero={zero || undefined} className={cx("flex min-w-0 items-center justify-between gap-2 rounded-ui-sm bg-muted px-2.5 py-1 @xs:block @xs:py-2", zero && "opacity-50")}>
            <dt className={cx("flex items-center gap-1 text-xs font-bold whitespace-nowrap", text[r.colour])}>
              <Icon name={r.icon} size="sm" />
              {r.label}
            </dt>
            <dd className="text-base leading-6 font-extrabold tracking-tight whitespace-nowrap tabular-nums">
              {r.value ?? (r.power === undefined ? "–" : kw(r.power))}
            </dd>
          </div>
        );
      })}
    </dl>
  );
}

/** Today's solar power over the whole day, as a small filled curve. */
function SolarCurve({ day, tall }: { day: GetPeriodResponse; tall?: boolean }) {
  if (!day.from || !day.to) return null;
  const from = timestampDate(day.from).getTime();
  const span = timestampDate(day.to).getTime() - from;
  const points = day.samples.flatMap((s) => (s.at && s.power?.solar !== undefined ? [{ x: ((timestampDate(s.at).getTime() - from) / span) * 100, v: Math.max(0, s.power.solar) }] : []));
  if (points.length < 2) return null;
  const W = 100;
  const H = 24;
  const max = Math.max(...points.map((p) => p.v));
  const r = (v: number) => Math.round(v * 100) / 100;
  const line = points.map((p) => `${r(p.x)},${r(max > 0 ? H - (p.v / max) * H : H)}`).join(" L");
  return (
    <svg role="img" aria-label="Solar power today" viewBox={`0 0 ${W} ${H}`} preserveAspectRatio="none" className={cx("w-full overflow-visible", tall ? "h-20" : "h-9")}>
      <path d={`M${r(points[0].x)},${H} L${line} L${r(points[points.length - 1].x)},${H} Z`} className="fill-chart-4 opacity-20" />
      <path d={`M${line}`} fill="none" vectorEffect="non-scaling-stroke" strokeWidth={1.5} strokeLinejoin="round" className="stroke-chart-4" />
      <line x1={0} x2={W} y1={H} y2={H} vectorEffect="non-scaling-stroke" strokeWidth={1} className="stroke-border" />
    </svg>
  );
}

/** Today in one line: the solar, how much the house covered itself, and what that saved. */
function TodayLine({ day }: { day: GetPeriodResponse }) {
  const t = day.totals;
  if (!t) return null;
  const s = shares(t);
  return (
    <p className="flex flex-wrap justify-between gap-x-3 text-xs text-muted-foreground tabular-nums">
      <span>
        Today <strong className="font-bold text-foreground">{kwh(t.solar)}</strong> solar
      </span>
      {s.selfSufficiency !== undefined && (
        <span>
          <strong className="font-bold text-foreground">{pct(s.selfSufficiency)}</strong> self-sufficient
        </span>
      )}
      {day.prices && <span className="font-bold text-up-text">{eur(t.savings)} saved</span>}
    </p>
  );
}

/** Today so far as figures: solar, consumption, how much of it the house covered itself, and what that saved. */
function TodayRow({ day }: { day: GetPeriodResponse }) {
  const t = day.totals;
  if (!t) return null;
  const s = shares(t);
  return (
    <StatRow
      items={[
        { label: "Solar today", value: kwh(t.solar) },
        { label: "Used", value: kwh(t.consumption) },
        { label: "Self-sufficient", value: s.selfSufficiency === undefined ? "–" : pct(s.selfSufficiency) },
        ...(day.prices ? [{ label: "Saved", value: eur(t.savings) }] : []),
      ]}
    />
  );
}

/** Today's curve and totals: one line at M, figures under a taller curve at L. */
function Today({ size }: { size: WidgetSize }) {
  const { data } = usePeriod(Span.DAY, "");
  if (!data) return <Skeleton lines={2} />;
  return (
    <div className="flex flex-col gap-2">
      <SolarCurve day={data} tall={size === "L"} />
      {size === "L" ? <TodayRow day={data} /> : <TodayLine day={data} />}
    </div>
  );
}

/** S: what the power adds up to, the battery and the readings. M adds today; L shows today larger. */
function Body({ size }: { size: WidgetSize }) {
  const { data, error, notSetUp } = useFlow();
  if (notSetUp)
    return (
      <p className="text-sm text-muted-foreground">
        energy-api isn&apos;t set up.{" "}
        <Link to="/plugins" className="font-semibold text-primary hover:underline">
          Set it up in Plugins
        </Link>
      </p>
    );
  // Unavailable: the Widget's status says why.
  if (error && !data) return null;
  if (!data) return <Skeleton lines={3} />;
  return (
    <div className="@container flex h-full flex-col gap-4">
      <Lead flow={data} />
      <Readings flow={data} />
      {size !== "S" && <Today size={size} />}
    </div>
  );
}

/** The Overview Widget: what the power flowing now adds up to. */
export const energyNow: Widget = {
  id: "energy-now",
  title: "Energy now",
  sizes: ["S", "M", "L"],
  defaultSize: "M",
  Body,
  useStatus,
};
