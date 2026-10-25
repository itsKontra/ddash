import { useAction, useQuery } from "@ddash/plugin";
import { CoreService, Theme } from "@ddash/proto/ddash/core/core_pb";
import { cx, Icon, PageHeader, Switch, type IconName } from "@ddash/ui";
import { Link, Route, Routes } from "react-router";
import { themeName, useScheme } from "./appearance";
import { AuditLog } from "./audit-log";
import { ErrorFrame } from "./boundary";

/** /settings/*: the core's settings and the Pages under them. */
export function Settings() {
  return (
    <Routes>
      <Route index element={<Overview />} />
      <Route path="audit-log" element={<AuditLog />} />
      <Route path="*" element={<PageHeader title="Not found" subtitle="Settings has no Page here." />} />
    </Routes>
  );
}

function Overview() {
  return (
    <div>
      <PageHeader title="Settings" />
      <Appearance />
      <ul className="divide-y divide-border rounded-ui border border-border bg-card">
        <Entry to="audit-log" icon="scroll-text" title="Audit Log" description="Every Action ddash took, kept for a year." />
      </ul>
    </div>
  );
}

/**
 * The Theme, a core setting saved like any config, and the light/dark
 * switch, remembered in this browser only.
 */
function Appearance() {
  const form = useQuery(CoreService.method.getConfigForm, { owner: "core" });
  const settings = useQuery(CoreService.method.getCoreSettings, {});
  const save = useAction(CoreService.method.saveConfig);
  const [scheme, setScheme] = useScheme();
  const themes = form.data?.fields.find((f) => f.name === "theme")?.options ?? [];
  const current = Theme[settings.data?.settings?.theme ?? Theme.DDASH];
  return (
    <section aria-labelledby="appearance" className="mb-6 rounded-ui border border-border bg-card p-4">
      <h2 id="appearance" className="mb-3 font-medium">
        Appearance
      </h2>
      <div role="radiogroup" aria-label="Theme" className="grid grid-cols-2 gap-3 sm:grid-cols-3">
        {themes.map((t) => (
          <button
            key={t.value}
            type="button"
            role="radio"
            aria-checked={t.value === `THEME_${current}`}
            data-theme={themeName(Theme[t.value.replace(/^THEME_/, "") as keyof typeof Theme])}
            disabled={save.pending}
            onClick={() => save.run({ owner: "core", values: { theme: t.value } })}
            className={cx(
              "cursor-pointer rounded-ui border-2 bg-background p-3 text-left text-foreground",
              t.value === `THEME_${current}` ? "border-primary" : "border-border",
            )}
          >
            <span className="mb-2 block font-semibold">{t.label}</span>
            <span aria-hidden className="flex gap-1">
              <span className="h-4 w-4 rounded-full bg-primary" />
              <span className="h-4 w-4 rounded-full bg-up" />
              <span className="h-4 w-4 rounded-full bg-warn" />
              <span className="h-4 w-4 rounded-full bg-down" />
              <span className="h-4 w-4 rounded-full bg-maint" />
            </span>
          </button>
        ))}
      </div>
      <label className="mt-4 flex items-center gap-3 text-sm">
        <Switch label="Dark" checked={scheme === "dark"} onChange={(dark) => setScheme(dark ? "dark" : "light")} />
        Dark, in this browser
      </label>
      {save.error && (
        <div className="mt-3">
          <ErrorFrame title="Saving the Theme failed" error={save.error} />
        </div>
      )}
    </section>
  );
}

function Entry({ to, icon, title, description }: { to: string; icon: IconName; title: string; description: string }) {
  return (
    <li>
      <Link to={to} className="flex items-center gap-3 px-4 py-3 hover:bg-muted">
        <span className="flex text-muted-foreground">
          <Icon name={icon} size="lg" />
        </span>
        <span>
          <span className="block font-medium">{title}</span>
          <span className="block text-sm text-muted-foreground">{description}</span>
        </span>
        <span className="ml-auto flex text-muted-foreground">
          <Icon name="chevron-right" />
        </span>
      </Link>
    </li>
  );
}
