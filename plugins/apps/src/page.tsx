import { useAction } from "@ddash/plugin";
import { AppsService, type App, type Overlay, type Workload } from "@ddash/proto/apps/apps_pb";
import type { Check } from "@ddash/proto/checks/checks_pb";
import {
  ActionStrip,
  AppIcon,
  Button,
  DataTable,
  Dialog,
  EmptyState,
  Field,
  FormActions,
  matches,
  noMatch,
  Notice,
  PageTitle,
  Panel,
  Pill,
  Presence,
  SearchField,
  SectionHeading,
  Select,
  Skeleton,
  useQueryState,
  type Column,
} from "@ddash/ui";
import { useId, useState, type ReactNode } from "react";
import { sections, useApps } from "./apps";
import { checkStatus } from "@ddash/display";
import { useAppChecks } from "./checks";
import { EditSheet } from "./edit";
import { workloadName } from "@ddash/display";
import { WorkloadLine } from "./workload";

/** Whether an App matches the search: its name, its domains (a manual App's is its URL's host) or its group. */
const found = (q: string, app: App) => matches(q, app.name, app.serverName, ...app.aliases, URL.parse(app.url)?.hostname, app.group);

/** The Apps Page: App cards by group, display settings, manual Apps and unmatched settings. */
export function AppsPage() {
  const { list, error } = useApps();
  const { byApp, error: checksError } = useAppChecks();
  const [q, setQ] = useQueryState("q", "");
  // The App being edited, "new" while adding one.
  const [editing, setEditing] = useState<App | "new">();
  const apps = list?.apps ?? [];
  const shown = apps.filter((a) => found(q, a));
  const hidden = shown.filter((a) => a.hidden);
  const groups = [...new Set(apps.map((a) => a.group).filter(Boolean))].sort();
  const add = (
    <Button icon="plus" onClick={() => setEditing("new")}>
      Add App
    </Button>
  );
  const header = (
    <>
      {apps.length > 0 && (
        <div className="w-full sm:w-72">
          <SearchField label="Search Apps" value={q} onChange={setQ} placeholder="Search by name, domain or group" />
        </div>
      )}
      {add}
    </>
  );

  return (
    <div>
      <PageTitle kicker="Services" title="Apps" subtitle="Every nginx Proxy Host, plus the Apps you add." actions={header} />
      <div className="flex flex-col gap-6">
        {!list && !error && <Skeleton height="lg" />}
        {!list && error && <Notice tone="down">apps unavailable: {error.rawMessage}</Notice>}
        {list?.discoveryError && (
          <Notice tone="warn">Couldn't read the Proxy Hosts from nginx-config: {list.discoveryError}. Showing those of the last read.</Notice>
        )}
        {list && apps.length > 0 && checksError && <Notice tone="warn">Check status unavailable: {checksError.rawMessage}</Notice>}
        {list && apps.length === 0 && (
          <Panel>
            <EmptyState icon="layout-grid" title="No Apps yet" action={add}>
              Proxy Hosts show here once nginx-config reads them, or add an App by hand.
            </EmptyState>
          </Panel>
        )}
        {apps.length > 0 && shown.length === 0 && (
          <Panel>
            <EmptyState icon="search-x" title={noMatch("Apps", q)} />
          </Panel>
        )}
        {sections(shown).map((s) => (
          <Section key={s.title} title={s.title}>
            <Cards apps={s.apps} label={s.title} checks={byApp} onEdit={setEditing} />
          </Section>
        ))}
        {hidden.length > 0 && (
          <Section title="Hidden">
            <Cards apps={hidden} label="Hidden" checks={byApp} onEdit={setEditing} />
          </Section>
        )}
        {list && list.unmatched.length > 0 && (
          <Section title="Unmatched settings">
            <p className="mb-3 text-sm text-muted-foreground">
              Settings kept for Proxy Hosts that are gone or were renamed. Re-attach them to an App, or delete them.
            </p>
            <UnmatchedSettings overlays={list.unmatched} targets={apps.filter((a) => !a.manual && !a.overlay)} />
          </Section>
        )}
      </div>
      <Presence>{editing && <EditSheet app={editing === "new" ? undefined : editing} groups={groups} onClose={() => setEditing(undefined)} />}</Presence>
    </div>
  );
}

/** A group of cards under its heading. */
function Section({ title, children }: { title: string; children: ReactNode }) {
  return (
    <section aria-label={title}>
      <SectionHeading title={title} />
      {children}
    </section>
  );
}

function Cards({
  apps,
  label,
  checks,
  onEdit,
}: {
  apps: readonly App[];
  label: string;
  /** Each App's Check by App id; undefined while checks hasn't answered. */
  checks?: ReadonlyMap<string, Check>;
  onEdit: (a: App) => void;
}) {
  return (
    <ul aria-label={label} className="grid grid-cols-1 gap-4 sm:grid-cols-2 xl:grid-cols-3">
      {apps.map((a) => (
        <Card key={a.id} app={a} checks={checks} onEdit={() => onEdit(a)} />
      ))}
    </ul>
  );
}

/** An App's card: icon, name and URL opening it, its Check's status, its Workload and its aliases. */
function Card({ app, checks, onEdit }: { app: App; checks?: ReadonlyMap<string, Check>; onEdit: () => void }) {
  const status = checks && checkStatus(checks.get(app.id));
  return (
    <li aria-label={app.name} className="min-w-0">
      <Panel>
        <div className="flex flex-col gap-3">
          <div className="flex items-start gap-2">
            <a href={app.url} target="_blank" rel="noreferrer" className="group flex min-w-0 flex-1 items-center gap-3 rounded-ui-sm">
              <AppIcon name={app.name} icon={app.icon} url={app.url} />
              <span className="flex min-w-0 flex-col">
                <span className="truncate font-semibold text-foreground group-hover:underline">{app.name}</span>
                <span className="truncate text-xs text-muted-foreground">{app.url}</span>
              </span>
            </a>
            <Button variant="ghost" size="sm" icon="pencil" aria-label={`Edit ${app.name}`} onClick={onEdit}>
              Edit
            </Button>
          </div>
          {status && (
            <span role="status" className="flex">
              <Pill tone={status.tone} dot>
                {status.label}
              </Pill>
            </span>
          )}
          {(app.workload || app.aliases.length > 0) && (
            <div className="flex min-w-0 flex-col gap-1">
              {app.workload && <WorkloadLine workload={app.workload} />}
              {app.aliases.length > 0 && <p className="truncate text-xs text-muted-foreground">Also {app.aliases.join(", ")}</p>}
            </div>
          )}
        </div>
      </Panel>
    </li>
  );
}

/** What an overlay sets, e.g. "Documents · group Office · hidden". */
function summary(o: Overlay) {
  const workload = o.workload && `on ${workloadName({ ref: o.workload } as Workload)}`;
  return [o.name, o.icon && `icon ${o.icon}`, o.group && `group ${o.group}`, o.hidden && "hidden", workload].filter(Boolean).join(" · ");
}

const unmatchedColumns: Column<Overlay>[] = [
  { id: "host", header: "Proxy Host", rowHeader: true, mono: true, cell: (o) => o.serverName },
  { id: "settings", header: "Settings", cell: (o) => summary(o) },
];

/**
 * Settings whose Proxy Host is gone, as a compact table: each row's strip
 * re-attaches them to an App without settings, or deletes them.
 */
function UnmatchedSettings({ overlays, targets }: { overlays: readonly Overlay[]; targets: readonly App[] }) {
  const [reattaching, setReattaching] = useState<Overlay>();
  return (
    <>
      <Panel flush>
        <DataTable
          rows={overlays}
          rowKey={(o) => o.serverName}
          columns={unmatchedColumns}
          density="compact"
          rowLabel={(o) => o.serverName}
          strip={(o) => <UnmatchedStrip overlay={o} canReattach={targets.length > 0} onReattach={() => setReattaching(o)} />}
        />
      </Panel>
      <Presence>{reattaching && <Reattach overlay={reattaching} targets={targets} onClose={() => setReattaching(undefined)} />}</Presence>
    </>
  );
}

/** An unmatched setting's strip: Re-attach… opens its Dialog, Delete asks first. */
function UnmatchedStrip({ overlay, canReattach, onReattach }: { overlay: Overlay; canReattach: boolean; onReattach: () => void }) {
  const remove = useAction(AppsService.method.deleteOverlay);
  return (
    <ActionStrip summary={`Kept for ${overlay.serverName}`} notice={remove.error && <Notice tone="down">{remove.error.rawMessage}</Notice>}>
      <Button size="sm" variant="outline" disabled={!canReattach} onClick={onReattach}>
        Re-attach…
      </Button>
      <Button
        size="sm"
        variant="danger"
        disabled={remove.pending}
        onClick={() => void remove.run({ serverName: overlay.serverName }, { confirm: { text: `Delete the settings kept for ${overlay.serverName}?`, action: "Delete" } })}
      >
        Delete
      </Button>
    </ActionStrip>
  );
}

/** Re-attaches an unmatched setting to an App without settings of its own. */
function Reattach({ overlay, targets, onClose }: { overlay: Overlay; targets: readonly App[]; onClose: () => void }) {
  const attach = useAction(AppsService.method.attachOverlay);
  const [to, setTo] = useState("");
  const form = useId();
  return (
    <Dialog
      title={`Re-attach ${overlay.serverName}`}
      subtitle={summary(overlay)}
      onClose={onClose}
      footer={
        <FormActions>
          <Button variant="ghost" onClick={onClose}>
            Cancel
          </Button>
          <Button type="submit" form={form} disabled={!to || attach.pending}>
            Re-attach
          </Button>
        </FormActions>
      }
    >
      <form
        id={form}
        className="flex flex-col gap-4"
        onSubmit={async (e) => {
          e.preventDefault();
          if (await attach.run({ serverName: overlay.serverName, to })) onClose();
        }}
      >
        <Field label="Re-attach to" hint="Only Apps without settings of their own.">
          <Select value={to} onChange={(e) => setTo(e.target.value)}>
            <option value="">Choose an App…</option>
            {targets.map((a) => (
              <option key={a.serverName} value={a.serverName}>
                {a.name} ({a.serverName})
              </option>
            ))}
          </Select>
        </Field>
        {attach.error && <Notice tone="down">{attach.error.rawMessage}</Notice>}
      </form>
    </Dialog>
  );
}
