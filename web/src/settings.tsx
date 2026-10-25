import { useAction, useQuery } from "@ddash/plugin";
import { CoreService, Theme } from "@ddash/proto/ddash/core/core_pb";
import { cx, Icon, Notice, PageTitle, Panel, Segmented, Skeleton, type IconName } from "@ddash/ui";
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

function Overview() {
  return (
    <div>
      <PageTitle kicker="System" title="Settings" subtitle="The core's own settings. Each Component's config lives with its Plugin, under Plugins." />
      <div className="flex max-w-4xl flex-col gap-6">
        <Appearance />
        <Panel flush title="More settings">
          <ul className="divide-y divide-border">
            <Entry to="audit-log" icon="scroll-text" title="Audit Log" description="Every Action ddash took, kept for a year." />
            <Entry to="/plugins" icon="puzzle" title="Plugins" description="Install Plugins and configure the Components they run." />
          </ul>
        </Panel>
      </div>
    </div>
  );
}

const schemes = ["Light", "Dark"] as const;

/**
 * The Theme, a core setting saved like any config, and the light/dark
 * scheme, remembered in this browser only.
 */
function Appearance() {
  const form = useQuery(CoreService.method.getConfigForm, { owner: "core" });
  const settings = useQuery(CoreService.method.getCoreSettings, {});
  const save = useAction(CoreService.method.saveConfig);
  const [scheme, setScheme] = useScheme();
  const themes = form.data?.fields.find((f) => f.name === "theme")?.options ?? [];
  const current = `THEME_${Theme[settings.data?.settings?.theme ?? Theme.DDASH]}`;
  return (
    <Panel title="Appearance" description="The Theme applies to every Operator.">
      {form.data ? (
        <div role="radiogroup" aria-label="Theme" className="grid grid-cols-1 gap-3 sm:grid-cols-3">
          {themes.map((t) => {
            const on = t.value === current;
            return (
              <button
                key={t.value}
                type="button"
                role="radio"
                aria-checked={on}
                data-theme={themeName(Theme[t.value.replace(/^THEME_/, "") as keyof typeof Theme])}
                disabled={save.pending}
                onClick={() => save.run({ owner: "core", values: { theme: t.value } })}
                className={cx(
                  "cursor-pointer rounded-ui border-2 bg-background p-4 text-left text-foreground transition-colors",
                  on ? "border-primary" : "border-border hover:border-muted-foreground",
                )}
              >
                <span className="mb-3 flex items-center gap-2 text-sm font-extrabold">
                  {t.label}
                  {on && <span className="ml-auto text-[10px] font-extrabold tracking-[0.14em] text-primary uppercase">in use</span>}
                </span>
                <span aria-hidden className="flex gap-1.5">
                  <span className="h-5 w-5 rounded-ui-sm bg-nav" />
                  <span className="h-5 w-5 rounded-ui-sm bg-primary" />
                  <span className="h-5 w-5 rounded-ui-sm bg-up" />
                  <span className="h-5 w-5 rounded-ui-sm bg-warn" />
                  <span className="h-5 w-5 rounded-ui-sm bg-down" />
                  <span className="h-5 w-5 rounded-ui-sm bg-maint" />
                </span>
              </button>
            );
          })}
        </div>
      ) : form.error ? (
        <Notice tone="down" title="The Themes can't be read">
          {form.error.rawMessage}
        </Notice>
      ) : (
        <Skeleton height="lg" />
      )}
      <div className="mt-6 flex flex-wrap items-center gap-x-4 gap-y-2 border-t border-border pt-5">
        <div className="min-w-0 flex-1">
          <div className="text-sm font-bold">Scheme</div>
          <div className="text-xs text-muted-foreground">Remembered in this browser only.</div>
        </div>
        <Segmented
          label="Scheme"
          value={scheme === "dark" ? "Dark" : "Light"}
          options={schemes}
          onChange={(s) => setScheme(s === "Dark" ? "dark" : "light")}
        />
      </div>
      {save.error && (
        <div className="mt-4">
          <Notice tone="down" title="Saving the Theme failed">
            {save.error.message}
          </Notice>
        </div>
      )}
    </Panel>
  );
}

/** A link row: an icon chip, the title over its description, and a chevron. */
function Entry({ to, icon, title, description }: { to: string; icon: IconName; title: string; description: string }) {
  return (
    <li>
      <Link to={to} className="flex items-center gap-3.5 px-5 py-4 transition-colors hover:bg-muted">
        <span className="grid h-9 w-9 shrink-0 place-items-center rounded-ui-sm bg-primary-tint text-primary">
          <Icon name={icon} />
        </span>
        <span className="min-w-0">
          <span className="block text-sm font-bold">{title}</span>
          <span className="block text-xs text-muted-foreground">{description}</span>
        </span>
        <span className="ml-auto flex shrink-0 text-muted-foreground">
          <Icon name="chevron-right" />
        </span>
      </Link>
    </li>
  );
}
