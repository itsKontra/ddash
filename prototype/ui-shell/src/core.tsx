// PROTOTYPE: core-owned Pages and shared bits every variant mounts.
import { useEffect, useState } from "react";
import { ChevronLeft, ChevronRight, Download, Check } from "lucide-react";
import {
  Badge,
  Button,
  ExpandableCard,
  Hero,
  PageHeader,
  Segmented,
  Sheet,
  Stat,
  StatusDot,
  Switch,
  UptimeBars,
  WidgetCard,
} from "./ddash-ui";
import { navEntries, notInstalledNav, pluginName, plugins, widgets } from "./registry";

// ---- Theme state (core-defined Themes, operator picks one) ----
export type ThemeName = "slate" | "nord" | "terminal";
export function useTheme() {
  const [theme, setTheme] = useState<ThemeName>("slate");
  const [dark, setDark] = useState(window.matchMedia("(prefers-color-scheme: dark)").matches);
  useEffect(() => {
    document.documentElement.dataset.theme = theme;
    document.documentElement.classList.toggle("dark", dark);
  }, [theme, dark]);
  return { theme, setTheme, dark, setDark };
}
export type ThemeCtl = ReturnType<typeof useTheme>;

// ---- Pages ----
export function PluginPage({ id }: { id: string }) {
  const nav = [...navEntries, notInstalledNav].find((n) => n.id === id);
  const own = widgets.filter((w) => w.plugin === nav?.plugin);
  return (
    <div>
      <PageHeader title={nav?.label ?? id} subtitle={`Page provided by the ${pluginName(nav?.plugin ?? id)} Plugin`} />
      <div className="rounded-ui border border-dashed p-8 text-center text-sm text-muted-foreground">
        The Plugin's own Page renders here, loaded at runtime via the import map. Out of scope for this prototype.
        <br />
        Its Widgets, for reference:
      </div>
      <div className="mt-4 grid grid-cols-2 gap-4">
        {own.map((w) => (
          <WidgetCard key={w.id} title={w.title} health={w.health}>
            <w.Body size={w.sizes[w.sizes.length - 1]} />
          </WidgetCard>
        ))}
      </div>
    </div>
  );
}

export function CataloguePage() {
  return (
    <div>
      <PageHeader title="Plugins" subtitle="First-party Plugins from this Release. Installing pulls in the Components they require." />
      <div className="divide-y rounded-ui border bg-card">
        {plugins.map((p) => (
          <div key={p.id} className="flex items-center gap-4 px-4 py-3">
            <div>
              <div className="font-medium">{p.name}</div>
              <div className="text-sm text-muted-foreground">{p.description}</div>
            </div>
            <div className="ml-auto">
              {p.installed ? (
                <Badge tone="up">
                  <Check className="h-3 w-3" /> installed
                </Badge>
              ) : (
                <Button size="sm">
                  <Download className="h-3.5 w-3.5" /> Install
                </Button>
              )}
            </div>
          </div>
        ))}
      </div>
    </div>
  );
}

export function SettingsPage({ t }: { t: ThemeCtl }) {
  return (
    <div>
      <PageHeader title="Settings" subtitle="Core settings. Component config forms would live here too." />
      <WidgetCard title="Theme">
        <div className="space-y-4">
          <div className="grid grid-cols-3 gap-3">
            {(["slate", "nord", "terminal"] as const).map((n) => (
              <button
                key={n}
                data-theme={n}
                onClick={() => t.setTheme(n)}
                className={`${t.dark ? "dark" : ""} cursor-pointer rounded-ui border-2 p-3 text-left ${t.theme === n ? "border-primary" : ""}`}
                style={{ background: "var(--background)", color: "var(--foreground)", fontFamily: "var(--font)" }}
              >
                <div className="mb-2 font-semibold capitalize">{n}</div>
                <div className="flex gap-1">
                  {["--primary", "--up", "--warn", "--down", "--maint"].map((v) => (
                    <span key={v} className="h-4 w-4 rounded-full" style={{ background: `var(${v})` }} />
                  ))}
                </div>
              </button>
            ))}
          </div>
          <div className="flex items-center gap-3 text-sm">
            <Switch checked={t.dark} onChange={t.setDark} /> Dark
          </div>
        </div>
      </WidgetCard>
    </div>
  );
}

export function UiKitPage() {
  const [open, setOpen] = useState(true);
  const [seg, setSeg] = useState<"S" | "M" | "L">("M");
  const [sw, setSw] = useState(true);
  const tokens = ["background", "foreground", "card", "muted", "muted-foreground", "border", "primary", "primary-foreground", "up", "down", "warn", "maint"];
  return (
    <div className="space-y-6">
      <PageHeader title="@ddash/ui" subtitle="Proposed first component set and Theme tokens. Plugins build only from these." />
      <WidgetCard title="Tokens">
        <div className="grid grid-cols-6 gap-3">
          {tokens.map((t) => (
            <div key={t} className="text-xs">
              <div className="mb-1 h-10 rounded-ui border" style={{ background: `var(--${t})` }} />
              --{t}
            </div>
          ))}
        </div>
        <div className="mt-3 text-xs text-muted-foreground">Plus --radius and --font, set per Theme.</div>
      </WidgetCard>
      <div className="grid grid-cols-2 gap-4">
        <WidgetCard title="Button">
          <div className="flex flex-wrap gap-2">
            <Button>Default</Button>
            <Button variant="outline">Outline</Button>
            <Button variant="ghost">Ghost</Button>
            <Button variant="danger">Stop guest</Button>
            <Button size="sm">Small</Button>
          </div>
        </WidgetCard>
        <WidgetCard title="StatusDot · Badge · Switch · Segmented">
          <div className="flex flex-wrap items-center gap-3">
            {(["up", "down", "warn", "maint", "idle"] as const).map((t) => (
              <StatusDot key={t} tone={t} />
            ))}
            <Badge tone="down">1 down</Badge>
            <Badge tone="warn">2</Badge>
            <Badge>idle</Badge>
            <Switch checked={sw} onChange={setSw} />
            <Segmented value={seg} options={["S", "M", "L"]} onChange={setSeg} />
          </div>
        </WidgetCard>
        <WidgetCard title="Stat · UptimeBars">
          <div className="flex gap-6">
            <Stat label="up" value="22" tone="up" />
            <Stat label="queries" value="48.2k" />
          </div>
          <div className="mt-3">
            <UptimeBars buckets={Array.from({ length: 40 }, (_, i) => (i === 30 ? "down" : i === 12 ? "maint" : "up"))} />
          </div>
        </WidgetCard>
        <WidgetCard title="WidgetCard" health={{ tone: "warn", summary: "" }} actions={<Badge>actions slot</Badge>}>
          <div className="text-sm text-muted-foreground">Host-owned frame: title, health dot, actions slot. The Plugin renders the body.</div>
        </WidgetCard>
      </div>
      <Hero total={9} problems={[{ title: "Checks", health: { tone: "down", summary: "paperless.<domain> down 14 min" } }]} />
      <ExpandableCard title="ExpandableCard" health={{ tone: "up", summary: "one-line summary when collapsed" }} open={open} onToggle={() => setOpen(!open)}>
        <div className="text-sm">Detail when expanded.</div>
      </ExpandableCard>
      <div className="text-sm text-muted-foreground">Also used here: Sheet (the Add Widget drawer), PageHeader, AppIcon.</div>
    </div>
  );
}

// ---- Add Widget picker (shared by every variant) ----
export function AddWidgetSheet({ open, onClose, placed, onAdd }: { open: boolean; onClose: () => void; placed: string[]; onAdd: (id: string) => void }) {
  const byPlugin = plugins.filter((p) => p.installed);
  return (
    <Sheet open={open} onClose={onClose} title="Add Widget">
      <div className="space-y-5">
        {byPlugin.map((p) => {
          const ws = widgets.filter((w) => w.plugin === p.id);
          if (!ws.length) return null;
          return (
            <div key={p.id}>
              <div className="mb-2 text-xs font-semibold uppercase tracking-wide text-muted-foreground">{p.name}</div>
              <div className="space-y-2">
                {ws.map((w) => {
                  const on = placed.includes(w.id);
                  return (
                    <div key={w.id} className="flex items-center gap-3 rounded-ui border bg-card px-3 py-2">
                      <div>
                        <div className="text-sm font-medium">{w.title}</div>
                        <div className="text-xs text-muted-foreground">sizes {w.sizes.join(" / ")}</div>
                      </div>
                      <Button size="sm" className="ml-auto" variant={on ? "outline" : "default"} disabled={on} onClick={() => onAdd(w.id)}>
                        {on ? "On startpage" : "Add"}
                      </Button>
                    </div>
                  );
                })}
              </div>
            </div>
          );
        })}
        <div className="text-xs text-muted-foreground">Widgets from Plugins that aren't installed (Docs) don't appear. Install them from Plugins.</div>
      </div>
    </Sheet>
  );
}

export const problemsOf = (ids: string[]) =>
  ids
    .map((id) => widgets.find((w) => w.id === id)!)
    .filter((w) => w.health.tone !== "up" && w.health.tone !== "idle")
    .map((w) => ({ title: w.title, health: w.health }));

// ---- Floating variant switcher (prototype chrome, not part of the design) ----
export const VARIANTS = [
  { key: "A", name: "Sidebar + sized grid" },
  { key: "B", name: "Icon rail + lanes" },
  { key: "C", name: "Grouped nav + summary stack" },
] as const;

export function Switcher({ current, onChange }: { current: string; onChange: (k: string) => void }) {
  const i = VARIANTS.findIndex((v) => v.key === current);
  const go = (d: number) => onChange(VARIANTS[(i + d + VARIANTS.length) % VARIANTS.length].key);
  useEffect(() => {
    const h = (e: KeyboardEvent) => {
      const el = document.activeElement as HTMLElement | null;
      if (el && (el.tagName === "INPUT" || el.tagName === "TEXTAREA" || el.isContentEditable)) return;
      if (e.key === "ArrowLeft") go(-1);
      if (e.key === "ArrowRight") go(1);
    };
    window.addEventListener("keydown", h);
    return () => window.removeEventListener("keydown", h);
  });
  return (
    <div
      className="fixed bottom-4 left-1/2 z-50 flex -translate-x-1/2 items-center gap-2 rounded-full bg-black px-2 py-1.5 text-sm text-white shadow-2xl ring-2 ring-fuchsia-500"
      style={{ fontFamily: "ui-sans-serif, system-ui" }}
    >
      <button className="rounded-full p-1 hover:bg-white/20 cursor-pointer" onClick={() => go(-1)}>
        <ChevronLeft className="h-4 w-4" />
      </button>
      <span className="min-w-56 text-center">
        <b>{current}</b> · {VARIANTS[i]?.name} <span className="text-white/50">PROTOTYPE</span>
      </span>
      <button className="rounded-full p-1 hover:bg-white/20 cursor-pointer" onClick={() => go(1)}>
        <ChevronRight className="h-4 w-4" />
      </button>
    </div>
  );
}
