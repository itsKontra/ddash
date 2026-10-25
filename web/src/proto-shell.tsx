// PROTOTYPE (wayfinder: "What should the shell, Overview and a Plugin Page
// look like in the new language?"): three shell chromes, switched by
// ?variant=, and the floating switcher. Lives on prototype/ui-redesign only.
import { useQuery } from "@ddash/plugin";
import { CoreService } from "@ddash/proto/ddash/core/core_pb";
import { cx, setVariant, useVariant, variantNames, variants, type Variant } from "@ddash/ui";
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
  const v = useVariant();
  return v === "B" ? <ChromeB {...props} /> : v === "C" ? <ChromeC {...props} /> : <ChromeA {...props} />;
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
      <aside className="sticky top-0 hidden h-screen w-64 shrink-0 border-r border-nav-border bg-nav lg:block">
        <SidebarA items={items} badge={badge} />
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

/* ---------- B: Rail & split — icon rail, top header, bottom tabs on phones ---------- */

function RailLink({ e, bottom }: { e: Entry; bottom?: boolean }) {
  const Icon = icon(e.icon);
  return (
    <NavLink
      to={e.to}
      end={e.end}
      title={e.label}
      className={({ isActive }) =>
        cx(
          "relative grid place-items-center rounded-xl transition-colors",
          bottom ? "h-12 flex-1" : "h-12 w-12",
          isActive ? "bg-nav-active text-nav-strong" : "text-nav-foreground hover:bg-nav-hover hover:text-nav-strong",
          !bottom && "aria-[current=page]:before:absolute aria-[current=page]:before:-left-3.5 aria-[current=page]:before:h-6 aria-[current=page]:before:w-1 aria-[current=page]:before:rounded-r aria-[current=page]:before:bg-nav-accent",
        )
      }
    >
      <Icon className="h-5 w-5" aria-hidden />
      <span className="sr-only">{e.label}</span>
      {e.aside && <span className="absolute -top-2 -right-4 origin-top-right scale-75">{e.aside}</span>}
    </NavLink>
  );
}

function ChromeB({ items, badge, banner, children }: ChromeProps) {
  const { main, plugins, system, all } = entries({ items, badge });
  const label = usePageLabel(all);
  const operator = useOperator();
  return (
    <div className="flex min-h-screen bg-background font-sans text-foreground">
      <aside className="sticky top-0 hidden h-screen w-20 shrink-0 flex-col items-center gap-2 border-r border-nav-border bg-nav py-5 md:flex">
        <div className="mb-4 grid h-10 w-10 place-items-center rounded-xl bg-linear-to-br from-nav-accent to-primary text-xl font-black text-white">d</div>
        <nav aria-label="Main" className="flex flex-1 flex-col items-center gap-2">
          {[...main, ...plugins].map((e) => (
            <RailLink key={e.to} e={e} />
          ))}
          <div className="mt-auto flex flex-col items-center gap-2 border-t border-nav-border pt-3">
            {system.map((e) => (
              <RailLink key={e.to} e={e} />
            ))}
          </div>
        </nav>
      </aside>
      <div className="min-w-0 flex-1">
        <header className="sticky top-0 z-30 flex items-center gap-4 border-b border-border bg-card/85 px-4 py-3 backdrop-blur sm:px-8">
          <div className="min-w-0">
            <div className="text-[10px] font-extrabold tracking-[0.16em] text-primary">DDASH</div>
            <div className="truncate text-lg leading-tight font-extrabold tracking-tight">{label}</div>
          </div>
          <div className="mx-auto hidden h-10 w-full max-w-md items-center gap-2 rounded-lg border border-border bg-background px-3 text-sm text-muted-foreground md:flex">
            <Search className="h-4 w-4" aria-hidden /> Search Apps, Checks, Devices…
            <kbd className="ml-auto rounded border border-border px-1.5 text-[10px] font-bold">⌘K</kbd>
          </div>
          <div className="ml-auto flex items-center gap-2 md:ml-0">
            <SchemeToggle className="text-muted-foreground hover:bg-muted" />
            <div className="grid h-9 w-9 place-items-center rounded-full bg-primary/12 text-sm font-extrabold text-primary" title={operator}>
              {operator.slice(0, 1).toUpperCase()}
            </div>
          </div>
        </header>
        <main className="mx-auto max-w-[1600px] px-4 pt-6 pb-28 sm:px-8">
          {banner}
          {children}
        </main>
      </div>
      <nav aria-label="Main" className="fixed inset-x-0 bottom-0 z-30 flex gap-1 border-t border-nav-border bg-nav px-2 py-2 md:hidden">
        {[...main, ...plugins].slice(0, 5).map((e) => (
          <RailLink key={e.to} e={e} bottom />
        ))}
      </nav>
    </div>
  );
}

/* ---------- C: Light editorial — floating light sidebar, chip nav on phones ---------- */

function ChromeC({ items, badge, banner, children }: ChromeProps) {
  const { main, plugins, system } = entries({ items, badge });
  const operator = useOperator();
  const link = (e: Entry, chip?: boolean) => {
    const Icon = icon(e.icon);
    return (
      <NavLink
        key={e.to}
        to={e.to}
        end={e.end}
        className={({ isActive }) =>
          cx(
            "flex items-center gap-3 text-sm font-semibold whitespace-nowrap transition-colors",
            chip ? "rounded-full border px-3.5 py-1.5" : "rounded-xl px-3 py-2.5",
            isActive ? (chip ? "border-primary bg-primary text-primary-foreground" : "bg-primary/10 font-bold text-primary") : chip ? "border-border bg-card text-muted-foreground" : "text-muted-foreground hover:bg-muted hover:text-foreground",
          )
        }
      >
        <Icon className="h-4 w-4" aria-hidden />
        {e.label}
        {e.aside && !chip && <span className="ml-auto flex items-center">{e.aside}</span>}
      </NavLink>
    );
  };
  return (
    <div className="min-h-screen bg-background font-sans text-foreground lg:flex">
      <aside className="sticky top-4 m-4 hidden h-[calc(100vh-2rem)] w-60 shrink-0 flex-col rounded-2xl border border-border bg-card p-3 shadow-panel lg:flex">
        <div className="flex items-center gap-2 px-3 pt-2 pb-5">
          <span className="h-2.5 w-2.5 rounded-full bg-primary" />
          <span className="text-lg font-extrabold tracking-tight">ddash</span>
          <SchemeToggle className="ml-auto text-muted-foreground hover:bg-muted" />
        </div>
        <nav aria-label="Main" className="flex flex-1 flex-col gap-0.5">
          {main.map((e) => link(e))}
          <div className="mt-5 mb-1.5 px-3 text-[10px] font-extrabold tracking-[0.14em] text-muted-foreground">PLUGINS</div>
          {plugins.map((e) => link(e))}
          <div className="mt-auto flex flex-col gap-0.5 border-t border-border pt-2">{system.map((e) => link(e))}</div>
        </nav>
        <div className="mt-2 flex items-center gap-2 rounded-xl bg-muted px-3 py-2.5 text-sm">
          <span className="truncate font-semibold">{operator}</span>
          <form method="post" action="/auth/logout" className="ml-auto">
            <button type="submit" aria-label="Sign out" className="cursor-pointer text-muted-foreground hover:text-foreground">
              <LogOut className="h-4 w-4" aria-hidden />
            </button>
          </form>
        </div>
      </aside>
      <div className="sticky top-0 z-30 border-b border-border bg-background/90 backdrop-blur lg:hidden">
        <div className="flex items-center gap-2 px-4 pt-3">
          <span className="h-2.5 w-2.5 rounded-full bg-primary" />
          <span className="text-lg font-extrabold tracking-tight">ddash</span>
          <SchemeToggle className="ml-auto text-muted-foreground" />
        </div>
        <nav aria-label="Main" className="flex gap-2 overflow-x-auto px-4 py-3">
          {[...main, ...plugins, ...system].map((e) => link(e, true))}
        </nav>
      </div>
      <main className="mx-auto min-w-0 max-w-[1400px] flex-1 px-4 pt-6 pb-24 sm:px-8 lg:px-12 lg:pt-12">
        {banner}
        {children}
      </main>
    </div>
  );
}

/* ---------- The floating switcher (dev only) ---------- */

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
  const step = (d: number) => go(variants[(variants.indexOf(v) + d + variants.length) % variants.length]);
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
  if (!import.meta.env.DEV || hidden) return null;
  return (
    <div className="fixed bottom-20 left-1/2 z-[60] flex -translate-x-1/2 items-center gap-1 rounded-full bg-[#ff3e8a] p-1 text-sm font-bold text-white shadow-2xl ring-2 ring-white md:bottom-5">
      <button type="button" aria-label="Previous variant" className="grid h-8 w-8 cursor-pointer place-items-center rounded-full hover:bg-white/20" onClick={() => step(-1)}>
        <ChevronLeft className="h-4 w-4" />
      </button>
      <span className="px-2 whitespace-nowrap">
        {v} ({variantNames[v]})
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
