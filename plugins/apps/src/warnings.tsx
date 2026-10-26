import { useAction } from "@ddash/plugin";
import { AppsService, FixKind, type App, type Fix, type Warning } from "@ddash/proto/apps/apps_pb";
import { Button, cx, Notice, Pill, StatusDot } from "@ddash/ui";
import { fixes, fixLabel, warningText } from "./apps";

/**
 * How the pane acts on a fix it can't run itself: the Panels' Edits, the
 * Reattach Dialog, Delete's Plan Dialog, and a Plan Dialog for each other
 * external fix. A fix without a handler shows disabled.
 */
export interface FixHandlers {
  /** Opens the Workload and Check Panel's Edit. */
  editWorkload?: () => void;
  /** Opens the Reattach Dialog. */
  reattach?: () => void;
  /** Opens the Reverse proxy Panel's Edit, to pick a certificate. */
  editProxy?: () => void;
  /** Opens the Reverse proxy Panel's Add reverse proxy. */
  addProxy?: () => void;
  /** Opens Delete's Plan Dialog. */
  remove?: () => void;
  /** Runs any other external fix through its Plan Dialog. */
  external?: (w: Warning, f: Fix) => void;
}

/** The Warning boxes at the top of an App's pane: the Warnings that count, and Ignored ones with showIgnored. */
export function WarningBoxes({ app, apps, showIgnored, handlers, onGone }: { app: App; apps: readonly App[]; showIgnored: boolean; handlers: FixHandlers; onGone: () => void }) {
  const shown = app.warnings.filter((w) => showIgnored || !w.ignored);
  if (shown.length === 0) return null;
  return (
    <div className="flex flex-col gap-3">
      {shown.map((w) => (
        <WarningBox key={w.kind} app={app} apps={apps} warning={w} handlers={handlers} onGone={onGone} />
      ))}
    </div>
  );
}

function WarningBox({ app, apps, warning: w, handlers, onGone }: { app: App; apps: readonly App[]; warning: Warning; handlers: FixHandlers; onGone: () => void }) {
  const ignore = useAction(AppsService.method.ignoreWarning);
  const unignore = useAction(AppsService.method.unignoreWarning);
  const promote = useAction(AppsService.method.promoteAlias);
  const merge = useAction(AppsService.method.merge);
  const remove = useAction(AppsService.method.deleteRecord);
  const title = warningText[w.kind];
  const error = ignore.error ?? unignore.error ?? promote.error ?? merge.error ?? remove.error;

  const run = (f: Fix): (() => void) | undefined => {
    const fix = fixes[f.kind];
    if (fix.place) return handlers[fix.place];
    switch (f.kind) {
      case FixKind.USE_AS_URL:
        return () => void promote.run({ id: app.id, alias: f.hostname });
      case FixKind.MERGE: {
        const other = apps.find((a) => a.id === f.appId)?.name ?? f.appId;
        return () =>
          void merge.run(
            { id: app.id, from: f.appId },
            { confirm: { text: `Merge ${other} into ${app.name}? ${app.name} keeps its id, settings and Check history and gains ${other}'s sources; ${other}'s record and Check go.`, action: "Merge" } },
          );
      }
      case FixKind.DELETE_ORPHAN:
        return async () => {
          if (await remove.run({ id: app.id }, { confirm: { text: `Delete the settings kept for ${app.name}?`, action: "Delete" } })) onGone();
        };
      default:
        return fix.ddashOnly || !handlers.external ? undefined : () => handlers.external!(w, f);
    }
  };

  return (
    <section aria-label={title} className={cx("flex flex-col gap-2 rounded-ui border p-3", w.ignored ? "border-border" : "border-warn bg-warn-tint")}>
      <div className="flex items-center gap-2 text-sm font-semibold">
        <StatusDot tone={w.ignored ? "idle" : "warn"} label={w.ignored ? "Ignored" : "Warning"} />
        <h3 className="flex-1">{title}</h3>
        {w.ignored && <Pill>Ignored</Pill>}
      </div>
      {w.hostnames.length > 0 && <p className="font-mono text-xs break-all">{w.hostnames.join(", ")}</p>}
      {w.detail && <p className="text-xs break-words">{w.detail}</p>}
      <div className="flex flex-wrap gap-2">
        {w.fixes.map((f, i) => {
          const act = run(f);
          return (
            <Button
              key={i}
              size="sm"
              variant={fixes[f.kind].removes ? "danger" : "outline"}
              disabled={!act}
              onClick={act}
            >
              {fixLabel(f, apps)}
            </Button>
          );
        })}
        <Button
          size="sm"
          variant="ghost"
          icon={w.ignored ? "eye" : "eye-off"}
          disabled={ignore.pending || unignore.pending}
          onClick={() => void (w.ignored ? unignore : ignore).run({ id: app.id, kind: w.kind })}
        >
          {w.ignored ? "Un-ignore" : "Ignore"}
        </Button>
      </div>
      {error && <Notice tone="down">{error.rawMessage}</Notice>}
    </section>
  );
}
