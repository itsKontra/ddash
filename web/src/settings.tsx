import { timestampDate } from "@bufbuild/protobuf/wkt";
import { useAction, useQuery } from "@ddash/plugin";
import { CoreService, Theme } from "@ddash/proto/ddash/core/core_pb";
import { cx, Notice, PageTitle, Panel, Segmented, StatusList, useVariant } from "@ddash/ui";
import { ChevronRight, Moon, Puzzle, ScrollText, Sun, type LucideIcon } from "lucide-react";
import { Link, Route, Routes } from "react-router";
import { themeName, useScheme } from "./appearance";
import { AuditLog } from "./audit-log";

/** /settings/*: the core's settings and the Pages under them. */
export function Settings() {
  return (
    <Routes>
      <Route index element={<Overview />} />
      <Route path="audit-log" element={<AuditLog />} />
      <Route path="*" element={<PageTitle title="Not found" subtitle="Settings has no Page here." />} />
    </Routes>
  );
}

/**
 * PROTOTYPE: ?variant=1 stacked Panels (Theme swatches, a scheme switch,
 * links); ?variant=2 a live preview per Theme and scheme, picked in one
 * click, beside the latest Actions.
 */
function Overview() {
  const v = useVariant();
  return (
    <div>
      <PageTitle kicker="System" title="Settings" subtitle="The core's own settings. Each Component's config lives with its Plugin, under Plugins." />
      {v === "2" ? (
        <div className="grid grid-cols-1 gap-6 xl:grid-cols-[minmax(0,1.6fr)_minmax(0,1fr)]">
          <Appearance previews />
          <div className="flex flex-col gap-6">
            <RecentActions />
            <Panel kicker="Elsewhere" title="More settings">
              <Links />
            </Panel>
          </div>
        </div>
      ) : (
        <div className="flex max-w-4xl flex-col gap-6">
          <Appearance />
          <Panel flush kicker="Records and more" title="More settings">
            <Links />
          </Panel>
        </div>
      )}
    </div>
  );
}

function Links() {
  return (
    <ul className="divide-y divide-border">
      <Entry to="audit-log" icon={ScrollText} title="Audit Log" description="Every Action ddash took, kept for a year." />
      <Entry to="/plugins" icon={Puzzle} title="Plugins and their Components" description="Install Plugins and configure the Components they run." />
    </ul>
  );
}

/**
 * The Theme, a core setting saved like any config, and the light/dark
 * switch, remembered in this browser only.
 */
function Appearance({ previews }: { previews?: boolean }) {
  const form = useQuery(CoreService.method.getConfigForm, { owner: "core" });
  const settings = useQuery(CoreService.method.getCoreSettings, {});
  const save = useAction(CoreService.method.saveConfig);
  const [scheme, setScheme] = useScheme();
  const themes = form.data?.fields.find((f) => f.name === "theme")?.options ?? [];
  const current = Theme[settings.data?.settings?.theme ?? Theme.DDASH];
  const pick = (value: string) => value !== `THEME_${current}` && save.run({ owner: "core", values: { theme: value } });
  const error = save.error && <Notice tone="down" title="Saving the Theme failed">{save.error.rawMessage}</Notice>;

  if (previews) {
    return (
      <Panel kicker="Appearance" title="Theme" description="Pick a Theme and a scheme in one go. The Theme applies to every Operator; light or dark is remembered in this browser only.">
        <div role="radiogroup" aria-label="Theme and scheme" className="flex flex-col gap-5">
          {themes.map((t) => {
            const name = themeName(Theme[t.value.replace(/^THEME_/, "") as keyof typeof Theme]);
            const on = t.value === `THEME_${current}`;
            return (
              <div key={t.value}>
                <div className="mb-2 flex items-center gap-2 text-sm font-extrabold">
                  {t.label}
                  {on && <span className="text-[10px] font-extrabold tracking-[0.14em] text-primary uppercase">in use</span>}
                </div>
                <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
                  {(["light", "dark"] as const).map((s) => (
                    <button
                      key={s}
                      type="button"
                      role="radio"
                      aria-checked={on && scheme === s}
                      aria-label={`${t.label}, ${s}`}
                      disabled={save.pending}
                      onClick={() => {
                        pick(t.value);
                        setScheme(s);
                      }}
                      className={cx(
                        "cursor-pointer overflow-hidden rounded-xl text-left outline-offset-2 transition-shadow",
                        on && scheme === s ? "outline-2 outline-primary" : "outline-1 outline-border hover:shadow-panel",
                      )}
                    >
                      <ShellPreview theme={name} scheme={s} />
                      <span className="flex items-center gap-2 border-t border-border bg-card px-3 py-2 text-xs font-bold">
                        {s === "light" ? <Sun className="h-3.5 w-3.5" aria-hidden /> : <Moon className="h-3.5 w-3.5" aria-hidden />}
                        {s === "light" ? "Light" : "Dark"}
                        {on && scheme === s && <span className="ml-auto text-primary">Selected</span>}
                      </span>
                    </button>
                  ))}
                </div>
              </div>
            );
          })}
          {error}
        </div>
      </Panel>
    );
  }

  return (
    <Panel kicker="Appearance" title="Theme" description="The Theme applies to every Operator.">
      <div role="radiogroup" aria-label="Theme" className="grid grid-cols-1 gap-3 sm:grid-cols-3">
        {themes.map((t) => {
          const on = t.value === `THEME_${current}`;
          return (
            <button
              key={t.value}
              type="button"
              role="radio"
              aria-checked={on}
              data-theme={themeName(Theme[t.value.replace(/^THEME_/, "") as keyof typeof Theme])}
              disabled={save.pending}
              onClick={() => pick(t.value)}
              className={cx("cursor-pointer rounded-xl border-2 bg-background p-4 text-left text-foreground transition-colors", on ? "border-primary" : "border-border hover:border-foreground/25")}
            >
              <span className="mb-3 flex items-center gap-2 text-sm font-extrabold">
                {t.label}
                {on && <span className="ml-auto text-[10px] font-extrabold tracking-[0.14em] text-primary uppercase">in use</span>}
              </span>
              <span aria-hidden className="flex gap-1.5">
                <span className="h-5 w-5 rounded-md bg-nav" />
                <span className="h-5 w-5 rounded-md bg-primary" />
                <span className="h-5 w-5 rounded-md bg-up" />
                <span className="h-5 w-5 rounded-md bg-warn" />
                <span className="h-5 w-5 rounded-md bg-down" />
                <span className="h-5 w-5 rounded-md bg-maint" />
              </span>
            </button>
          );
        })}
      </div>
      <div className="mt-6 flex flex-wrap items-center gap-x-4 gap-y-2 border-t border-border pt-5">
        <div className="min-w-0 flex-1">
          <div className="text-sm font-bold">Scheme</div>
          <div className="text-xs text-muted-foreground">Remembered in this browser only.</div>
        </div>
        <Segmented label="Scheme" value={scheme === "dark" ? "Dark" : "Light"} options={["Light", "Dark"]} onChange={(s) => setScheme(s === "Dark" ? "dark" : "light")} />
      </div>
      {error && <div className="mt-4">{error}</div>}
    </Panel>
  );
}

/** A miniature of the shell in one Theme and scheme. */
function ShellPreview({ theme, scheme }: { theme: string; scheme: "light" | "dark" }) {
  return (
    <span aria-hidden data-theme={theme} className={cx("flex h-32 bg-background", `scheme-${scheme}`)}>
      <span className="flex w-12 shrink-0 flex-col gap-1.5 bg-nav p-2">
        <span className="mb-1 h-3.5 w-3.5 rounded bg-linear-to-br from-nav-accent to-primary" />
        <span className="h-1.5 rounded-full bg-nav-active shadow-[inset_2px_0_var(--nav-accent)]" />
        <span className="h-1.5 w-3/4 rounded-full bg-nav-foreground/30" />
        <span className="h-1.5 w-2/3 rounded-full bg-nav-foreground/30" />
        <span className="h-1.5 w-3/4 rounded-full bg-nav-foreground/30" />
      </span>
      <span className="flex min-w-0 flex-1 flex-col gap-2 p-2.5">
        <span className="flex h-10 items-center justify-between rounded-md bg-linear-to-br from-hero-from to-hero-to px-2.5">
          <span className="flex flex-col gap-1">
            <span className="h-1 w-6 rounded-full bg-hero-accent" />
            <span className="h-1.5 w-14 rounded-full bg-hero-foreground/80" />
          </span>
          <span className="h-6 w-6 rounded-full border-3 border-hero-accent border-r-hero-foreground/15" />
        </span>
        <span className="grid grid-cols-3 gap-1.5">
          {(["up", "warn", "down"] as const).map((t) => (
            <span key={t} className="flex flex-col gap-1 rounded-md border border-border bg-card p-1.5 shadow-panel">
              <span className={cx("h-2 w-2 rounded-sm", { up: "bg-up", warn: "bg-warn", down: "bg-down" }[t])} />
              <span className="h-1.5 w-5 rounded-full bg-foreground/70" />
            </span>
          ))}
        </span>
        <span className="flex flex-1 flex-col justify-center gap-1 rounded-md border border-border bg-card px-1.5">
          <span className="h-1 w-full rounded-full bg-muted" />
          <span className="h-1 w-4/5 rounded-full bg-muted" />
        </span>
      </span>
    </span>
  );
}

/** The latest three Actions, with a way into the Audit Log. */
function RecentActions() {
  const { data } = useQuery(CoreService.method.listAuditLog, { actorKind: "", target: "", outcome: "", pageToken: "" });
  const chains = (data?.chains ?? []).slice(0, 4);
  return (
    <Panel
      kicker="Audit Log"
      title="Latest Actions"
      actions={
        <Link to="audit-log" className="inline-flex items-center gap-1 text-xs font-bold text-primary hover:underline">
          Open <ChevronRight className="h-3.5 w-3.5" aria-hidden />
        </Link>
      }
    >
      <StatusList
        timeline
        items={chains.map((c) => {
          const e = c.entries[0]!;
          return {
            tone: e.outcome === "ok" ? "up" : e.outcome === "" ? "idle" : e.outcome === "interrupted" ? "warn" : "down",
            title: `${e.target} · ${e.method}`,
            meta: e.username ? `${e.actorKind}:${e.username}` : e.actorKind,
            aside: e.time && timestampDate(e.time).toLocaleTimeString(undefined, { hour: "2-digit", minute: "2-digit" }),
          };
        })}
      />
    </Panel>
  );
}

function Entry({ to, icon: Icon, title, description }: { to: string; icon: LucideIcon; title: string; description: string }) {
  return (
    <li>
      <Link to={to} className="flex items-center gap-3.5 px-5 py-4 transition-colors hover:bg-primary/5">
        <span className="grid h-9 w-9 shrink-0 place-items-center rounded-lg bg-primary/10 text-primary">
          <Icon className="h-4.5 w-4.5" aria-hidden />
        </span>
        <span className="min-w-0">
          <span className="block text-sm font-bold">{title}</span>
          <span className="block text-xs text-muted-foreground">{description}</span>
        </span>
        <ChevronRight className="ml-auto h-4 w-4 shrink-0 text-muted-foreground" aria-hidden />
      </Link>
    </li>
  );
}
