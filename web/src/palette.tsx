// The command palette (Ctrl K / ⌘ K, or / outside a text field): opens an
// App, goes to a Page, runs a Concern's Action or a Plugin's Command, or
// draws the Command's Sheet (a Plan, a form) in the Plugin's scope on any
// Page. It only calls what the Plugin offers; it never changes anything
// itself.
import { PluginScope, type Command, type Fetch } from "@ddash/plugin";
import { AppIcon, Button, cx, Icon, Notice, Presence } from "@ddash/ui";
import { backdropMotion } from "@ddash/ui/motion";
import { useCallback, useEffect, useId, useRef, useState, type KeyboardEvent, type ReactNode } from "react";
import { useNavigate } from "react-router";
import { useScheme } from "./appearance";
import { ErrorBoundary } from "./boundary";
import { commandEntries, concernEntries, listed, pageEntries, type Destination, type Entry } from "./commands";
import { signature, useConcernList } from "./needs-you";
import type { LoadedPlugin, Plugins } from "./plugins";

/** Whether the palette is open, and opening and closing it; closing gives the focus back to where it was. */
export interface PaletteState {
  open: boolean;
  /** How many times it opened: each opening starts empty, even one that interrupts the closing. */
  opened: number;
  show: () => void;
  hide: () => void;
}

export function usePalette(): PaletteState {
  const [open, setOpen] = useState(false);
  const [opened, setOpened] = useState(0);
  const opener = useRef<HTMLElement | null>(null);
  const show = useCallback(() => {
    opener.current = document.activeElement instanceof HTMLElement ? document.activeElement : null;
    setOpen(true);
    setOpened((n) => n + 1);
  }, []);
  const hide = useCallback(() => {
    setOpen(false);
    const o = opener.current;
    opener.current = null;
    if (o?.isConnected) o.focus();
  }, []);
  return { open, opened, show, hide };
}

// Where a typed "/" is text, not the palette's key.
const typing = "input, textarea, select, [contenteditable]:not([contenteditable='false'])";

/**
 * The palette's keys on every Page: Ctrl K or ⌘ K opens and closes it, /
 * opens it outside a text field. Neither opens it over another dialog.
 */
function useKeys({ open, show, hide }: PaletteState) {
  useEffect(() => {
    const onKey = (e: globalThis.KeyboardEvent) => {
      const k = e.key.toLowerCase() === "k" && (e.ctrlKey || e.metaKey) && !e.altKey && !e.shiftKey;
      const slash = e.key === "/" && !e.ctrlKey && !e.metaKey && !e.altKey && !(e.target instanceof Element && e.target.closest(typing));
      if (!k && !slash) return;
      if (open) {
        if (k) {
          e.preventDefault();
          hide();
        }
        return;
      }
      // Another dialog is open (one leaving, inert, isn't).
      if ([...document.querySelectorAll("[aria-modal='true']")].some((d) => !d.closest("[inert]"))) return;
      e.preventDefault();
      show();
    };
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, [open, show, hide]);
}

const mac = () => typeof navigator !== "undefined" && /Mac|iPhone|iPad/.test(navigator.userAgent);

function Kbd({ children }: { children: ReactNode }) {
  return <kbd className="rounded-md border border-border bg-muted px-1.5 py-0.5 font-sans text-[11px] font-semibold text-muted-foreground">{children}</kbd>;
}

/** The shell's button that opens the palette: "Search or run…  Ctrl K"; on a phone without the keys. */
export function PaletteTrigger({ onOpen, className }: { onOpen: () => void; className?: string }) {
  return (
    <button
      type="button"
      onClick={onOpen}
      aria-label="Search or run…"
      aria-keyshortcuts={mac() ? "Meta+K" : "Control+K"}
      className={cx(
        "flex h-9 min-w-0 cursor-pointer items-center gap-2.5 rounded-ui-sm border px-3 text-left text-sm transition-colors",
        className,
      )}
    >
      <Icon name="search" />
      {/* On a phone the short label, so it isn't cut. */}
      <span className="min-w-0 flex-1 truncate">
        <span className="sm:hidden">Search…</span>
        <span className="hidden sm:inline">Search or run…</span>
      </span>
      <span className="hidden sm:inline">
        <Kbd>{mac() ? "⌘ K" : "Ctrl K"}</Kbd>
      </span>
    </button>
  );
}

export interface PaletteProps {
  state: PaletteState;
  plugins: Plugins;
  fetch?: Fetch;
  /** The Pages it goes to, in the sidebar's order. */
  pages: readonly Destination[];
}

/** The Sheet a Command has open: whose, which Command, and its Sheet as run. */
interface OpenSheet {
  plugin: string;
  id: string;
  sheet: (close: () => void) => ReactNode;
}

/** A Command whose run failed, as the palette shows it until dismissed or run again. */
interface Failure {
  /** The Command's entry key: "<plugin>:<id>". */
  key: string;
  title: string;
  error: string;
}

/**
 * The palette: runs every installed Plugin's useCommands on every Page,
 * each in its scope behind an error boundary (one that throws offers
 * nothing), draws the dialog while open, the Sheet of the Command last
 * run, if it has one, and each Command's newly set error, since the Page
 * that would show it may not be mounted.
 */
export function Palette({ state, plugins, fetch, pages }: PaletteProps) {
  useKeys(state);
  const navigate = useNavigate();
  const [scheme, setScheme] = useScheme();
  const { needs, act } = useConcernList();
  const [reports, setReports] = useState<ReadonlyMap<string, readonly Command[]>>(new Map());
  // Each Plugin's Commands as last rendered, so an entry runs the newest.
  const [live] = useState(() => new Map<string, readonly Command[]>());
  const [sheet, setSheet] = useState<OpenSheet | null>(null);
  const closeSheet = useCallback(() => setSheet(null), []);
  const [failures, setFailures] = useState<readonly Failure[]>([]);
  // Each Command's error as last reported, so only a newly set one is shown.
  const seen = useRef(new Map<string, string>());
  const onReport = useCallback((plugin: string, commands: readonly Command[] | undefined) => {
    setReports((prev) => {
      const next = new Map(prev);
      if (commands) next.set(plugin, commands);
      else next.delete(plugin);
      return next;
    });
  }, []);

  const sources = (plugins.installed ?? []).flatMap((p) => {
    const loaded = plugins.loaded.get(p.name);
    return loaded?.definition?.useCommands ? [loaded] : [];
  });
  const offered = sources.flatMap((p) => (reports.has(p.name) ? [{ plugin: p.name, title: p.title, commands: reports.get(p.name)! }] : []));

  useEffect(() => {
    const now = [...reports].flatMap(([plugin, commands]) =>
      commands.flatMap((c): Failure[] => (typeof c.error === "string" && c.error ? [{ key: `${plugin}:${c.id}`, title: c.title, error: c.error }] : [])),
    );
    const fresh = now.filter((f) => seen.current.get(f.key) !== f.error);
    seen.current = new Map(now.map((f) => [f.key, f.error]));
    if (fresh.length > 0) setFailures((prev) => [...prev.filter((p) => !fresh.some((f) => f.key === p.key)), ...fresh]);
  }, [reports]);
  const dismiss = useCallback((key: string) => setFailures((prev) => prev.filter((f) => f.key !== key)), []);

  const go = useCallback((to: string) => void navigate(to), [navigate]);
  const runCommand = useCallback(
    (plugin: string, id: string) => {
      const c = live.get(plugin)?.find((x) => x.id === id);
      if (!c) return;
      dismiss(`${plugin}:${id}`);
      if ("sheet" in c) setSheet({ plugin, id, sheet: c.sheet });
      else c.run();
    },
    [live, dismiss],
  );
  const dark = scheme === "dark";
  const all = {
    concerns: concernEntries(needs.loud, act, go),
    pages: pageEntries(pages, go),
    actions: [
      {
        key: "scheme",
        group: "Actions",
        title: dark ? "Switch to light mode" : "Switch to dark mode",
        icon: dark ? "sun" : "moon",
        keywords: ["theme", "scheme", "light", "dark"],
        run: () => setScheme(dark ? "light" : "dark"),
      } satisfies Entry,
    ],
    commands: commandEntries(offered, runCommand),
  };

  return (
    <>
      {sources.map((p) => (
        <PluginScope key={p.name} plugin={p} fetch={fetch}>
          <ErrorBoundary fallback={() => null}>
            <Source plugin={p} live={live} open={sheet?.plugin === p.name ? sheet : null} onReport={onReport} onClose={closeSheet} />
          </ErrorBoundary>
        </PluginScope>
      ))}
      <Presence>{state.open && <Dialog key={state.opened} all={all} onClose={state.hide} />}</Presence>
      {failures.length > 0 && (
        <div className="fixed inset-x-4 bottom-4 z-50 flex flex-col gap-2 sm:inset-x-auto sm:right-6 sm:bottom-6 sm:w-96">
          {failures.map((f) => (
            <div key={f.key} className="flex items-start gap-1 rounded-ui-lg border border-border bg-card p-1.5 shadow-elevation-raised">
              <div className="min-w-0 flex-1">
                <Notice tone="down">
                  {f.title} failed: {f.error}
                </Notice>
              </div>
              <Button size="sm" variant="ghost" icon="x" aria-label={`Dismiss: ${f.title} failed`} onClick={() => dismiss(f.key)} />
            </div>
          ))}
        </div>
      )}
    </>
  );
}

/**
 * Runs one Plugin's useCommands in its scope: passes each changed report
 * up, keeps the newest in live, and draws the Sheet of the Command run,
 * which stays open when the Command goes away (a Start once it started).
 */
function Source({
  plugin,
  live,
  open,
  onReport,
  onClose,
}: {
  plugin: LoadedPlugin;
  live: Map<string, readonly Command[]>;
  open: OpenSheet | null;
  onReport: (plugin: string, commands: readonly Command[] | undefined) => void;
  onClose: () => void;
}) {
  const name = plugin.name;
  const commands = plugin.definition!.useCommands!();
  const sig = signature(commands);
  useEffect(() => {
    if (commands) live.set(name, commands);
    else live.delete(name);
  });
  useEffect(() => {
    onReport(name, live.get(name));
  }, [name, sig, live, onReport]);
  useEffect(() => () => onReport(name, undefined), [name, onReport]);

  // The Command's newest Sheet while it lasts, then the one run.
  const found = open && commands?.find((c) => c.id === open.id);
  const sheet = open && (found && "sheet" in found ? found.sheet : open.sheet);
  return <Presence>{sheet ? sheet(onClose) : null}</Presence>;
}

/**
 * The dialog: a search box over the entries, in groups while it is empty.
 * The box is a combobox whose active option is announced; arrows move,
 * Enter runs, Escape or a click outside closes.
 */
function Dialog({ all, onClose }: { all: Parameters<typeof listed>[1]; onClose: () => void }) {
  const id = useId();
  const [query, setQuery] = useState("");
  const [sel, setSel] = useState(0);
  const items = listed(query, all);
  const active = Math.min(sel, Math.max(items.length - 1, 0));
  const optionId = (i: number) => `${id}-option-${i}`;

  useEffect(() => {
    document.getElementById(optionId(active))?.scrollIntoView?.({ block: "nearest" });
  });

  const run = (i: number) => {
    const e = items[i];
    if (!e) return;
    // Closed first, so what the entry opens (a Sheet, a confirm) gives the focus back to where it was.
    onClose();
    e.run();
  };
  const onKeyDown = (e: KeyboardEvent<HTMLInputElement>) => {
    const n = items.length;
    switch (e.key) {
      case "ArrowDown":
      case "ArrowUp":
        e.preventDefault();
        if (n) setSel((active + (e.key === "ArrowDown" ? 1 : -1) + n) % n);
        return;
      case "Enter":
        e.preventDefault();
        run(active);
        return;
      case "Escape":
        e.preventDefault();
        e.stopPropagation();
        onClose();
        return;
      case "Tab":
        // Nothing else to tab to: the options are reached by the arrows.
        e.preventDefault();
    }
  };

  const querying = query.trim() !== "";
  // Empty, the entries are drawn in their groups; with a query, as one ranked list.
  const groups: { group: string; entries: { e: Entry; i: number }[] }[] = [];
  items.forEach((e, i) => {
    const last = groups[groups.length - 1];
    if (last?.group === e.group) last.entries.push({ e, i });
    else groups.push({ group: e.group, entries: [{ e, i }] });
  });
  const option = (e: Entry, i: number) => (
    <Option key={e.key} id={optionId(i)} entry={e} selected={i === active} querying={querying} onHover={() => setSel(i)} onRun={() => run(i)} />
  );

  return (
    <div className={cx("fixed inset-0 z-50 flex items-start justify-center bg-foreground/40 px-3 pt-[14vh] backdrop-blur-xs", backdropMotion)} onClick={onClose}>
      <div
        role="dialog"
        aria-modal="true"
        aria-label="Command palette"
        onClick={(e) => e.stopPropagation()}
        // A click anywhere in it keeps the focus in the search box.
        onMouseDown={(e) => e.target instanceof HTMLInputElement || e.preventDefault()}
        className="w-full max-w-160 overflow-hidden rounded-ui-lg border border-border bg-card text-foreground shadow-elevation-raised starting:-translate-y-1 starting:opacity-0 in-data-[state=closed]:-translate-y-1 in-data-[state=closed]:opacity-0 motion-safe:transition-[opacity,translate] motion-safe:duration-(--motion-fade)"
      >
        <div className="flex h-13.5 items-center gap-3 border-b border-border px-4 text-muted-foreground">
          <Icon name="search" />
          <input
            autoFocus
            role="combobox"
            aria-label="Search or run"
            aria-expanded="true"
            aria-controls={`${id}-list`}
            aria-autocomplete="list"
            aria-activedescendant={items.length ? optionId(active) : undefined}
            value={query}
            onChange={(e) => {
              setQuery(e.target.value);
              setSel(0);
            }}
            onKeyDown={onKeyDown}
            placeholder="Open an App, go to a Page…"
            autoComplete="off"
            spellCheck={false}
            className="min-w-0 flex-1 bg-transparent text-base text-foreground outline-none placeholder:text-muted-foreground"
          />
          <Kbd>esc</Kbd>
        </div>
        <div role="listbox" id={`${id}-list`} aria-label="Commands" className="max-h-[min(52vh,440px)] overflow-y-auto p-1.5">
          {items.length === 0 ? (
            <div className="p-7 text-center text-sm text-muted-foreground">Nothing matches “{query.trim()}”.</div>
          ) : querying ? (
            items.map(option)
          ) : (
            groups.map((g, n) => (
              <div key={g.group} role="group" aria-labelledby={`${id}-group-${n}`}>
                <div id={`${id}-group-${n}`} className="px-2.5 pt-2.5 pb-1 text-[11px] font-semibold tracking-[0.08em] text-muted-foreground uppercase">
                  {g.group}
                </div>
                {g.entries.map(({ e, i }) => option(e, i))}
              </div>
            ))
          )}
        </div>
        <div role="status" className="sr-only">
          {!querying ? "" : items.length === 1 ? "1 result" : `${items.length} results`}
        </div>
        <div className="flex items-center gap-3.5 border-t border-border px-4 py-2.5 text-xs text-muted-foreground">
          <span>
            <Kbd>↑</Kbd> <Kbd>↓</Kbd> move
          </span>
          <span>
            <Kbd>↵</Kbd> run
          </span>
          <span className="ml-auto hidden sm:inline">Type a word's start: “graf”, “restart pap”</span>
        </div>
      </div>
    </div>
  );
}

function Option({
  id,
  entry: e,
  selected,
  querying,
  onHover,
  onRun,
}: {
  id: string;
  entry: Entry;
  selected: boolean;
  querying: boolean;
  onHover: () => void;
  onRun: () => void;
}) {
  const aside = e.hint ?? (querying ? e.group : undefined);
  return (
    <div
      id={id}
      role="option"
      aria-selected={selected}
      onMouseMove={() => selected || onHover()}
      onClick={onRun}
      className={cx("flex cursor-pointer items-center gap-3 rounded-ui-sm px-2.5 py-2", selected && "bg-primary-tint")}
    >
      <span className={cx("grid size-7 shrink-0 place-items-center rounded-ui-sm bg-muted", selected ? "text-primary" : "text-muted-foreground")}>
        <EntryIcon icon={e.icon} title={e.title} />
      </span>
      <span className="min-w-0 flex-1 truncate text-sm font-medium">{e.title}</span>
      {aside && <span className="max-w-[45%] shrink-0 truncate text-xs text-muted-foreground">{aside}</span>}
      <span aria-hidden className={cx("flex text-primary", !selected && "invisible")}>
        <Icon name="corner-down-left" size="sm" />
      </span>
    </div>
  );
}

function EntryIcon({ icon, title }: { icon: Entry["icon"]; title: string }) {
  if (icon === undefined) return <span aria-hidden className="text-xs font-bold">{title.charAt(0).toUpperCase()}</span>;
  if (typeof icon === "string") return <Icon name={icon} />;
  return <AppIcon name={icon.app} icon={icon.icon} url={icon.url} size={20} />;
}
