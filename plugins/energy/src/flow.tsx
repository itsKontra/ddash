import type { GetFlowResponse } from "@ddash/proto/energy-api/energy_api_pb";
import { cx, Icon, type IconName } from "@ddash/ui";
import { useEffect, useLayoutEffect, useRef, useState } from "react";
import { colours, kw } from "./energy";

// The diagram's grid, in its viewBox: the AC bus runs along y 150 from
// where grid and solar join it, past the heat pump, to the Victron, which
// feeds the house and charges the battery.
const W = 720;
const wide = {
  H: 300,
  at: {
    grid: [80, 62],
    solar: [80, 238],
    heatPump: [380, 62],
    victron: [530, 150],
    house: [650, 150],
    battery: [530, 250],
  },
} as const;
// On a phone the same wiring, stretched down so the labels have room.
const tall = {
  H: 440,
  at: {
    grid: [80, 70],
    solar: [80, 350],
    heatPump: [380, 70],
    victron: [530, 210],
    house: [650, 210],
    battery: [530, 350],
  },
} as const;
type Layout = typeof wide | typeof tall;
const busJoin = 205;

type Part = keyof Layout["at"];

// Literal classes, so the shell's Tailwind build finds them.
const stroke = {
  "chart-1": "stroke-chart-1",
  "chart-2": "stroke-chart-2",
  "chart-3": "stroke-chart-3",
  "chart-4": "stroke-chart-4",
  "chart-5": "stroke-chart-5",
  "chart-6": "stroke-chart-6",
  bus: "stroke-muted-foreground",
} as const;
const fill = {
  "chart-1": "fill-chart-1",
  "chart-2": "fill-chart-2",
  "chart-3": "fill-chart-3",
  "chart-4": "fill-chart-4",
  "chart-5": "fill-chart-5",
  "chart-6": "fill-chart-6",
  bus: "fill-muted-foreground",
} as const;
const ring = {
  "chart-1": "border-chart-1 text-chart-1",
  "chart-2": "border-chart-2 text-chart-2",
  "chart-3": "border-chart-3 text-chart-3",
  "chart-4": "border-chart-4 text-chart-4",
  "chart-5": "border-chart-5 text-chart-5",
  "chart-6": "border-chart-6 text-chart-6",
  bus: "border-muted-foreground text-muted-foreground",
} as const;

/** Whether the Operator asked for less motion. */
function useReducedMotion() {
  const query = "(prefers-reduced-motion: reduce)";
  const [reduced, setReduced] = useState(() => typeof matchMedia === "function" && matchMedia(query).matches);
  useEffect(() => {
    if (typeof matchMedia !== "function") return;
    const m = matchMedia(query);
    const on = () => setReduced(m.matches);
    m.addEventListener("change", on);
    return () => m.removeEventListener("change", on);
  }, []);
  return reduced;
}

/** Whether the box is narrower than px, e.g. on a phone, followed as it resizes. */
function useNarrow(px: number) {
  const ref = useRef<HTMLDivElement>(null);
  const [narrow, setNarrow] = useState(false);
  useLayoutEffect(() => {
    const el = ref.current;
    if (!el || typeof ResizeObserver === "undefined") return;
    const ro = new ResizeObserver(([e]) => setNarrow(e.contentRect.width > 0 && e.contentRect.width < px));
    ro.observe(el);
    return () => ro.disconnect();
  }, [px]);
  return [ref, narrow] as const;
}

/** Below this a line counts as idle. */
const idle = 0.01;

/**
 * One line of the diagram: a muted track, and while power flows a line in
 * its colour with dots running in the flow's direction, faster with more
 * power (still, with reduced motion).
 */
function Line({ d, kw: p, colour, still }: { d: string; kw: number | undefined; colour: keyof typeof stroke; still: boolean }) {
  const flowing = p !== undefined && Math.abs(p) >= idle;
  const dur = flowing ? Math.max(1.1, Math.min(6, 3.2 / (Math.abs(p) + 0.25))) : 0;
  const reverse = (p ?? 0) < 0;
  return (
    <g data-line={d} data-flowing={flowing || undefined}>
      <path d={d} fill="none" strokeWidth={4} strokeLinecap="round" strokeLinejoin="round" className="stroke-muted" />
      {flowing && <path d={d} fill="none" strokeWidth={2} strokeLinecap="round" strokeLinejoin="round" className={cx(stroke[colour], "opacity-60")} />}
      {flowing &&
        !still &&
        [0, 1, 2].map((i) => (
          <circle key={i} r={4} className={fill[colour]}>
            <animateMotion
              dur={`${dur}s`}
              begin={`${(-dur * i) / 3}s`}
              repeatCount="indefinite"
              path={d}
              keyPoints={reverse ? "1;0" : "0;1"}
              keyTimes="0;1"
              calcMode="linear"
            />
          </circle>
        ))}
    </g>
  );
}

const pos = (l: Layout, p: Part) => ({ left: `${(l.at[p][0] / W) * 100}%`, top: `${(l.at[p][1] / l.H) * 100}%` });

/** One part: its icon in a ring of its colour, its name and its figures beside it. */
function Node({ layout, part, icon, colour, name, value, detail, compact, side = "below" }: {
  layout: Layout;
  part: Part;
  icon: IconName;
  colour: keyof typeof ring;
  name: string;
  value?: string;
  detail?: string;
  compact?: boolean;
  side?: "below" | "above" | "right";
}) {
  return (
    <div className="absolute -translate-x-1/2 -translate-y-1/2" style={pos(layout, part)} role="group" aria-label={name}>
      <div className={cx("relative grid place-items-center rounded-full border-2 bg-card shadow-elevation", compact ? "size-10" : "size-14", ring[colour])}>
        <Icon name={icon} size={compact ? "md" : "lg"} />
        <div
          className={cx(
            "absolute text-center leading-tight whitespace-nowrap",
            side === "below" && "top-full left-1/2 mt-1.5 -translate-x-1/2",
            side === "above" && "bottom-full left-1/2 mb-1.5 -translate-x-1/2",
            side === "right" && "top-1/2 left-full ml-2.5 -translate-y-1/2 text-left",
          )}
        >
          {!compact && <div className="text-xs font-semibold text-muted-foreground">{name}</div>}
          <div className={cx("font-extrabold tracking-tight text-foreground tabular-nums", compact ? "text-xs" : "text-sm")}>{value ?? "–"}</div>
          {detail && <div className="text-xs text-muted-foreground tabular-nums">{detail}</div>}
        </div>
      </div>
    </div>
  );
}

/** "Import" or "Export" with the grid's power, or "Balanced". */
export function gridText(grid: number | undefined): { value?: string; detail?: string } {
  if (grid === undefined) return {};
  if (Math.abs(grid) < idle) return { value: kw(0), detail: "Balanced" };
  return { value: kw(Math.abs(grid)), detail: grid > 0 ? "Import" : "Export" };
}

/** "Charging", "Discharging" or "Idle", with the battery's power. */
export function batteryText(battery: number | undefined): string | undefined {
  if (battery === undefined) return undefined;
  if (Math.abs(battery) < idle) return "Idle";
  return `${battery > 0 ? "Charging" : "Discharging"} ${kw(Math.abs(battery))}`;
}

/**
 * Where the power flows now, drawn as the house is wired: grid and solar
 * on the AC bus with the heat pump, the Victron between the bus, the house
 * and the battery. compact draws it small, for a Widget.
 */
export function FlowDiagram({ flow, compact: small }: { flow: GetFlowResponse; compact?: boolean }) {
  const still = useReducedMotion();
  const [ref, narrow] = useNarrow(520);
  const compact = small || narrow;
  const layout: Layout = narrow ? tall : wide;
  const { at, H } = layout;
  const p = flow.power;
  const solar = p?.solar;
  const grid = p?.grid;
  const heatPump = p?.heatPump;
  // What reaches the Victron's input: positive into it, negative back out.
  const busIn = solar !== undefined && grid !== undefined ? solar + grid : undefined;
  const victronIn = busIn !== undefined ? busIn - (heatPump ?? 0) : undefined;
  const soc = flow.battery?.socPct;
  const [gx, gy] = at.grid;
  const [sx, sy] = at.solar;
  const [hx, hy] = at.heatPump;
  const [vx, vy] = at.victron;
  const [ox] = at.house;
  const [, by] = at.battery;
  return (
    <div ref={ref} className={cx("relative mx-auto w-full", small ? "max-w-xl" : "max-w-3xl")} style={{ aspectRatio: `${W} / ${H}` }}>
      <svg viewBox={`0 0 ${W} ${H}`} className="absolute inset-0 size-full overflow-visible" aria-hidden="true">
        <Line d={`M${gx},${gy} H${busJoin} V${vy}`} kw={grid} colour={colours.grid} still={still} />
        <Line d={`M${sx},${sy} H${busJoin} V${vy}`} kw={solar} colour={colours.solar} still={still} />
        <Line d={`M${busJoin},${vy} H${hx}`} kw={busIn} colour="bus" still={still} />
        <Line d={`M${hx},${vy} V${hy}`} kw={heatPump} colour={colours.heatPump} still={still} />
        <Line d={`M${hx},${vy} H${vx}`} kw={victronIn} colour="bus" still={still} />
        <Line d={`M${vx},${vy} H${ox}`} kw={p?.house} colour={colours.house} still={still} />
        <Line d={`M${vx},${vy} V${by}`} kw={p?.battery} colour={colours.battery} still={still} />
        <circle cx={busJoin} cy={vy} r={4} className="fill-muted-foreground" />
        <circle cx={hx} cy={vy} r={4} className="fill-muted-foreground" />
      </svg>
      <Node layout={layout} part="grid" icon="utility-pole" colour={colours.grid} name="Grid" {...gridText(grid)} compact={compact} />
      <Node layout={layout} part="solar" icon="sun" colour={colours.solar} name="Solar" value={solar === undefined ? undefined : kw(solar)} compact={compact} />
      <Node layout={layout} part="heatPump" icon="heater" colour={colours.heatPump} name="Heat pump" value={heatPump === undefined ? undefined : kw(heatPump)} compact={compact} side="right" />
      <div className="absolute -translate-x-1/2 -translate-y-1/2" style={pos(layout, "victron")} role="group" aria-label="Victron">
        <div className={cx("grid place-items-center rounded-ui-sm border-2 border-muted-foreground bg-card text-muted-foreground shadow-elevation", compact ? "size-9" : "size-12")}>
          <Icon name="zap" size={compact ? "sm" : "md"} />
        </div>
        {!compact && <div className="absolute bottom-full left-1/2 mb-1.5 -translate-x-1/2 text-xs font-semibold whitespace-nowrap text-muted-foreground">Victron</div>}
      </div>
      <Node layout={layout} part="house" icon="house" colour={colours.house} name="House" value={p?.house === undefined ? undefined : kw(p.house)} compact={compact} />
      <Node
        layout={layout}
        part="battery"
        icon={soc === undefined ? "battery" : soc >= 80 ? "battery-full" : soc >= 35 ? "battery-medium" : "battery-low"}
        colour={colours.battery}
        name="Battery"
        value={soc === undefined ? undefined : `${Math.round(soc)} %`}
        detail={compact ? undefined : batteryText(p?.battery)}
        compact={compact}
        side="right"
      />
    </div>
  );
}
