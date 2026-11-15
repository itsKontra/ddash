import { PluginScope, type Concern, type ConcernAction, type ConcernTone, type Fetch } from "@ddash/plugin";
import { Button, cx, Icon, Panel, Pill, Presence, Skeleton, type IconName } from "@ddash/ui";
import { useCallback, useEffect, useRef, useState, type ReactNode, type RefObject } from "react";
import { Link } from "react-router";
import { ErrorBoundary } from "./boundary";
import { needs, offOnPurpose, type Listed } from "./concerns";
import type { LoadedPlugin, Plugins } from "./plugins";

/** The Sheet a Concern's Action has open: whose, which Concern, which Action, and its Sheet as clicked. */
interface Open {
  plugin: string;
  id: string;
  label: string;
  sheet: (close: () => void) => ReactNode;
}

/**
 * "Needs you": every installed Plugin's Concerns in one list, worst first,
 * each with its Actions; the quiet ones folded under "Off on purpose". Each
 * Plugin's useConcerns runs in its own scope behind an error boundary,
 * placed Widget or not, and a crash is a warn Concern of its own.
 */
export function NeedsYou({ plugins, fetch }: { plugins: Plugins; fetch?: Fetch }) {
  const [reports, setReports] = useState<ReadonlyMap<string, readonly Concern[]>>(new Map());
  const [open, setOpen] = useState<Open | null>(null);
  // Each Plugin's Concerns as last rendered, so a button runs the newest Action.
  const live = useRef(new Map<string, readonly Concern[]>());
  const onReport = useCallback((plugin: string, concerns: readonly Concern[] | undefined) => {
    setReports((prev) => {
      const next = new Map(prev);
      if (concerns) next.set(plugin, concerns);
      else next.delete(plugin);
      return next;
    });
  }, []);
  const close = useCallback(() => setOpen(null), []);

  const installed = plugins.installed ?? [];
  const sources = installed.flatMap((p) => {
    const loaded = plugins.loaded.get(p.name);
    return loaded?.definition?.useConcerns ? [loaded] : [];
  });
  // Still to say: Plugins whose bundle is loading, and those that haven't reported yet.
  const waiting = !plugins.installed || installed.some((p) => !plugins.loaded.has(p.name)) || sources.some((p) => !reports.has(p.name));
  const n = needs(sources.flatMap((p) => (reports.has(p.name) ? [{ plugin: p.name, concerns: reports.get(p.name)! }] : [])));

  const act = (l: Listed, a: ConcernAction) => {
    if ("sheet" in a) {
      setOpen({ plugin: l.plugin, id: l.concern.id, label: a.label, sheet: a.sheet });
      return;
    }
    const now = live.current.get(l.plugin)?.find((c) => c.id === l.concern.id)?.actions?.find((x) => x.label === a.label);
    if (now && "run" in now) void now.run();
  };

  return (
    <>
      {sources.map((p) => (
        <PluginScope key={p.name} plugin={p} fetch={fetch}>
          <ErrorBoundary fallback={(error) => <Failed plugin={p} error={error} onReport={onReport} />}>
            <Source plugin={p} live={live} open={open?.plugin === p.name ? open : null} onReport={onReport} onClose={close} />
          </ErrorBoundary>
        </PluginScope>
      ))}
      <Panel
        flush
        title={
          <span className="flex items-center gap-2">
            <Icon name="bell" />
            Needs you
            {n.tone && <Pill tone={n.tone}>{n.loud.length}</Pill>}
          </span>
        }
      >
        {n.loud.length === 0 && waiting ? (
          <div className="p-5">
            <Skeleton lines={2} />
          </div>
        ) : (
          <>
            {n.loud.length > 0 ? (
              <ul aria-label="Needs you">
                {n.loud.map((l) => (
                  <Row key={`${l.plugin}:${l.concern.id}`} listed={l} onAct={act} />
                ))}
              </ul>
            ) : (
              <div className="flex items-center gap-3 px-5 py-4">
                <Icon name="check" tone="up" />
                <div className="text-sm font-semibold">Nothing needs you.</div>
              </div>
            )}
            {n.quiet.length > 0 && <OffOnPurpose quiet={n.quiet} onAct={act} />}
          </>
        )}
      </Panel>
    </>
  );
}

/** The quiet Concerns behind one folded line: "Off on purpose · 4 muted — backup, nvr, …". */
function OffOnPurpose({ quiet, onAct }: { quiet: Listed[]; onAct: (l: Listed, a: ConcernAction) => void }) {
  return (
    <details className="group border-t border-border">
      <summary className="flex cursor-pointer list-none items-center gap-2 px-5 py-3 text-xs font-semibold text-muted-foreground hover:text-foreground [&::-webkit-details-marker]:hidden">
        <span className="flex transition-transform group-open:rotate-90">
          <Icon name="chevron-right" size="sm" />
        </span>
        <Icon name="bell-off" size="sm" />
        <span className="shrink-0">Off on purpose · {offOnPurpose(quiet)}</span>
        <span className="hidden min-w-0 truncate font-normal sm:inline">— {quiet.map((l) => l.concern.subject).join(", ")}</span>
      </summary>
      <ul aria-label="Off on purpose" className="border-t border-border">
        {quiet.map((l) => (
          <Row key={`${l.plugin}:${l.concern.id}`} listed={l} onAct={onAct} />
        ))}
      </ul>
    </details>
  );
}

const mark: Record<ConcernTone, string> = { down: "bg-down", warn: "bg-warn", info: "bg-info" };

const icons: Record<ConcernTone, IconName> = { down: "circle-x", warn: "triangle-exclamation-point", info: "info" };

/**
 * One Concern: its tone's mark and icon, title (a link to its detail),
 * cause, then its Actions with the main one last, filled when it is a fix.
 * Without Actions, a Details link. A quiet one is drawn without its tone.
 */
function Row({ listed: l, onAct }: { listed: Listed; onAct: (l: Listed, a: ConcernAction) => void }) {
  const c = l.concern;
  const quiet = !!c.quiet;
  const [main, ...others] = c.actions ?? [];
  const errors = (c.actions ?? []).flatMap((a) => a.error ?? []);
  const button = (a: ConcernAction, variant: "default" | "outline" | "ghost") => (
    <Button key={a.label} size="sm" variant={variant} icon={a.icon} disabled={a.pending} onClick={() => onAct(l, a)}>
      {a.label}
    </Button>
  );
  // On a phone the Actions wrap under the title rather than squeeze it.
  return (
    <li aria-label={c.title} className="relative flex flex-wrap items-center gap-x-3 gap-y-2 border-t border-border px-5 py-3 first:border-t-0">
      {!quiet && <span aria-hidden className={cx("absolute inset-y-2.5 left-0 w-1 rounded-r-full", mark[c.tone])} />}
      <div className="flex min-w-48 flex-1 items-center gap-3">
        <Icon name={c.icon ?? (quiet ? "bell-off" : icons[c.tone])} tone={quiet ? "idle" : c.tone} />
        <div className="min-w-0 flex-1">
          <div className="truncate text-sm font-semibold">{c.href && main ? <Link to={c.href}>{c.title}</Link> : c.title}</div>
          {c.cause && <div className="text-xs text-muted-foreground">{c.cause}</div>}
          {errors.map((e) => (
            <div key={e} className="text-xs text-down-text">
              {e}
            </div>
          ))}
        </div>
      </div>
      <div className="ml-auto flex shrink-0 items-center gap-1.5">
        {others.map((a) => button(a, "ghost"))}
        {main
          ? button(main, quiet || main.secondary ? "outline" : "default")
          : c.href && (
              <Link to={c.href} className="inline-flex h-7 items-center rounded-ui px-2.5 text-xs font-medium hover:bg-muted">
                Details
              </Link>
            )}
      </div>
    </li>
  );
}

// What a report says, functions aside: a new one is only passed up when it changed.
const signature = (concerns: readonly Concern[] | undefined) => JSON.stringify(concerns, (_, v: unknown) => (typeof v === "function" ? "ƒ" : v));

/**
 * Runs one Plugin's useConcerns, in its scope: passes each changed report
 * up, keeps the newest in live, and draws the Sheet of the Action open on
 * one of its Concerns, which stays open when the Concern goes away.
 */
function Source({
  plugin,
  live,
  open,
  onReport,
  onClose,
}: {
  plugin: LoadedPlugin;
  live: RefObject<Map<string, readonly Concern[]>>;
  open: Open | null;
  onReport: (plugin: string, concerns: readonly Concern[] | undefined) => void;
  onClose: () => void;
}) {
  const name = plugin.name;
  const concerns = plugin.definition!.useConcerns!();
  const sig = signature(concerns);
  useEffect(() => {
    if (concerns) live.current.set(name, concerns);
    else live.current.delete(name);
  });
  useEffect(() => {
    onReport(name, live.current.get(name));
  }, [name, sig, live, onReport]);
  useEffect(() => () => onReport(name, undefined), [name, onReport]);

  // The Concern's newest Sheet while it lasts, then the one clicked.
  const found = open && concerns?.find((c) => c.id === open.id)?.actions?.find((a) => a.label === open.label);
  const sheet = open && (found && "sheet" in found ? found.sheet : open.sheet);
  return <Presence>{sheet ? sheet(onClose) : null}</Presence>;
}

/** A Plugin whose useConcerns threw: one warn Concern saying so. */
function Failed({ plugin, error, onReport }: { plugin: LoadedPlugin; error: Error; onReport: (plugin: string, concerns: readonly Concern[] | undefined) => void }) {
  const { name, title } = plugin;
  const message = error.message;
  useEffect(() => {
    onReport(name, [{ id: "failed", tone: "warn", subject: title, title: `${title} can't say what needs you`, cause: message, icon: "bug" }]);
    return () => onReport(name, undefined);
  }, [name, title, message, onReport]);
  return null;
}
