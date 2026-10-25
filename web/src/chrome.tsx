// The shell's frame in the Control room look: the navy sidebar (a drawer on
// phones), the top bar with the breadcrumb and the scheme toggle. Shell-only;
// Plugins never reach the nav.
import { useQuery } from "@ddash/plugin";
import { CoreService } from "@ddash/proto/ddash/core/core_pb";
import { cx, Icon, type IconName } from "@ddash/ui";
import { useEffect, useRef, useState, type KeyboardEvent, type ReactNode } from "react";
import { NavLink, useLocation } from "react-router";
import { useScheme } from "./appearance";
import type { NavItem } from "./nav";

export interface ChromeProps {
  items: NavItem[];
  /** What a Plugin's Nav Entry shows at its end: its badge, or that it failed to load. */
  aside: (item: NavItem) => ReactNode;
  /** Shown above the Page, e.g. the unreachable banner. */
  banner?: ReactNode;
  children: ReactNode;
}

interface Entry {
  to: string;
  label: string;
  /** A lucide name, a plain string when it comes from a manifest. */
  icon: string;
  end?: boolean;
  aside?: ReactNode;
}

interface Sections {
  main: Entry[];
  plugins: Entry[];
  system: Entry[];
}

function sections(items: NavItem[], aside: ChromeProps["aside"]): Sections {
  return {
    main: [{ to: "/", label: "Overview", icon: "layout-dashboard", end: true }],
    plugins: items.map((i) => ({ to: `/${i.plugin.name}`, label: i.label, icon: i.icon, aside: aside(i) })),
    system: [
      { to: "/plugins", label: "Plugins", icon: "puzzle" },
      { to: "/settings", label: "Settings", icon: "settings" },
    ],
  };
}

/** The label of the Nav Entry the path is under, which names the current Page. */
function usePageLabel({ main, plugins, system }: Sections) {
  const { pathname } = useLocation();
  const first = "/" + (pathname.split("/")[1] ?? "");
  return [...main, ...plugins, ...system].find((e) => (e.end ? pathname === e.to : first === e.to))?.label;
}

export function Chrome({ items, aside, banner, children }: ChromeProps) {
  const [open, setOpen] = useState(false);
  const opener = useRef<HTMLButtonElement>(null);
  const all = sections(items, aside);
  const label = usePageLabel(all);
  const close = () => {
    setOpen(false);
    opener.current?.focus();
  };
  return (
    <div className="flex min-h-screen bg-background font-sans text-foreground">
      {/* The column stretches with the page so its navy reaches the bottom; only its content sticks. */}
      <aside className="hidden w-64 shrink-0 border-r border-nav-border bg-nav lg:block">
        <div className="sticky top-0 h-screen">
          <Sidebar sections={all} />
        </div>
      </aside>
      {open && <Drawer sections={all} onClose={close} />}
      <div className="min-w-0 flex-1">
        <header className="sticky top-0 z-30 flex items-center justify-between bg-nav px-4 py-3 lg:hidden">
          <Brand sub={false} />
          <div className="flex items-center gap-1">
            <SchemeToggle className="text-nav-strong hover:bg-nav-hover" />
            <button
              ref={opener}
              type="button"
              aria-label="Open navigation"
              aria-expanded={open}
              onClick={() => setOpen(true)}
              className="grid h-9 w-9 cursor-pointer place-items-center rounded-ui-sm text-nav-strong hover:bg-nav-hover"
            >
              <Icon name="menu" />
            </button>
          </div>
        </header>
        <main className="mx-auto max-w-[1520px] px-4 pt-6 pb-24 sm:px-8 lg:px-11 lg:pt-9">
          <div className="mb-8 hidden items-center justify-between lg:flex">
            <nav aria-label="Breadcrumb" className="text-xs font-semibold text-muted-foreground">
              ddash
              <span aria-hidden className="px-3 text-border">
                /
              </span>
              <span aria-current="page" className="font-bold text-foreground">
                {label ?? "Not found"}
              </span>
            </nav>
            <SchemeToggle className="border border-border bg-card text-muted-foreground shadow-elevation hover:text-foreground" />
          </div>
          {banner}
          {children}
        </main>
      </div>
    </div>
  );
}

/** Switches light and dark, remembered in this browser. */
function SchemeToggle({ className }: { className?: string }) {
  const [scheme, setScheme] = useScheme();
  const dark = scheme === "dark";
  return (
    <button
      type="button"
      aria-label="Dark mode"
      aria-pressed={dark}
      onClick={() => setScheme(dark ? "light" : "dark")}
      className={cx("grid h-9 w-9 cursor-pointer place-items-center rounded-ui-sm", className)}
    >
      <Icon name={dark ? "sun" : "moon"} />
    </button>
  );
}

function Brand({ sub = true }: { sub?: boolean }) {
  return (
    <div className="flex items-center gap-3">
      <div
        aria-hidden
        className="grid h-10 w-10 place-items-center rounded-ui bg-linear-to-br from-nav-accent to-primary text-xl font-black text-primary-foreground shadow-[0_5px_20px_var(--color-nav-glow-strong)]"
      >
        d
      </div>
      <div>
        <strong className="block text-xl leading-none font-extrabold tracking-tight text-nav-strong">ddash</strong>
        {sub && <span className="mt-1.5 block text-[9px] font-extrabold tracking-[0.16em] text-nav-foreground">HOME NETWORK</span>}
      </div>
    </div>
  );
}

function Sidebar({ sections: { main, plugins, system }, onNavigate }: { sections: Sections; onNavigate?: () => void }) {
  return (
    <div className="relative flex h-full flex-col overflow-y-auto" onClick={(e) => (e.target as HTMLElement).closest("a") && onNavigate?.()}>
      {/* The brand's halo, bleeding in from the corner. */}
      <div
        aria-hidden
        className="pointer-events-none absolute -top-28 -left-32 size-60 rounded-full bg-[radial-gradient(circle,var(--color-nav-glow),transparent_70%)]"
      />
      <div className="relative border-b border-nav-border px-6 pt-8 pb-7">
        <Brand />
      </div>
      <nav aria-label="Main">
        <Section label="MAIN" entries={main} />
        {plugins.length > 0 && <Section label="PLUGINS" entries={plugins} />}
        <Section label="SYSTEM" entries={system} />
      </nav>
      <div className="mt-auto p-5">
        <Operator />
      </div>
    </div>
  );
}

function Section({ label, entries }: { label: string; entries: Entry[] }) {
  const id = `nav-${label.toLowerCase()}`;
  return (
    <div role="group" aria-labelledby={id} className="px-3.5 pt-6">
      <div id={id} className="mb-3 px-3.5 text-[10px] font-extrabold tracking-[0.14em] text-nav-foreground">
        {label}
      </div>
      {entries.map((e) => (
        <Link key={e.to} entry={e} />
      ))}
    </div>
  );
}

function Link({ entry: { to, label, icon, end, aside } }: { entry: Entry }) {
  return (
    <NavLink
      to={to}
      end={end}
      className={({ isActive }) =>
        cx(
          "mb-1 flex items-center gap-3 rounded-ui-sm px-3.5 py-2.5 text-sm font-semibold transition-colors",
          isActive ? "bg-nav-active text-nav-strong shadow-[inset_3px_0_var(--nav-accent)]" : "text-nav-foreground hover:bg-nav-hover hover:text-nav-strong",
        )
      }
    >
      <Icon name={icon as IconName} />
      {label}
      {aside && <span className="ml-auto flex items-center">{aside}</span>}
    </NavLink>
  );
}

/** Who is signed in, and Sign out: the host ends the session and Keycloak's. */
function Operator() {
  const { data } = useQuery(CoreService.method.getOperator, {});
  return (
    <section aria-label="Operator" className="rounded-ui-lg border border-nav-border bg-linear-to-br from-nav-hover to-nav p-4">
      <div className="text-[10px] font-extrabold tracking-widest text-nav-foreground">SIGNED IN</div>
      <div className="mt-1.5 flex items-center gap-2 text-sm font-bold text-nav-strong">
        <span aria-hidden className="h-1.5 w-1.5 shrink-0 rounded-full bg-nav-accent" />
        <span className="min-w-0 truncate">{data?.operator?.name}</span>
        <form method="post" action="/auth/logout" className="ml-auto flex">
          <button type="submit" aria-label="Sign out" className="cursor-pointer text-nav-foreground hover:text-nav-strong">
            <Icon name="log-out" />
          </button>
        </form>
      </div>
    </section>
  );
}

const focusable = "a[href], button:not([disabled]), input:not([disabled]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex='-1'])";

/**
 * The sidebar as a drawer over the Page, at phone width. It traps the focus;
 * Escape, a click outside, Close or following a link closes it.
 */
function Drawer({ sections, onClose }: { sections: Sections; onClose: () => void }) {
  const panel = useRef<HTMLDivElement>(null);
  useEffect(() => {
    panel.current?.querySelector<HTMLElement>(focusable)?.focus();
  }, []);
  const onKeyDown = (e: KeyboardEvent) => {
    if (e.key === "Escape") return onClose();
    if (e.key !== "Tab" || !panel.current) return;
    const all = [...panel.current.querySelectorAll<HTMLElement>(focusable)];
    const first = all[0];
    const last = all[all.length - 1];
    if (e.shiftKey && document.activeElement === first) {
      e.preventDefault();
      last?.focus();
    } else if (!e.shiftKey && document.activeElement === last) {
      e.preventDefault();
      first?.focus();
    }
  };
  return (
    <div className="fixed inset-0 z-40 bg-foreground/40 backdrop-blur-sm lg:hidden" onClick={onClose}>
      <div
        ref={panel}
        role="dialog"
        aria-modal="true"
        aria-label="Navigation"
        onKeyDown={onKeyDown}
        onClick={(e) => e.stopPropagation()}
        className="relative h-full w-72 max-w-[85vw] bg-nav shadow-elevation-raised"
      >
        <button
          type="button"
          aria-label="Close navigation"
          onClick={onClose}
          className="absolute top-3 right-3 z-10 grid h-9 w-9 cursor-pointer place-items-center rounded-ui-sm text-nav-strong hover:bg-nav-hover"
        >
          <Icon name="x" />
        </button>
        <Sidebar sections={sections} onNavigate={onClose} />
      </div>
    </div>
  );
}
