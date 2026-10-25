// PROTOTYPE (wayfinder: "What should the shell, Overview and a Plugin Page
// look like in the new language?", then "What do the remaining screens look
// like in the Control room language?"): the shell is variant A, "Control
// room"; ?variant= now switches each screen's own variants, named below.
// Lives on prototype/ui-screens only.
import { useQuery } from "@ddash/plugin";
import { CoreService } from "@ddash/proto/ddash/core/core_pb";
import { cx, setVariant, useVariant, variants, type Variant } from "@ddash/ui";
import { ChevronLeft, ChevronRight, LogOut, Menu as MenuIcon, Moon, Search, Sun, X } from "lucide-react";
import { useEffect, useState, type ReactNode } from "react";
import { NavLink, useLocation, useNavigate } from "react-router";
import { useScheme } from "./appearance";
import { icon } from "./icons";
import type { NavItem } from "./nav";

export interface ChromeProps {
  items: NavItem[];
  /** The live badge of a Plugin's Nav Entry, rendered in its scope. */
  badge: (item: NavItem) => ReactNode;
  banner: ReactNode;
  children: ReactNode;
}

interface Entry {
  to: string;
  label: string;
  icon: string;
  end?: boolean;
  aside?: ReactNode;
}

function entries({ items, badge }: Pick<ChromeProps, "items" | "badge">) {
  const main: Entry[] = [{ to: "/", label: "Overview", icon: "layout-dashboard", end: true }];
  const plugins: Entry[] = items.map((i) => ({ to: `/${i.plugin.name}`, label: i.label, icon: i.icon, aside: badge(i) }));
  const system: Entry[] = [
    { to: "/plugins", label: "Plugins", icon: "puzzle" },
    { to: "/settings", label: "Settings", icon: "settings" },
  ];
  return { main, plugins, system, all: [...main, ...plugins, ...system] };
}

function usePageLabel(all: Entry[]) {
  const { pathname } = useLocation();
  const first = "/" + (pathname.split("/")[1] ?? "");
  return all.find((e) => (e.end ? pathname === e.to : first === e.to))?.label ?? "ddash";
}

function useOperator() {
  return useQuery(CoreService.method.getOperator, {}).data?.operator?.name ?? "";
}

function SchemeToggle({ className }: { className?: string }) {
  const [scheme, setScheme] = useScheme();
  const Icon = scheme === "dark" ? Sun : Moon;
  return (
    <button type="button" aria-label="Switch light/dark" onClick={() => setScheme(scheme === "dark" ? "light" : "dark")} className={cx("grid h-9 w-9 cursor-pointer place-items-center rounded-lg", className)}>
      <Icon className="h-4 w-4" aria-hidden />
    </button>
  );
}

function Brand({ sub = true }: { sub?: boolean }) {
  return (
    <div className="flex items-center gap-3">
      <div className="grid h-10 w-10 place-items-center rounded-xl bg-linear-to-br from-nav-accent to-primary text-xl font-black text-white shadow-[0_5px_20px_rgb(19_193_165/0.2)]">d</div>
      <div>
        <strong className="block text-xl leading-none font-extrabold tracking-tight text-nav-strong">ddash</strong>
        {sub && <span className="mt-1.5 block text-[9px] font-extrabold tracking-[0.16em] text-nav-foreground/70">HOME NETWORK</span>}
      </div>
    </div>
  );
}

export function Chrome(props: ChromeProps) {
  return <ChromeA {...props} />;
}

/* ---------- A: Control room — the inspiration's dark sidebar ---------- */

function NavA({ e }: { e: Entry }) {
  const Icon = icon(e.icon);
  return (
    <NavLink
      to={e.to}
      end={e.end}
      className={({ isActive }) =>
        cx(
          "mb-1 flex items-center gap-3 rounded-lg px-3.5 py-2.5 text-sm font-semibold transition-colors",
          isActive ? "bg-nav-active text-nav-strong shadow-[inset_3px_0_var(--nav-accent)]" : "text-nav-foreground hover:bg-nav-hover hover:text-nav-strong",
        )
      }
    >
      <Icon className="h-4.5 w-4.5" aria-hidden />
      {e.label}
      {e.aside && <span className="ml-auto flex items-center">{e.aside}</span>}
    </NavLink>
  );
}

function SidebarA({ items, badge, onNavigate }: Pick<ChromeProps, "items" | "badge"> & { onNavigate?: () => void }) {
  const { main, plugins, system } = entries({ items, badge });
  const operator = useOperator();
  const section = (label: string, es: Entry[]) => (
    <div className="px-3.5 pt-6">
      <div className="mb-3 px-3.5 text-[10px] font-extrabold tracking-[0.14em] text-nav-foreground/60">{label}</div>
      {es.map((e) => (
        <NavA key={e.to} e={e} />
      ))}
    </div>
  );
  return (
    <div className="relative flex h-full flex-col overflow-y-auto" onClick={(ev) => (ev.target as HTMLElement).closest("a") && onNavigate?.()}>
      <div className="pointer-events-none absolute -top-28 -left-32 h-60 w-60 bg-[radial-gradient(circle,rgb(31_179_161/0.14),transparent_70%)]" />
      <div className="border-b border-nav-border px-6 pt-8 pb-7">
        <Brand />
      </div>
      <nav aria-label="Main">
        {section("MAIN", main)}
        {section("PLUGINS", plugins)}
        {section("SYSTEM", system)}
      </nav>
      <div className="mt-auto p-5">
        <section aria-label="Operator" className="rounded-2xl border border-nav-border bg-linear-to-br from-nav-hover to-nav p-4">
          <div className="text-[10px] font-extrabold tracking-widest text-nav-foreground/70">SIGNED IN</div>
          <div className="mt-1.5 flex items-center gap-2 text-sm font-bold text-nav-strong">
            <span className="h-1.5 w-1.5 rounded-full bg-nav-accent shadow-[0_0_0_4px_rgb(43_182_159/0.15)]" />
            <span className="truncate">{operator}</span>
            <form method="post" action="/auth/logout" className="ml-auto">
              <button type="submit" aria-label="Sign out" className="cursor-pointer text-nav-foreground hover:text-nav-strong">
                <LogOut className="h-4 w-4" aria-hidden />
              </button>
            </form>
          </div>
        </section>
      </div>
    </div>
  );
}

function ChromeA({ items, badge, banner, children }: ChromeProps) {
  const [open, setOpen] = useState(false);
  const label = usePageLabel(entries({ items, badge }).all);
  return (
    <div className="flex min-h-screen bg-background font-sans text-foreground">
      {/* The column stretches to the page's full height, so its navy reaches the bottom of any page; only its content sticks. */}
      <aside className="hidden w-64 shrink-0 border-r border-nav-border bg-nav lg:block">
        <div className="sticky top-0 h-screen">
          <SidebarA items={items} badge={badge} />
        </div>
      </aside>
      {open && (
        <div className="fixed inset-0 z-40 bg-black/50 lg:hidden" onClick={() => setOpen(false)}>
          <aside className="h-full w-72 bg-nav" onClick={(e) => e.stopPropagation()}>
            <SidebarA items={items} badge={badge} onNavigate={() => setOpen(false)} />
          </aside>
        </div>
      )}
      <div className="min-w-0 flex-1">
        <div className="sticky top-0 z-30 flex items-center justify-between bg-nav px-4 py-3 lg:hidden">
          <Brand sub={false} />
          <button type="button" aria-label="Open navigation" className="grid h-9 w-9 place-items-center rounded-lg text-nav-strong" onClick={() => setOpen(true)}>
            <MenuIcon className="h-5 w-5" aria-hidden />
          </button>
        </div>
        <main className="mx-auto max-w-[1520px] px-4 pt-6 pb-24 sm:px-8 lg:px-11 lg:pt-9">
          <div className="mb-8 hidden items-center justify-between lg:flex">
            <div className="text-xs font-semibold text-muted-foreground">
              ddash<span className="px-3 text-border">/</span>
              <b className="font-bold text-foreground/80">{label}</b>
            </div>
            <SchemeToggle className="border border-border bg-card text-muted-foreground hover:text-foreground" />
          </div>
          {banner}
          {children}
        </main>
      </div>
    </div>
  );
}

/* ---------- The floating switcher (dev only) ---------- */

/** Each screen's variants, by the path it lives on. */
const screens: { match: (path: string) => boolean; screen: string; names: Partial<Record<Variant, string>> }[] = [
  { match: (p) => p === "/", screen: "Overview edit", names: { "1": "Edit in place", "2": "Arrange board", "3": "Docked library" } },
  { match: (p) => p.startsWith("/plugins"), screen: "Catalogue", names: { "1": "Explorer · Sheet form", "2": "Cards · Dialog form" } },
  { match: (p) => p.startsWith("/settings/audit-log"), screen: "Audit Log", names: { "1": "Table · hops in a Sheet", "2": "Timeline · hops inline" } },
  { match: (p) => p.startsWith("/settings"), screen: "Settings", names: { "1": "Stacked panels", "2": "Live previews" } },
];

export function PrototypeSwitcher() {
  const v = useVariant();
  const [hidden, setHidden] = useState(false);
  const navigate = useNavigate();
  const { pathname, search } = useLocation();
  const go = (to: Variant) => {
    setVariant(to);
    const q = new URLSearchParams(search);
    q.set("variant", to);
    navigate({ pathname, search: q.toString() }, { replace: true });
  };
  const screen = screens.find((s) => s.match(pathname));
  const keys = screen ? variants.filter((k) => screen.names[k]) : [];
  const step = (d: number) => keys.length && go(keys[(Math.max(0, keys.indexOf(v)) + d + keys.length) % keys.length]);
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      const t = e.target as HTMLElement;
      if (t.closest("input, textarea, select, [contenteditable]")) return;
      if (e.key === "ArrowLeft") step(-1);
      if (e.key === "ArrowRight") step(1);
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  });
  if (!import.meta.env.DEV || hidden || !screen) return null;
  return (
    <div className="fixed bottom-20 left-1/2 z-[60] flex -translate-x-1/2 items-center gap-1 rounded-full bg-[#ff3e8a] p-1 text-sm font-bold text-white shadow-2xl ring-2 ring-white md:bottom-5">
      <button type="button" aria-label="Previous variant" className="grid h-8 w-8 cursor-pointer place-items-center rounded-full hover:bg-white/20" onClick={() => step(-1)}>
        <ChevronLeft className="h-4 w-4" />
      </button>
      <span className="px-2 whitespace-nowrap">
        {screen.screen} {keys.includes(v) ? v : keys[0]}: {screen.names[keys.includes(v) ? v : keys[0]]}
      </span>
      <button type="button" aria-label="Next variant" className="grid h-8 w-8 cursor-pointer place-items-center rounded-full hover:bg-white/20" onClick={() => step(1)}>
        <ChevronRight className="h-4 w-4" />
      </button>
      <button type="button" aria-label="Hide switcher" className="grid h-8 w-8 cursor-pointer place-items-center rounded-full hover:bg-white/20" onClick={() => setHidden(true)}>
        <X className="h-3.5 w-3.5" />
      </button>
    </div>
  );
}
