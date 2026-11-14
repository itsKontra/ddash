import { timestampDate } from "@bufbuild/protobuf/wkt";
import type { Status, Widget, WidgetSize } from "@ddash/plugin";
import type { GetFlowResponse } from "@ddash/proto/energy-api/energy_api_pb";
import { Skeleton, StatRow } from "@ddash/ui";
import { Link } from "react-router";
import { eur, isFresh, kw, kwh, pct, shares, Span, useFlow, usePeriod } from "./energy";
import { FlowDiagram, gridText } from "./flow";

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

/** The four figures that matter now, two by two. */
function Now({ flow }: { flow: GetFlowResponse }) {
  const p = flow.power;
  const g = gridText(p?.grid);
  const figures = [
    { label: "Solar", value: p?.solar === undefined ? "–" : kw(p.solar) },
    { label: "Using", value: p?.consumption === undefined ? "–" : kw(p.consumption) },
    { label: g.detail ?? "Grid", value: g.value ?? "–" },
    { label: "Battery", value: flow.battery?.socPct === undefined ? "–" : `${Math.round(flow.battery.socPct)} %` },
  ];
  return (
    <dl className="grid grid-cols-2 gap-x-4 gap-y-3">
      {figures.map((f) => (
        <div key={f.label} className="min-w-0">
          <dt className="truncate text-xs font-bold text-muted-foreground">{f.label}</dt>
          <dd className="truncate text-lg leading-6 font-extrabold tracking-tight tabular-nums">{f.value}</dd>
        </div>
      ))}
    </dl>
  );
}

/** Today so far: solar, consumption, how much of it the house covered itself, and what that saved. */
function Today() {
  const { data } = usePeriod(Span.DAY, "");
  const t = data?.totals;
  const s = t && shares(t);
  return (
    <StatRow
      items={[
        { label: "Solar today", value: t && kwh(t.solar) },
        { label: "Used", value: t && kwh(t.consumption) },
        { label: "Self-sufficient", value: s && (s.selfSufficiency === undefined ? "–" : pct(s.selfSufficiency)) },
        ...(data?.prices ? [{ label: "Saved", value: t && eur(t.savings) }] : []),
      ]}
    />
  );
}

/** S: the figures now. M: where the power flows. L: the flow and today so far. */
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
  if (size === "S") return <Now flow={data} />;
  return (
    <div className="flex h-full flex-col justify-between gap-4">
      <FlowDiagram flow={data} compact />
      {size === "L" && <Today />}
    </div>
  );
}

/** The Overview Widget: the power flowing now. */
export const energyNow: Widget = {
  id: "energy-now",
  title: "Energy now",
  sizes: ["S", "M", "L"],
  defaultSize: "M",
  Body,
  useStatus,
};
