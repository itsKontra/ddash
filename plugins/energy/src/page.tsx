import { timestampDate } from "@bufbuild/protobuf/wkt";
import type { GetFlowResponse, GetPeriodResponse, Totals } from "@ddash/proto/energy-api/energy_api_pb";
import {
  Button,
  ColumnChart,
  EmptyState,
  Facts,
  Icon,
  LineChart,
  Meter,
  MetricTile,
  Notice,
  PageTitle,
  Panel,
  Segmented,
  Skeleton,
  useQueryState,
  type ColumnSeries,
} from "@ddash/ui";
import { useState } from "react";
import { Link, useSearchParams } from "react-router";
import { colours, eur, isFresh, kw, kwAxis, kwh, kwhAxis, pct, shares, shareTone, Span, useFlow, usePeriod } from "./energy";
import { batteryText, FlowDiagram } from "./flow";

const spans = ["Day", "Week", "Month", "Year"] as const;
type SpanName = (typeof spans)[number];
const spanOf: Record<SpanName, Span> = { Day: Span.DAY, Week: Span.WEEK, Month: Span.MONTH, Year: Span.YEAR };

const pad = (n: number) => String(n).padStart(2, "0");
/** A local date as YYYY-MM-DD. */
const ymd = (d: Date) => `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`;
const clock = (d: Date) => d.toLocaleTimeString(undefined, { hour: "2-digit", minute: "2-digit" });

/** The period's name: "Saturday, 14 November 2026", "9 – 15 Nov 2026", "November 2026" or "2026". */
function title(p: GetPeriodResponse): string {
  if (!p.from || !p.to) return "";
  const from = timestampDate(p.from);
  const last = new Date(timestampDate(p.to).getTime() - 1);
  switch (p.span) {
    case Span.DAY:
      return from.toLocaleDateString(undefined, { weekday: "long", day: "numeric", month: "long", year: "numeric" });
    case Span.WEEK:
      return new Intl.DateTimeFormat(undefined, { day: "numeric", month: "short", year: "numeric" }).formatRange(from, last);
    case Span.MONTH:
      return from.toLocaleDateString(undefined, { month: "long", year: "numeric" });
    default:
      return String(from.getFullYear());
  }
}

/** The last answer while the next one loads, so stepping through periods doesn't flash. */
function useKept<T>(value: T | undefined): [T | undefined, boolean] {
  const [kept, setKept] = useState(value);
  // Adjusting state while rendering: React renders again before painting.
  if (value !== undefined && value !== kept) setKept(value);
  return [value ?? kept, value === undefined && kept !== undefined];
}

/** "Set up energy-api", while it has no InfluxDB. */
function SetUp() {
  return (
    <Panel>
      <EmptyState
        icon="sun"
        title="Set up energy-api"
        action={
          <Link to="/plugins" className="text-sm font-semibold text-primary hover:underline">
            Configure it in Plugins
          </Link>
        }
      >
        Give energy-api the InfluxDB address, a read-only token and the bucket Telegraf writes to, to see the solar power, the grid, the battery and the house.
      </EmptyState>
    </Panel>
  );
}

/** The power flowing now, with when it was read, and where it goes beside the diagram. */
function Now() {
  const { data, error } = useFlow();
  const fresh = data && isFresh(data);
  return (
    <Panel title="Now" description={data?.at ? `Read at ${clock(timestampDate(data.at))}` : undefined}>
      {error && !data && <Notice tone="warn" title="energy-api can't read InfluxDB">{error.rawMessage}</Notice>}
      {data && !fresh && (
        <div className="mb-4">
          <Notice tone="warn" title="No current reading">
            Nothing has arrived from the last five minutes: check Telegraf and the inverters.
          </Notice>
        </div>
      )}
      {data ? (
        <div className="flex flex-col gap-8 lg:flex-row lg:items-center">
          <div className="min-w-0 lg:flex-1">
            <FlowDiagram flow={data} />
          </div>
          <div className="lg:w-72 lg:shrink-0">
            <NowFacts flow={data} />
          </div>
        </div>
      ) : (
        !error && <Skeleton lines={6} />
      )}
    </Panel>
  );
}

/** What the house uses now, split as the Victron meters it, and the battery. */
function NowFacts({ flow }: { flow: GetFlowResponse }) {
  const p = flow.power;
  const soc = flow.battery?.socPct;
  const losses = p?.consumption !== undefined && p.loads !== undefined ? Math.max(0, p.consumption - p.loads) : undefined;
  const show = (v: number | undefined) => (v === undefined ? "–" : kw(v));
  return (
    <div className="flex flex-col gap-5 lg:border-l lg:border-border lg:pl-8">
      <div>
        <div className="text-xs font-bold text-muted-foreground">Using</div>
        <div className="text-3xl font-extrabold tracking-tight tabular-nums">{show(p?.consumption)}</div>
      </div>
      <Facts
        items={[
          { label: "House", value: show(p?.house) },
          { label: "Heat pump", value: show(p?.heatPump) },
          { label: "Conversion losses", value: show(losses) },
        ]}
      />
      {soc !== undefined && (
        <div className="flex flex-col gap-2">
          <div className="flex items-baseline justify-between text-xs font-bold text-muted-foreground">
            <span>Battery</span>
            <span className="text-sm text-foreground tabular-nums">{Math.round(soc)} %</span>
          </div>
          <Meter ratio={soc / 100} tone={soc < 20 ? "warn" : "info"} label="Battery state of charge" />
          <div className="text-xs text-muted-foreground tabular-nums">
            {[batteryText(p?.battery), flow.battery?.voltage !== undefined && `${flow.battery.voltage.toFixed(1)} V`].filter(Boolean).join(" · ")}
          </div>
        </div>
      )}
    </div>
  );
}

/** What the grid cost after feed-in, or earned when feed-in paid more. */
const bill = (t: Totals) => (t.cost >= t.revenue ? `Grid bill ${eur(t.cost - t.revenue)} after feed-in` : `Feed-in earned ${eur(t.revenue - t.cost)} over the grid bill`);

/** The period's headline figures. */
function Tiles({ t, priced }: { t: Totals; priced: boolean }) {
  const s = shares(t);
  return (
    <div className="grid gap-4 sm:grid-cols-2 xl:grid-cols-4">
      <MetricTile label="Solar" icon="sun" tone="warn" value={kwh(t.solar)} desc={s.selfConsumption === undefined ? undefined : `${pct(s.selfConsumption)} used at home`} />
      <MetricTile label="Used" icon="house" tone="info" value={kwh(t.consumption)} desc={`${kwh(t.gridImport)} from the grid`} />
      <MetricTile
        label="Self-sufficiency"
        icon="leaf"
        tone={shareTone(s.selfSufficiency)}
        value={s.selfSufficiency === undefined ? "–" : pct(s.selfSufficiency)}
        desc="Of the use covered by solar and battery"
      />
      {priced ? (
        <MetricTile label="Saved" icon="piggy-bank" tone="up" value={eur(t.savings)} desc={bill(t)} />
      ) : (
        <MetricTile label="Exported" icon="utility-pole" value={kwh(t.gridExport)} desc="Set prices in Plugins to see costs" />
      )}
    </div>
  );
}

/** A day's or week's power curve. */
function Curve({ p }: { p: GetPeriodResponse }) {
  const times = p.samples.map((s) => (s.at ? timestampDate(s.at) : new Date(0)));
  const has = (f: (s: (typeof p.samples)[number]) => number | undefined) => p.samples.some((s) => f(s) !== undefined);
  const battery = has((s) => s.power?.battery);
  return (
    <LineChart
      label="Power"
      from={timestampDate(p.from!)}
      to={timestampDate(p.to!)}
      times={times}
      format={kwAxis}
      valueFormat={kw}
      height={280}
      series={[
        { label: "Solar", colour: colours.solar, shape: "area", values: p.samples.map((s) => s.power?.solar) },
        { label: "Used", colour: colours.house, values: p.samples.map((s) => s.power?.consumption) },
        { label: "Grid (+ import, − export)", colour: colours.grid, values: p.samples.map((s) => s.power?.grid) },
        ...(battery ? [{ label: "Battery (+ charging)", colour: colours.battery, values: p.samples.map((s) => s.power?.battery) }] : []),
      ]}
    />
  );
}

/** A month's days or a year's months: what was used, by source, above the line; where the rest of the solar went below it. */
function Columns({ p, onOpen }: { p: GetPeriodResponse; onOpen: (span: SpanName, date: string) => void }) {
  const year = p.span === Span.YEAR;
  const starts = p.buckets.map((b) => (b.from ? timestampDate(b.from) : new Date(0)));
  const t = p.buckets.map((b) => b.totals!);
  const series: ColumnSeries[] = [
    { label: "Solar, used directly", colour: colours.solar, values: t.map((x) => shares(x).solarDirect) },
    { label: "From the battery", colour: colours.battery, values: t.map((x) => x.batteryDischarged) },
    { label: "From the grid", colour: colours.grid, values: t.map((x) => x.gridImport) },
    { label: "Exported", colour: colours.export, values: t.map((x) => -x.gridExport) },
    { label: "Into the battery", colour: colours.battery, values: t.map((x) => -x.batteryCharged) },
  ].filter((s) => s.values.some((v) => Math.abs(v) > 0.005));
  const name = (d: Date) => (year ? d.toLocaleDateString(undefined, { month: "long", year: "numeric" }) : d.toLocaleDateString(undefined, { weekday: "short", day: "numeric", month: "long" }));
  return (
    <ColumnChart
      label={year ? "Energy by month" : "Energy by day"}
      categories={starts.map((d) => (year ? d.toLocaleDateString(undefined, { month: "short" }) : String(d.getDate())))}
      series={series}
      format={kwhAxis}
      valueFormat={kwh}
      height={280}
      heading={(i) => name(starts[i])}
      onSelect={(i) => onOpen(year ? "Month" : "Day", ymd(starts[i]))}
      selectLabel={(i) => `Show ${name(starts[i])}`}
    />
  );
}

/** Where the energy came from and went. */
function EnergyFacts({ t }: { t: Totals }) {
  const s = shares(t);
  const metered = t.house > 0 || t.heatPump > 0;
  return (
    <Facts
      items={[
        { label: "Solar", value: kwh(t.solar) },
        { label: "Used", value: kwh(t.consumption) },
        ...(metered
          ? [
              { label: "… by the house", value: kwh(t.house) },
              { label: "… by the heat pump", value: kwh(t.heatPump) },
              { label: "… lost or unmetered", value: kwh(s.other) },
            ]
          : []),
        { label: "From the grid", value: kwh(t.gridImport) },
        { label: "Into the grid", value: kwh(t.gridExport) },
        ...(t.batteryCharged > 0 || t.batteryDischarged > 0
          ? [
              { label: "Into the battery", value: kwh(t.batteryCharged) },
              { label: "From the battery", value: kwh(t.batteryDischarged) },
            ]
          : []),
        { label: "Self-consumption", value: s.selfConsumption === undefined ? "–" : pct(s.selfConsumption) },
      ]}
    />
  );
}

const price = (v: number) => `${new Intl.NumberFormat(undefined, { maximumFractionDigits: 5 }).format(v)} €/kWh`;

/** What the grid cost and paid, and what the solar saved. */
function MoneyFacts({ p }: { p: GetPeriodResponse }) {
  const t = p.totals!;
  if (!p.prices)
    return (
      <p className="text-sm text-muted-foreground">
        No prices set.{" "}
        <Link to="/plugins" className="font-semibold text-primary hover:underline">
          Set them in Plugins
        </Link>{" "}
        to see what the grid cost and the solar saved.
      </p>
    );
  return (
    <Facts
      items={[
        { label: "Grid cost", value: eur(t.cost) },
        { label: "Feed-in revenue", value: eur(t.revenue) },
        t.cost >= t.revenue
          ? { label: "Grid bill", value: <strong>{eur(t.cost - t.revenue)}</strong> }
          : { label: "Earned net", value: <strong className="text-up-text">{eur(t.revenue - t.cost)}</strong> },
        { label: "Without solar", value: eur(t.consumption * p.prices.buy) },
        { label: "Saved by solar", value: <strong className="text-up-text">{eur(t.savings)}</strong> },
        { label: "Prices", value: `${price(p.prices.buy)} bought, ${price(p.prices.sell)} sold` },
      ]}
    />
  );
}

/** The battery's state of charge over a day or week. */
function BatteryCurve({ p }: { p: GetPeriodResponse }) {
  if (!p.samples.some((s) => s.socPct !== undefined)) return null;
  const last = [...p.samples].reverse().find((s) => s.socPct !== undefined);
  return (
    <Panel title="Battery" description={last ? `${Math.round(last.socPct!)} % · ${batteryText(last.power?.battery) ?? ""}` : undefined}>
      <LineChart
        label="State of charge"
        from={timestampDate(p.from!)}
        to={timestampDate(p.to!)}
        times={p.samples.map((s) => (s.at ? timestampDate(s.at) : new Date(0)))}
        series={[{ label: "State of charge", colour: colours.battery, shape: "area", values: p.samples.map((s) => s.socPct) }]}
        format={(v) => `${v} %`}
        yMin={0}
        yMax={100}
        height={160}
      />
    </Panel>
  );
}

/** One day, week, month or year, stepped through with the arrows. */
function History() {
  const [spanName, setSpan] = useQueryState("span", "Day");
  const [date, setDate] = useQueryState("date", "");
  const name = (spans as readonly string[]).includes(spanName) ? (spanName as SpanName) : "Day";
  const query = usePeriod(spanOf[name], date);
  const [p, loading] = useKept(query.data);
  const [, setParams] = useSearchParams();
  // One update for both (two would lose the first), and a new history
  // entry, so Back returns to the month or year.
  const open = (span: SpanName, d: string) =>
    setParams(
      (prev) => {
        const next = new URLSearchParams(prev);
        next.set("span", span);
        next.set("date", d);
        return next;
      },
      { preventScrollReset: true },
    );
  const atStart = !!p?.firstDay && !!p.previous && p.previous < p.firstDay;
  const curve = p && (p.span === Span.DAY || p.span === Span.WEEK);
  return (
    <>
      <Panel
        title={p ? title(p) : "History"}
        description={p?.firstDay ? `Readings since ${new Date(`${p.firstDay}T00:00`).toLocaleDateString(undefined, { day: "numeric", month: "long", year: "numeric" })}` : undefined}
        actions={
          <div className="flex items-center gap-1">
            <Button variant="ghost" size="sm" aria-label="Previous" disabled={!p || atStart} onClick={() => p && setDate(p.previous)}>
              <Icon name="chevron-left" />
            </Button>
            <Button variant="ghost" size="sm" aria-label="Next" disabled={!p?.next} onClick={() => p && setDate(p.next)}>
              <Icon name="chevron-right" />
            </Button>
            <Button variant="outline" size="sm" disabled={date === ""} onClick={() => setDate("")}>
              Today
            </Button>
          </div>
        }
        toolbar={<Segmented label="Period" value={name} options={spans} onChange={(s) => setSpan(s)} />}
      >
        {query.error && !p && <Notice tone="warn" title="energy-api can't read InfluxDB">{query.error.rawMessage}</Notice>}
        {!p && !query.error && <Skeleton lines={8} />}
        {p?.totals && (
          <div className={loading ? "opacity-60 transition-opacity" : "transition-opacity"} aria-busy={loading || undefined}>
            <Tiles t={p.totals} priced={!!p.prices} />
            <div className="mt-6">{curve ? <Curve p={p} /> : <Columns p={p} onOpen={open} />}</div>
          </div>
        )}
      </Panel>
      {p?.totals && (
        <div className="grid gap-6 lg:grid-cols-2">
          <Panel title="Energy" description="From the inverter's, the grid meter's and the battery's counters.">
            <EnergyFacts t={p.totals} />
          </Panel>
          <Panel title="Money" description="Bought and sold at the prices set in Plugins.">
            <MoneyFacts p={p} />
          </Panel>
        </div>
      )}
      {curve && <BatteryCurve p={p} />}
    </>
  );
}

/** The Energy Page: the flow now, then any period's history. */
export function EnergyPage() {
  const { notSetUp } = useFlow();
  return (
    <div>
      <PageTitle title="Energy" subtitle="The solar power, the grid, the battery and the house, as InfluxDB has them." />
      {notSetUp ? (
        <SetUp />
      ) : (
        <div className="flex flex-col gap-6">
          <Now />
          <History />
        </div>
      )}
    </div>
  );
}
