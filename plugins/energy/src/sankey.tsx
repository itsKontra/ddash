import type { Totals } from "@ddash/proto/energy-api/energy_api_pb";
import { cx } from "@ddash/ui";
import { useLayoutEffect, useRef, useState } from "react";
import { kwh, pct, shares } from "./energy";

/** A place the period's energy came from or went to. */
export type NodeId = "solar" | "batteryOut" | "grid" | "export" | "batteryIn" | "used" | "house" | "heatPump" | "other";

/** Energy that went from one node to another over the period. */
export interface Flow {
  from: NodeId;
  to: NodeId;
  kwh: number;
}

// Each node's name and colour, the same slots as energy.ts's colours;
// literal classes, so the Plugin's Tailwind build finds them.
const nodes: Record<NodeId, { label: string; fill: string; band: string }> = {
  solar: { label: "Solar", fill: "fill-chart-4", band: "fill-chart-4" },
  batteryOut: { label: "Battery", fill: "fill-chart-3", band: "fill-chart-3" },
  grid: { label: "Grid", fill: "fill-chart-1", band: "fill-chart-1" },
  export: { label: "Exported", fill: "fill-chart-6", band: "fill-chart-6" },
  batteryIn: { label: "Into battery", fill: "fill-chart-3", band: "fill-chart-3" },
  used: { label: "Used", fill: "fill-chart-2", band: "fill-chart-2" },
  house: { label: "House", fill: "fill-chart-2", band: "fill-chart-2" },
  heatPump: { label: "Heat pump", fill: "fill-chart-5", band: "fill-chart-5" },
  other: { label: "Lost or unmetered", fill: "fill-muted-foreground/40", band: "fill-muted-foreground" },
};

/** Each node's counter: what its label says, whatever the bands add up to. */
function counter(t: Totals, id: NodeId): number {
  switch (id) {
    case "solar":
      return t.solar;
    case "batteryOut":
      return t.batteryDischarged;
    case "grid":
      return t.gridImport;
    case "export":
      return t.gridExport;
    case "batteryIn":
      return t.batteryCharged;
    case "used":
      return t.consumption;
    case "house":
      return t.house;
    case "heatPump":
      return t.heatPump;
    case "other":
      return shares(t).other;
  }
}

/** Whether the period has the Victron's split of the use into house and heat pump. */
const metered = (t: Totals) => t.house > 0 || t.heatPump > 0;

/**
 * Where the period's energy went, as flows between nodes. The solar fills
 * the export and the battery first and the rest is used directly (as
 * shares' solarDirect); export beyond the solar came from the battery,
 * charge beyond it from the grid. Each source's flows add up to its
 * counter, and the use's to the consumption.
 */
export function flows(t: Totals): Flow[] {
  const solarExport = Math.min(t.solar, t.gridExport);
  const solarCharge = Math.min(t.solar - solarExport, t.batteryCharged);
  const batteryExport = Math.min(t.batteryDischarged, t.gridExport - solarExport);
  const gridCharge = Math.min(t.gridImport, t.batteryCharged - solarCharge);
  const all: Flow[] = [
    { from: "solar", to: "export", kwh: solarExport },
    { from: "solar", to: "batteryIn", kwh: solarCharge },
    { from: "solar", to: "used", kwh: t.solar - solarExport - solarCharge },
    { from: "batteryOut", to: "export", kwh: batteryExport },
    { from: "batteryOut", to: "used", kwh: t.batteryDischarged - batteryExport },
    { from: "grid", to: "batteryIn", kwh: gridCharge },
    { from: "grid", to: "used", kwh: t.gridImport - gridCharge },
    ...(metered(t)
      ? [
          { from: "used", to: "house", kwh: t.house } as const,
          { from: "used", to: "heatPump", kwh: t.heatPump } as const,
          { from: "used", to: "other", kwh: shares(t).other } as const,
        ]
      : []),
  ];
  // Under 0.05 kWh shows as 0.0 kWh: not worth a band.
  return all.filter((f) => f.kwh >= 0.05);
}

/** A node where it is drawn. */
export interface PlacedNode {
  id: NodeId;
  /** Its counter, in kWh. */
  value: number;
  x: number;
  y: number;
  /** Its height: the larger of the flows into and out of it, at the scale. */
  h: number;
}

/** A flow drawn as a band from its source's right edge to its destination's left. */
export interface Band extends Flow {
  /** Its thickness: kWh at the scale. */
  h: number;
  x0: number;
  y0: number;
  x1: number;
  y1: number;
}

export interface Layout {
  nodes: PlacedNode[];
  bands: Band[];
  height: number;
}

export const nodeWidth = 10;
const gap = 14;
/** A label's height: its name over its kWh. */
const labelHeight = 30;
/** How far apart labels in a column are at least. */
const labelStep = 34;

/** The kWh a node carries: the larger of what flows in and out. */
function sizes(fs: readonly Flow[]) {
  const into = new Map<NodeId, number>();
  const out = new Map<NodeId, number>();
  for (const f of fs) {
    out.set(f.from, (out.get(f.from) ?? 0) + f.kwh);
    into.set(f.to, (into.get(f.to) ?? 0) + f.kwh);
  }
  return (id: NodeId) => Math.max(into.get(id) ?? 0, out.get(id) ?? 0);
}

/**
 * Lays columns of nodes out at the x's given, stacked with gaps and centred
 * (a column split from one node level with it, as far as it fits), and
 * the flows between them as bands, leaving and entering each node top to
 * bottom. Nodes without a flow are left out.
 */
export function layout(t: Totals, fs: readonly Flow[], columns: readonly (readonly NodeId[])[], xs: readonly number[], height: number, scale: number): Layout {
  const size = sizes(fs);
  const placed = new Map<NodeId, PlacedNode>();
  columns.forEach((column, ci) => {
    const here = column.filter((id) => size(id) > 0);
    const extent = here.reduce((a, id) => a + size(id) * scale, 0) + gap * (here.length - 1);
    let y = (height - extent) / 2;
    // A column split from one node only (the use into its consumers) sits level with it.
    const parents = new Set(fs.filter((f) => here.includes(f.to)).map((f) => f.from));
    const p = parents.size === 1 ? placed.get([...parents][0]) : undefined;
    if (p) y = Math.min(height - extent, Math.max(0, p.y + p.h / 2 - extent / 2));
    for (const id of here) {
      const h = size(id) * scale;
      placed.set(id, { id, value: counter(t, id), x: xs[ci], y, h });
      y += h + gap;
    }
  });
  const outAt = new Map<NodeId, number>();
  const inAt = new Map<NodeId, number>();
  const bands: Band[] = [];
  for (const f of fs) {
    const a = placed.get(f.from);
    const b = placed.get(f.to);
    if (!a || !b) continue;
    const h = f.kwh * scale;
    const y0 = a.y + (outAt.get(f.from) ?? 0);
    const y1 = b.y + (inAt.get(f.to) ?? 0);
    outAt.set(f.from, (outAt.get(f.from) ?? 0) + h);
    inAt.set(f.to, (inAt.get(f.to) ?? 0) + h);
    bands.push({ ...f, h, x0: a.x + nodeWidth, y0, x1: b.x, y1 });
  }
  return { nodes: [...placed.values()], bands, height };
}

/** The kWh per px that fits every column into the height. */
export function fit(fs: readonly Flow[], columns: readonly (readonly NodeId[])[], height: number): number {
  const size = sizes(fs);
  const scales = columns.map((c) => {
    const here = c.filter((id) => size(id) > 0);
    const sum = here.reduce((a, id) => a + size(id), 0);
    return sum > 0 ? (height - gap * (here.length - 1)) / sum : Infinity;
  });
  const s = Math.min(...scales);
  return Number.isFinite(s) ? s : 0;
}

const sources: NodeId[] = ["solar", "batteryOut", "grid"];
const destinations: NodeId[] = ["export", "batteryIn", "used"];
const consumers: NodeId[] = ["house", "heatPump", "other"];

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

/** A band's path: a ribbon of constant thickness, curving between its ends. */
function ribbon(b: Band) {
  const mx = (b.x0 + b.x1) / 2;
  return `M${b.x0},${b.y0} C${mx},${b.y0} ${mx},${b.y1} ${b.x1},${b.y1} L${b.x1},${b.y1 + b.h} C${mx},${b.y1 + b.h} ${mx},${b.y0 + b.h} ${b.x0},${b.y0 + b.h} Z`;
}

/** What a band says on hover, focus and to a screen reader. */
const bandText = (b: Flow, to = "to") => `${nodes[b.from].label} ${to} ${nodes[b.to].label}`;

/** One Sankey drawing: its bands, nodes and labels, the first column labelled on its left. */
function Diagram({ lay, width, label, labelWidth }: { lay: Layout; width: number; label: string; labelWidth: number }) {
  const [active, setActive] = useState<string>();
  const key = (b: Flow) => `${b.from}-${b.to}`;
  const firstX = Math.min(...lay.nodes.map((n) => n.x));
  const lastX = Math.max(...lay.nodes.map((n) => n.x));
  const middle = (n: PlacedNode) => n.x > firstX && n.x < lastX;
  // Labels beside their node, nudged apart down each column, then pulled back up if that ran them off the bottom.
  const labels: { n: PlacedNode; y: number }[] = [];
  for (const x of new Set(lay.nodes.map((n) => n.x))) {
    const column: { n: PlacedNode; y: number }[] = [];
    for (const n of lay.nodes.filter((m) => m.x === x)) {
      const prev = column.at(-1);
      column.push({ n, y: Math.max(n.y + n.h / 2 - labelHeight / 2, prev ? prev.y + labelStep : -Infinity) });
    }
    const over = (column.at(-1)?.y ?? 0) + labelHeight - lay.height;
    if (over > 0) for (const l of column) l.y -= over;
    labels.push(...column);
  }
  const shown = lay.bands.find((b) => key(b) === active);
  return (
    <div className="relative w-full" style={{ height: lay.height }}>
      <svg width={width} height={lay.height} role="group" aria-label={label} className="absolute inset-0 overflow-visible">
        {lay.bands.map((b) => (
          <path
            key={key(b)}
            d={ribbon(b)}
            role="img"
            aria-label={`${bandText(b)}: ${kwh(b.kwh)}`}
            tabIndex={0}
            data-flow={key(b)}
            className={cx(nodes[b.from === "used" ? b.to : b.from].band, "outline-none transition-opacity", active === key(b) ? "opacity-60" : "opacity-30")}
            onPointerEnter={() => setActive(key(b))}
            onPointerLeave={() => setActive(undefined)}
            onFocus={() => setActive(key(b))}
            onBlur={() => setActive(undefined)}
          />
        ))}
        {lay.nodes.map((n) => (
          <rect key={n.id} x={n.x} y={n.y} width={nodeWidth} height={Math.max(2, n.h)} rx={2} className={nodes[n.id].fill} />
        ))}
      </svg>
      {labels.map(({ n, y }) => {
        const left = n.x === firstX;
        return (
          <div
            key={n.id}
            data-node={n.id}
            className={cx("pointer-events-none absolute flex flex-col justify-center leading-tight", left ? "items-end text-right" : "items-start")}
            style={left ? { top: y, height: labelHeight, right: width - n.x + 8, width: firstX - 8 } : { top: y, height: labelHeight, left: n.x + nodeWidth + 6, width: labelWidth - 6 }}
          >
            <span className={cx("flex max-w-full flex-col", middle(n) && "rounded-ui-sm bg-card/85 px-1 py-0.5")}>
              <span className="truncate text-xs text-muted-foreground">{nodes[n.id].label}</span>
              <span className="text-xs font-bold tabular-nums">{kwh(n.value)}</span>
            </span>
          </div>
        );
      })}
      {shown && (
        <div
          role="status"
          className="pointer-events-none absolute z-10 -translate-x-1/2 -translate-y-1/2 rounded-ui-sm border border-border bg-card px-2 py-1 text-xs whitespace-nowrap shadow-elevation"
          style={{ left: (shown.x0 + shown.x1) / 2, top: (shown.y0 + shown.y1 + shown.h) / 2 }}
        >
          <span className="text-muted-foreground">{bandText(shown, "→")}</span> <b className="tabular-nums">{kwh(shown.kwh)}</b>
        </div>
      )}
    </div>
  );
}

/** Below this width the Sankey splits in two, one above the other: where the energy came from and went, then what used it. */
const narrowBelow = 480;

/**
 * The period's energy as a Sankey, on one scale: solar, battery and grid
 * flow into the export, the battery and the use, and the use into the
 * house, the heat pump and what was lost or unmetered. Each node says its
 * counter as text; each band its kWh on hover and focus.
 */
export function EnergySankey({ t }: { t: Totals }) {
  const [ref, width] = useWidth();
  const fs = flows(t);
  const s = shares(t);
  const split = metered(t);
  const narrow = width < narrowBelow;
  // The sources' labels on the left are short; the right's ("Lost or unmetered") take more.
  const leftWidth = narrow ? 76 : 112;
  const labelWidth = narrow ? 128 : 112;
  const right = width - labelWidth - nodeWidth;
  let drawings: Layout[];
  if (narrow && split) {
    const height = 220;
    const scale = fit(fs, [sources, destinations], height);
    const second = fs.filter((f) => f.from === "used");
    const rows = consumers.filter((id) => second.some((f) => f.to === id)).length;
    const top = layout(t, fs.filter((f) => f.from !== "used"), [sources, destinations], [leftWidth, right], height, scale);
    const usedH = t.consumption * scale + gap * (rows - 1);
    const below = layout(t, second, [["used"], consumers], [leftWidth, right], Math.max(usedH, (rows - 1) * labelStep + labelHeight), scale);
    drawings = [top, below];
  } else {
    const height = 260;
    const columns = split ? [sources, destinations, consumers] : [sources, destinations];
    const xs = split ? [leftWidth, width / 2 - nodeWidth / 2, right] : [leftWidth, right];
    drawings = [layout(t, fs, columns, xs, height, fit(fs, columns, height))];
  }
  const empty = fs.length === 0;
  return (
    <div className="flex flex-col gap-4">
      <div ref={ref} className="flex w-full flex-col gap-6">
        {empty ? (
          <p className="text-sm text-muted-foreground">No energy counted in this period.</p>
        ) : (
          drawings.map((lay, i) => (
            <Diagram key={i} lay={lay} width={width} labelWidth={labelWidth} label={i === 0 ? "Where the energy came from and went" : "What used the energy"} />
          ))
        )}
      </div>
      <div className="flex flex-wrap gap-x-5 gap-y-1 border-t border-border pt-4 text-xs text-muted-foreground">
        <span>
          Self-sufficiency <b className="text-foreground tabular-nums">{s.selfSufficiency === undefined ? "–" : pct(s.selfSufficiency)}</b>
        </span>
        <span>
          Self-consumption <b className="text-foreground tabular-nums">{s.selfConsumption === undefined ? "–" : pct(s.selfConsumption)}</b>
        </span>
      </div>
    </div>
  );
}
