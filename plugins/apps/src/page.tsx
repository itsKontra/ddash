import { useAction } from "@ddash/plugin";
import { AppsService, type App, type Overlay } from "@ddash/proto/apps/apps_pb";
import { AppIcon, Button, PageHeader } from "@ddash/ui";
import { useId, useState, type ReactNode } from "react";
import { sections, useApps } from "./apps";
import { EditSheet } from "./edit";

/** The Apps Page: App cards by group, display settings, manual Apps and unmatched settings. */
export function AppsPage() {
  const { list, error } = useApps();
  // The App being edited, "new" while adding one.
  const [editing, setEditing] = useState<App | "new">();
  const apps = list?.apps ?? [];
  const hidden = apps.filter((a) => a.hidden);
  const groups = [...new Set(apps.map((a) => a.group).filter(Boolean))].sort();

  return (
    <div>
      <PageHeader
        title="Apps"
        subtitle="Every nginx Proxy Host, plus the Apps you add."
        actions={
          <Button size="sm" onClick={() => setEditing("new")}>
            Add App
          </Button>
        }
      />
      {!list && error && <p className="text-sm text-muted-foreground">apps unavailable: {error.rawMessage}</p>}
      {list?.discoveryError && (
        <p role="status" className="mb-4 rounded-ui border border-border bg-muted p-3 text-sm">
          Couldn't read the Proxy Hosts from nginx-config: {list.discoveryError}. Showing those of the last read.
        </p>
      )}
      {list && apps.length === 0 && <p className="text-sm text-muted-foreground">No Apps yet. Proxy Hosts show here once nginx-config reads them.</p>}
      <div className="flex flex-col gap-6">
        {sections(apps).map((s) => (
          <Section key={s.title} title={s.title}>
            <Cards apps={s.apps} label={s.title} onEdit={setEditing} />
          </Section>
        ))}
        {hidden.length > 0 && (
          <Section title="Hidden">
            <Cards apps={hidden} label="Hidden" onEdit={setEditing} />
          </Section>
        )}
        {list && list.unmatched.length > 0 && (
          <Section title="Unmatched settings">
            <p className="mb-3 text-sm text-muted-foreground">
              Settings kept for Proxy Hosts that are gone or were renamed. Re-attach them to an App, or delete them.
            </p>
            <ul aria-label="Unmatched settings" className="flex flex-col gap-2">
              {list.unmatched.map((o) => (
                <Unmatched key={o.serverName} overlay={o} targets={apps.filter((a) => !a.manual && !a.overlay)} />
              ))}
            </ul>
          </Section>
        )}
      </div>
      {editing && <EditSheet app={editing === "new" ? undefined : editing} groups={groups} onClose={() => setEditing(undefined)} />}
    </div>
  );
}

function Section({ title, children }: { title: string; children: ReactNode }) {
  const id = useId();
  return (
    <section aria-labelledby={id}>
      <h2 id={id} className="mb-3 font-semibold">
        {title}
      </h2>
      {children}
    </section>
  );
}

function Cards({ apps, label, onEdit }: { apps: readonly App[]; label: string; onEdit: (a: App) => void }) {
  return (
    <ul aria-label={label} className="grid grid-cols-1 gap-3 sm:grid-cols-2 xl:grid-cols-3">
      {apps.map((a) => (
        <Card key={a.id} app={a} onEdit={() => onEdit(a)} />
      ))}
    </ul>
  );
}

/** An App's card: icon, name and URL opening it, and its aliases. */
function Card({ app, onEdit }: { app: App; onEdit: () => void }) {
  return (
    <li aria-label={app.name} className="flex flex-col gap-2 rounded-ui border border-border bg-card p-3">
      <div className="flex items-center gap-2">
        <a href={app.url} target="_blank" rel="noreferrer" className="flex min-w-0 flex-1 items-center gap-3 rounded-ui hover:bg-muted">
          <AppIcon name={app.name} icon={app.icon} url={app.url} />
          <span className="flex min-w-0 flex-col">
            <span className="truncate font-medium">{app.name}</span>
            <span className="truncate text-xs text-muted-foreground">{app.url}</span>
          </span>
        </a>
        <Button variant="ghost" size="sm" aria-label={`Edit ${app.name}`} onClick={onEdit}>
          Edit
        </Button>
      </div>
      {app.aliases.length > 0 && <p className="truncate text-xs text-muted-foreground">Also {app.aliases.join(", ")}</p>}
    </li>
  );
}

/** What an overlay sets, e.g. "Documents · group Office · hidden". */
function summary(o: Overlay) {
  return [o.name, o.icon && `icon ${o.icon}`, o.group && `group ${o.group}`, o.hidden && "hidden"].filter(Boolean).join(" · ");
}

/** Settings whose Proxy Host is gone: re-attach to an App without settings, or delete. */
function Unmatched({ overlay, targets }: { overlay: Overlay; targets: readonly App[] }) {
  const attach = useAction(AppsService.method.attachOverlay);
  const remove = useAction(AppsService.method.deleteOverlay);
  const [to, setTo] = useState("");
  const error = attach.error ?? remove.error;
  return (
    <li aria-label={overlay.serverName} className="flex flex-wrap items-center gap-2 rounded-ui border border-border bg-card p-3 text-sm">
      <span className="font-medium">{overlay.serverName}</span>
      <span className="text-muted-foreground">{summary(overlay)}</span>
      <span className="ml-auto flex items-center gap-2">
        <select aria-label="Re-attach to" className="h-7 rounded-ui border border-border bg-card px-2 text-xs" value={to} onChange={(e) => setTo(e.target.value)}>
          <option value="">Choose an App…</option>
          {targets.map((a) => (
            <option key={a.serverName} value={a.serverName}>
              {a.name} ({a.serverName})
            </option>
          ))}
        </select>
        <Button size="sm" variant="outline" disabled={!to || attach.pending} onClick={() => void attach.run({ serverName: overlay.serverName, to })}>
          Re-attach
        </Button>
        <Button
          size="sm"
          variant="danger"
          disabled={remove.pending}
          onClick={() => void remove.run({ serverName: overlay.serverName }, { confirm: { text: `Delete the settings kept for ${overlay.serverName}?`, action: "Delete" } })}
        >
          Delete
        </Button>
      </span>
      {error && (
        <p role="alert" className="w-full text-down">
          {error.rawMessage}
        </p>
      )}
    </li>
  );
}
