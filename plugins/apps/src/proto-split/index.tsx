// PROTOTYPE (wayfinder #139 "What do the Apps page, an App's detail, its Edit
// Sheet and the Add App form look like?"): three variants of the Apps Plugin
// after the split, switchable with ?variant=A|B|C and the floating bar, on
// in-memory Apps (data.ts). ?scenario= picks a failure to see. Throwaway;
// lives on prototype/apps-split only. Nothing here calls a Component.
//
//   A Cards + page    today's cards by group, a Warnings Panel on top, a
//                     detail route per App, Edit and Add App as Sheets
//                     (Add App: Switch + Create Host Override / Link only)
//   B Table + Sheet   Tabs by reach and a Warnings tab over one table, the
//                     detail a tabbed Sheet that flips into edit mode, Add
//                     App a stepped Dialog with three reach tiles
//   C Split view      list left (Needs attention pinned), App right with
//                     sections edited in place, Add App in the pane with a
//                     three-way Reach and a live plan
import { Icon } from "@ddash/ui";
import { useEffect } from "react";
import { Navigate, useParams, useSearchParams } from "react-router";
import { counting, scenarios, store, useStore, type Scenario } from "./data";
import { VariantA, DetailA } from "./variant-a";
import { VariantB } from "./variant-b";
import { VariantC } from "./variant-c";

const variants = { A: "Cards + page", B: "Table + Sheet", C: "Split view" } as const;
type Variant = keyof typeof variants;
const order = Object.keys(variants) as Variant[];

function useVariant() {
  const [params] = useSearchParams();
  const v = params.get("variant") as Variant;
  return order.includes(v) ? v : "A";
}

/** PROTOTYPE: the Apps Page in the variant ?variant= names, plus the floating bar. */
export function ProtoApps() {
  const v = useVariant();
  return (
    <>
      {v === "A" && <VariantA />}
      {v === "B" && <VariantB />}
      {v === "C" && <VariantC />}
      <Bar />
    </>
  );
}

/** PROTOTYPE: /apps/:id, variant A's detail page; B and C open the App on the Page instead. */
export function ProtoDetail() {
  const v = useVariant();
  const { id = "" } = useParams();
  const [params] = useSearchParams();
  if (v !== "A") {
    const next = new URLSearchParams(params);
    if (v === "C") next.set("app", id);
    return <Navigate to={`..?${next}`} replace />;
  }
  return (
    <>
      <DetailA />
      <Bar />
    </>
  );
}

function Bar() {
  const [params, setParams] = useSearchParams();
  const s = useStore();
  const v = useVariant();
  const scenario = (params.get("scenario") as Scenario) in scenarios ? (params.get("scenario") as Scenario) : "normal";
  const set = (k: string, val: string) =>
    setParams(
      (prev) => {
        const next = new URLSearchParams(prev);
        next.set(k, val);
        if (k === "variant") next.delete("app");
        return next;
      },
      { replace: true, preventScrollReset: true },
    );
  const step = (d: number) => set("variant", order[(order.indexOf(v) + d + order.length) % order.length]);
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key !== "ArrowLeft" && e.key !== "ArrowRight") return;
      const t = e.target as HTMLElement | null;
      if (t?.closest("input, textarea, select, [contenteditable], [role=radio], [role=tab], [role=dialog], [role=switch]")) return;
      step(e.key === "ArrowLeft" ? -1 : 1);
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  });
  return (
    <div className="pointer-events-none fixed bottom-4 left-1/2 z-50 flex max-w-[95vw] -translate-x-1/2 flex-col items-center gap-1">
      {s.log.length > 0 && (
        <ol className="pointer-events-auto flex max-w-2xl flex-col gap-0.5 rounded bg-foreground/85 px-3 py-1.5 font-mono text-[11px] text-background">
          {s.log.slice(0, 3).map((l, i) => (
            <li key={i} className={i ? "opacity-60" : ""}>
              {l}
            </li>
          ))}
        </ol>
      )}
      <div className="pointer-events-auto flex items-center gap-1 rounded-full bg-foreground px-2 py-1.5 text-sm text-background shadow-lg">
        <button type="button" aria-label="Previous variant" onClick={() => step(-1)} className="cursor-pointer rounded-full p-1 hover:opacity-70">
          <Icon name="chevron-left" />
        </button>
        <span className="min-w-36 text-center font-semibold">
          {v} ({variants[v]})
        </span>
        <button type="button" aria-label="Next variant" onClick={() => step(1)} className="cursor-pointer rounded-full p-1 hover:opacity-70">
          <Icon name="chevron-right" />
        </button>
        <select aria-label="Scenario" value={scenario} onChange={(e) => set("scenario", e.target.value)} className="ml-2 cursor-pointer rounded-full bg-background/20 px-2 py-0.5 text-xs text-background">
          {Object.entries(scenarios).map(([k, label]) => (
            <option key={k} value={k} className="text-foreground">
              {label}
            </option>
          ))}
        </select>
        <span className="ml-2 text-xs opacity-80" title="Warnings that count: not Ignored, not on a Hidden App">
          badge {counting(s)}
        </span>
        <button type="button" onClick={() => store.reset()} className="ml-2 cursor-pointer rounded-full px-2 text-xs opacity-80 hover:opacity-100">
          reset
        </button>
      </div>
    </div>
  );
}
