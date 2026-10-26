import { useAction } from "@ddash/plugin";
import { AppsService, Reach, type App, type Workload } from "@ddash/proto/apps/apps_pb";
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
import { Link } from "react-router";
import { counting, sections, useApps, warningText } from "./apps";
import { checkStatus } from "@ddash/display";
import { useAppChecks } from "./checks";
import { EditSheet } from "./edit";
import { workloadName } from "@ddash/display";
import { WorkloadLine } from "./workload";

/** Whether an App matches the search: its name, its URL's host, its aliases or its group. */
const found = (q: string, app: App) => matches(q, app.name, URL.parse(app.url)?.hostname, ...app.aliases, app.group);

/** The Apps Page: App cards by group, their settings, Link-only Apps and orphaned App settings. */
export function AppsPage() {
  const { list, error } = useApps();
  const { byApp, error: checksError } = useAppChecks();
  const [q, setQ] = useQueryState("q", "");
  // The App being edited, "new" while adding one.
  const [editing, setEditing] = useState<App | "new">();
  const apps = list?.apps ?? [];
  const orphans = apps.filter((a) => a.orphaned);
  const shown = apps.filter((a) => !a.orphaned && found(q, a));
  const hidden = shown.filter((a) => a.hidden);
  const groups = [...new Set(apps.map((a) => a.group).filter(Boolean))].sort();
  const readError = list?.proxyHosts?.error;
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
      <PageTitle kicker="Services" title="Apps" subtitle="Every Proxy Host in Nginx Proxy Manager, plus the Apps you add." actions={header} />
      <div className="flex flex-col gap-6">
        {!list && !error && <Skeleton height="lg" />}
        {!list && error && <Notice tone="down">apps unavailable: {error.rawMessage}</Notice>}
        {list?.proxyHosts?.notConfigured ? (
          <Notice tone="info" title="Set up Nginx Proxy Manager">
            ddash reads the Proxy Hosts once nginx-proxy-manager-api has Nginx Proxy Manager's address and ddash's user there.{" "}
            <Link to="/plugins" className="font-semibold text-primary hover:underline">
              Configure it in Plugins
            </Link>
          </Notice>
        ) : (
          readError && <Notice tone="warn">Couldn't read the Proxy Hosts from Nginx Proxy Manager: {readError}. Showing those of the last read.</Notice>
        )}
        {list && apps.length > 0 && checksError && <Notice tone="warn">Check status unavailable: {checksError.rawMessage}</Notice>}
        {list && apps.length === 0 && (
          <Panel>
            <EmptyState icon="layout-grid" title="No Apps yet" action={add}>
              Proxy Hosts show here once ddash reads them from Nginx Proxy Manager, or add an App by hand.
            </EmptyState>
          </Panel>
        )}
        {apps.length > orphans.length && shown.length === 0 && (
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
        {orphans.length > 0 && (
          <Section title="Orphaned App settings">
            <p className="mb-3 text-sm text-muted-foreground">
              Settings kept for Apps whose Proxy Host is gone. Re-attach them to an App, or delete them.
            </p>
            <Orphans orphans={orphans} targets={apps.filter((a) => a.reach === Reach.PROXIED && !a.touched && !a.orphaned)} />
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

/** An App's card: icon, name and URL opening it, its Check's status, its Warnings that count, its Workload and its aliases. */
function Card({ app, checks, onEdit }: { app: App; checks?: ReadonlyMap<string, Check>; onEdit: () => void }) {
  const status = checks && checkStatus(checks.get(app.id));
  const warnings = counting(app);
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
          {warnings.length > 0 && (
            <p aria-label="Warnings" className="text-xs text-warn-text">
              {warnings.map((w) => warningText[w.kind]).join(" · ")}
            </p>
          )}
          {app.proxyHost && !app.proxyHost.enabled && <p className="text-xs text-muted-foreground">Proxy Host disabled</p>}
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

/** What orphaned App settings keep, e.g. "Documents · group Office · hidden". */
function summary(a: App) {
  const s = a.settings;
  const workload = s?.workload && `on ${workloadName({ ref: s.workload } as Workload)}`;
  return [s?.name, s?.icon && `icon ${s.icon}`, s?.group && `group ${s.group}`, s?.hidden && "hidden", workload].filter(Boolean).join(" · ");
}

/** An App's URL's hostname: what orphaned App settings were kept for. */
const hostOf = (a: App) => URL.parse(a.url)?.hostname ?? a.url;

const orphanColumns: Column<App>[] = [
  { id: "host", header: "Kept for", rowHeader: true, mono: true, cell: (a) => hostOf(a) },
  { id: "settings", header: "Settings", cell: (a) => summary(a) },
];

/**
 * Orphaned App settings as a compact table: each row's strip re-attaches
 * them to an App without settings, or deletes them.
 */
function Orphans({ orphans, targets }: { orphans: readonly App[]; targets: readonly App[] }) {
  const [reattaching, setReattaching] = useState<App>();
  return (
    <>
      <Panel flush>
        <DataTable
          rows={orphans}
          rowKey={(a) => a.id}
          columns={orphanColumns}
          density="compact"
          rowLabel={(a) => hostOf(a)}
          strip={(a) => <OrphanStrip orphan={a} canReattach={targets.length > 0} onReattach={() => setReattaching(a)} />}
        />
      </Panel>
      <Presence>{reattaching && <Reattach orphan={reattaching} targets={targets} onClose={() => setReattaching(undefined)} />}</Presence>
    </>
  );
}

/** An orphan's strip: Re-attach… opens its Dialog, Delete asks first. */
function OrphanStrip({ orphan, canReattach, onReattach }: { orphan: App; canReattach: boolean; onReattach: () => void }) {
  const remove = useAction(AppsService.method.deleteOrphan);
  const host = hostOf(orphan);
  return (
    <ActionStrip summary={`Kept for ${host}`} notice={remove.error && <Notice tone="down">{remove.error.rawMessage}</Notice>}>
      <Button size="sm" variant="outline" disabled={!canReattach} onClick={onReattach}>
        Re-attach…
      </Button>
      <Button
        size="sm"
        variant="danger"
        disabled={remove.pending}
        onClick={() => void remove.run({ id: orphan.id }, { confirm: { text: `Delete the settings kept for ${host}?`, action: "Delete" } })}
      >
        Delete
      </Button>
    </ActionStrip>
  );
}

/** Re-attaches orphaned App settings to an App without settings of its own. */
function Reattach({ orphan, targets, onClose }: { orphan: App; targets: readonly App[]; onClose: () => void }) {
  const attach = useAction(AppsService.method.reattach);
  const [to, setTo] = useState("");
  const form = useId();
  return (
    <Dialog
      title={`Re-attach ${hostOf(orphan)}`}
      subtitle={summary(orphan)}
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
          if (await attach.run({ id: orphan.id, to })) onClose();
        }}
      >
        <Field label="Re-attach to" hint="Only Apps without settings of their own.">
          <Select value={to} onChange={(e) => setTo(e.target.value)}>
            <option value="">Choose an App…</option>
            {targets.map((a) => (
              <option key={a.id} value={a.id}>
                {a.name} ({hostOf(a)})
              </option>
            ))}
          </Select>
        </Field>
        {attach.error && <Notice tone="down">{attach.error.rawMessage}</Notice>}
      </form>
    </Dialog>
  );
}
