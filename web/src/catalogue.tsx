import { Refresh, useAction, useQuery, type Action } from "@ddash/plugin";
import { CoreService, type CataloguePlugin, type ComponentStatus, type ListCatalogueResponse } from "@ddash/proto/ddash/core/core_pb";
import { Button, cx, DataTable, EmptyState, Facts, FormActions, Menu, MetricTile, Notice, PageTitle, Panel, ProtoSheet, SearchField, SectionHeading, StatePill, StatusDot, StatusList, Tabs, useVariant, type Tone } from "@ddash/ui";
import { useEffect, useId, useState } from "react";
import { ErrorFrame } from "./boundary";
import { ConfigDialog, ConfigForm } from "./config-form";
import { icon } from "./icons";

/**
 * The Plugins page: the Catalogue of the running Release, read at the FAST
 * tier, with Install and Uninstall, each installed Plugin's health and its
 * Components' config forms, and the orphaned DB files below. After an
 * install, the forms of the Components it added that turn out unconfigured
 * open in turn.
 * PROTOTYPE: ?variant=1 an explorer whose rows open a Plugin Sheet (the
 * config form opens inside it); ?variant=2 cards, the form in a Dialog.
 */
export function Catalogue() {
  const { data, error } = useQuery(CoreService.method.listCatalogue, {});
  const v = useVariant();
  const [configuring, setConfiguring] = useState<string>();
  const [queue, setQueue] = useState<string[]>([]);
  const [shown, setShown] = useState<string>();
  const done = (name: string) => setQueue((q) => q.filter((n) => n !== name));
  const onInstalled = (added: string[]) => setQueue((q) => [...q, ...added]);
  const installed = data?.plugins.filter((p) => p.installed) ?? [];
  const attention = installed.filter((p) => { const t = toneOf(summaryOf(p) ?? ""); return t === "down" || t === "warn"; });
  // PROTOTYPE: ?sheet=<plugin> and ?configure=<component> open them on load.
  const q = new URLSearchParams(window.location.search);
  const [opened, setOpened] = useState(false);
  if (!opened && data) {
    setOpened(true);
    if (q.get("sheet")) setShown(q.get("sheet")!);
    if (q.get("configure")) setConfiguring(q.get("configure")!);
  }
  const head = (
    <>
      <PageTitle
        kicker="System"
        title="Plugins"
        subtitle={`${data?.release ? `Release ${data.release}. ` : ""}Installing a Plugin pulls in the Components it requires.`}
      />
      {data?.availableRelease && (
        <div className="mb-6">
          <Notice tone="info" title={`Release ${data.availableRelease} available`}>
            Every Plugin moves to it together; ddash downloads it in the background and asks before switching.
          </Notice>
        </div>
      )}
      {error && !data && <ErrorFrame title="The Catalogue can't be read" error={error} />}
    </>
  );
  const forms =
    v !== "2" ? (
      queue.length > 0 && !configuring && <FormQueue key={queue[0]} next={queue[0]} onDone={done} />
    ) : configuring ? (
      <ConfigDialog owner={configuring} onClose={() => setConfiguring(undefined)} />
    ) : (
      queue.length > 0 && <FormQueue key={queue[0]} next={queue[0]} onDone={done} />
    );

  if (v === "2") {
    const available = data?.plugins.filter((p) => !p.installed) ?? [];
    return (
      <div>
        {head}
        {data && (
          <>
            <SectionHeading kicker={`${installed.length} of ${data.plugins.length}`} title="Installed" />
            <ul aria-label="Catalogue" className="grid grid-cols-1 gap-4 md:grid-cols-2 2xl:grid-cols-3">
              {installed.map((p) => (
                <PluginCard key={p.name} plugin={p} onConfigure={setConfiguring} onInstalled={onInstalled} />
              ))}
            </ul>
            {available.length > 0 && (
              <div className="mt-10">
                <SectionHeading kicker="In this Release" title="Available" />
                <ul className="grid grid-cols-1 gap-4 md:grid-cols-2 2xl:grid-cols-3">
                  {available.map((p) => (
                    <PluginCard key={p.name} plugin={p} onConfigure={setConfiguring} onInstalled={onInstalled} />
                  ))}
                </ul>
              </div>
            )}
          </>
        )}
        {data && data.orphans.length > 0 && <Orphans orphans={data.orphans} />}
        {forms}
      </div>
    );
  }

  const plugin = data?.plugins.find((p) => p.name === shown);
  return (
    <div>
      {head}
      {data && (
        <>
          <div className="mb-6 grid grid-cols-2 gap-4 xl:grid-cols-4">
            <MetricTile label="Installed" value={installed.length} icon="up" tone="up" desc={`of ${data.plugins.length} in the Release`} />
            <MetricTile label="Need attention" value={attention.length} icon="alert" tone={attention.length ? "warn" : "idle"} desc="a Component down or unconfigured" />
            <MetricTile label="Components" value={new Set(installed.flatMap((p) => p.components.map((c) => c.name))).size} icon="gauge" tone="maint" desc="running for the installed Plugins" />
            <MetricTile label="Orphaned data" value={data.orphans.length} icon="wrench" desc="DB files kept after an uninstall" />
          </div>
          <Explorer cat={data} attention={attention} onShow={setShown} onInstalled={onInstalled} />
        </>
      )}
      {data && data.orphans.length > 0 && <Orphans orphans={data.orphans} />}
      {plugin && (
        <PluginSheet
          plugin={plugin}
          configuring={configuring}
          onConfigure={setConfiguring}
          onClose={() => {
            setShown(undefined);
            setConfiguring(undefined);
          }}
          onInstalled={onInstalled}
        />
      )}
      {!plugin && configuring && <ConfigSheet owner={configuring} onClose={() => setConfiguring(undefined)} />}
      {forms}
    </div>
  );
}

type Tab = "all" | "installed" | "available" | "attention";

function Explorer({ cat, attention, onShow, onInstalled }: { cat: ListCatalogueResponse; attention: CataloguePlugin[]; onShow: (name: string) => void; onInstalled: (added: string[]) => void }) {
  const [query, setQuery] = useState("");
  const [tab, setTab] = useState<Tab>("all");
  const install = useAction(CoreService.method.install);
  const uninstall = useAction(CoreService.method.uninstall);
  const rows = cat.plugins.filter(
    (p) =>
      (tab === "all" || (tab === "installed" && p.installed) || (tab === "available" && !p.installed) || (tab === "attention" && attention.includes(p))) &&
      `${p.title} ${p.description} ${p.components.map((c) => c.name).join(" ")}`.toLowerCase().includes(query.toLowerCase()),
  );
  return (
    <Panel flush>
      <div className="flex flex-col gap-3 border-b border-border p-5 lg:flex-row lg:items-center">
        <SearchField label="Search Plugins" value={query} onChange={setQuery} placeholder="Search Plugins and Components…" />
        <div className="overflow-x-auto">
          <Tabs
            label="Filter"
            value={tab}
            onChange={setTab}
            options={[
              { id: "all", label: "All", count: cat.plugins.length },
              { id: "installed", label: "Installed", count: cat.plugins.filter((p) => p.installed).length },
              { id: "available", label: "Available", count: cat.plugins.filter((p) => !p.installed).length },
              { id: "attention", label: "Needs attention", count: attention.length },
            ]}
          />
        </div>
      </div>
      <DataTable
        label="Catalogue"
        rows={rows}
        rowKey={(p) => p.name}
        onRowClick={(p) => onShow(p.name)}
        empty={<EmptyState title="No Plugin matches">Try another name, or show All.</EmptyState>}
        columns={[
          {
            id: "plugin",
            header: "Plugin",
            rowHeader: true,
            sortValue: (p) => p.title,
            cell: (p) => <PluginName plugin={p} />,
          },
          {
            id: "components",
            header: "Components",
            hideBelow: "sm",
            cell: (p) => (
              <span className="flex gap-1.5">
                {p.components.slice(0, 3).map((c) => (
                  <span key={c.name} className="inline-flex items-center gap-1.5 rounded-md bg-muted px-2 py-0.5 text-[11px] font-semibold text-muted-foreground">
                    {c.state && <StatusDot tone={toneOf(stateOf(c))} />}
                    {c.name}
                  </span>
                ))}
                {p.components.length > 3 && <span className="text-[11px] font-bold text-muted-foreground">+{p.components.length - 3}</span>}
              </span>
            ),
          },
          {
            id: "health",
            header: "Health",
            sortValue: (p) => (p.installed ? rank[toneOf(summaryOf(p) ?? "")] : -1),
            cell: (p) => (p.installed ? <StatePill state={summaryOf(p) ?? "ok"} /> : <span className="text-xs text-muted-foreground">not installed</span>),
          },
          {
            id: "action",
            header: "",
            align: "end",
            cell: (p) =>
              p.installed ? (
                <span className="text-xs font-bold text-muted-foreground">Manage</span>
              ) : (
                <span onClick={(e) => e.stopPropagation()}>
                  <Button size="sm" disabled={install.pending} onClick={async () => { const res = await install.run({ plugin: p.name }); if (res) onInstalled(res.components); }}>
                    Install
                  </Button>
                </span>
              ),
          },
        ]}
        actions={(p) =>
          p.installed
            ? [
                { label: "Show details", onSelect: () => onShow(p.name) },
                { label: "Uninstall…", danger: true, disabled: uninstall.pending, onSelect: () => void uninstallRun(uninstall, p) },
              ]
            : [{ label: "Show details", onSelect: () => onShow(p.name) }]
        }
      />
      {install.error && <div className="p-5"><Notice tone="down" title="Install failed">{install.error.rawMessage}</Notice></div>}
    </Panel>
  );
}

const uninstallRun = (uninstall: Action<typeof CoreService.method.uninstall>, p: CataloguePlugin) =>
  uninstall.run(({ checked }) => ({ plugin: p.name, deleteData: checked }), {
    confirm: {
      text: `Uninstall ${p.title}? The Components no other Plugin requires stop. Their data is kept unless you delete it.`,
      checkbox: "Also delete data",
      action: "Uninstall",
    },
  });

function PluginName({ plugin: p }: { plugin: CataloguePlugin }) {
  const Icon = icon(p.icon);
  return (
    <span className="flex items-center gap-3">
      <span className={cx("grid h-9 w-9 shrink-0 place-items-center rounded-lg", p.installed ? "bg-primary/10 text-primary" : "bg-muted text-muted-foreground")}>
        <Icon className="h-4.5 w-4.5" aria-hidden />
      </span>
      <span className="min-w-0">
        <span className="block truncate font-bold">{p.title}</span>
        <span className="block max-w-80 truncate text-xs font-normal text-muted-foreground">{p.description}</span>
      </span>
    </span>
  );
}

/** Variant 1: a Plugin's detail, with its Components; Configure swaps in the form. */
function PluginSheet({
  plugin: p,
  configuring,
  onConfigure,
  onClose,
  onInstalled,
}: {
  plugin: CataloguePlugin;
  configuring?: string;
  onConfigure: (c: string | undefined) => void;
  onClose: () => void;
  onInstalled: (added: string[]) => void;
}) {
  const install = useAction(CoreService.method.install);
  const uninstall = useAction(CoreService.method.uninstall);
  const formId = useId();
  if (configuring) {
    return (
      <ProtoSheet
        wide
        kicker={`${p.title} · Configure`}
        title={configuring}
        subtitle={`Saving restarts ${configuring} with the new config. Unset fields take their defaults.`}
        back={{ label: p.title, onClick: () => onConfigure(undefined) }}
        onClose={onClose}
        footer={
          <FormActions>
            <Button variant="ghost" onClick={() => onConfigure(undefined)}>
              Cancel
            </Button>
            <Button type="submit" form={formId}>
              Save and restart
            </Button>
          </FormActions>
        }
      >
        <ConfigForm owner={configuring} formId={formId} />
      </ProtoSheet>
    );
  }
  const summary = summaryOf(p);
  return (
    <ProtoSheet
      wide
      kicker={p.installed ? "Installed Plugin" : "Available Plugin"}
      title={
        <span className="flex items-center gap-3">
          {p.title}
          {p.installed && summary && <StatePill state={summary} />}
        </span>
      }
      subtitle={p.description}
      onClose={onClose}
      footer={
        <FormActions status={p.installed ? "Uninstalling keeps the data unless you delete it." : "Installing pulls in the Components it requires."}>
          {p.installed ? (
            <Button variant="danger" disabled={uninstall.pending} onClick={() => void uninstallRun(uninstall, p)}>
              Uninstall
            </Button>
          ) : (
            <Button disabled={install.pending} onClick={async () => { const res = await install.run({ plugin: p.name }); if (res) onInstalled(res.components); }}>
              Install
            </Button>
          )}
        </FormActions>
      }
    >
      <div className="flex flex-col gap-6">
        {p.updatePending && <Notice tone="warn" title="Update pending">{p.updatePending}</Notice>}
        <Facts
          items={[
            { label: "Name", value: p.name, mono: true },
            { label: "Author", value: p.author ? <a className="font-semibold text-primary hover:underline" href={`https://github.com/${p.author}`} target="_blank" rel="noreferrer">@{p.author}</a> : "—" },
            { label: "Components", value: p.components.length },
          ]}
        />
        <div>
          <div className="mb-3 text-[10px] font-extrabold tracking-[0.14em] text-muted-foreground uppercase">Components</div>
          <div className="rounded-ui border border-border p-4">
            <StatusList items={p.components.map((c) => ({ tone: p.installed ? toneOf(stateOf(c)) : "idle", title: c.name, meta: p.installed ? [stateOf(c), c.message].filter(Boolean).join(" · ") : "starts on Install", aside: p.installed && <ComponentActions component={c} onConfigure={onConfigure} /> }))} />
          </div>
        </div>
        {install.error && <Notice tone="down" title="Install failed">{install.error.rawMessage}</Notice>}
        {uninstall.error && <Notice tone="down" title="Uninstall failed">{uninstall.error.rawMessage}</Notice>}
      </div>
    </ProtoSheet>
  );
}

function ConfigSheet({ owner, onClose }: { owner: string; onClose: () => void }) {
  const formId = useId();
  return (
    <ProtoSheet
      wide
      kicker="Configure"
      title={owner}
      subtitle={`Saving restarts ${owner} with the new config. Unset fields take their defaults.`}
      onClose={onClose}
      footer={
        <FormActions>
          <Button variant="ghost" onClick={onClose}>
            Cancel
          </Button>
          <Button type="submit" form={formId}>
            Save and restart
          </Button>
        </FormActions>
      }
    >
      <ConfigForm owner={owner} formId={formId} />
    </ProtoSheet>
  );
}

function ComponentActions({ component: c, onConfigure }: { component: ComponentStatus; onConfigure: (component: string) => void }) {
  const restart = useAction(CoreService.method.restart);
  return (
    <>
      {c.held && (
        <Button variant="outline" size="sm" disabled={restart.pending} onClick={() => restart.run({ component: c.name }, { confirm: { text: `Restart ${c.name}? It stays down until restarted.`, action: "Restart" } })}>
          Restart
        </Button>
      )}
      {c.configurable && (
        <Button variant={c.state === "unconfigured" || c.needsConfig ? "default" : "outline"} size="sm" onClick={() => onConfigure(c.name)}>
          Configure
        </Button>
      )}
    </>
  );
}

/** Variant 2: a Plugin as a card, its Components always listed. */
function PluginCard({ plugin: p, onConfigure, onInstalled }: { plugin: CataloguePlugin; onConfigure: (component: string) => void; onInstalled: (added: string[]) => void }) {
  const install = useAction(CoreService.method.install);
  const uninstall = useAction(CoreService.method.uninstall);
  const summary = summaryOf(p);
  return (
    <li aria-label={p.title}>
      <Panel>
        <div className="flex items-start gap-3">
          <PluginName plugin={{ ...p, description: p.author ? `@${p.author}` : "" } as CataloguePlugin} />
          <div className="ml-auto flex shrink-0 items-center gap-1">
            {p.installed && summary && <StatePill state={summary} />}
            {p.installed && <Menu label={`${p.title} actions`} items={[{ label: "Uninstall…", danger: true, onSelect: () => void uninstallRun(uninstall, p) }]} />}
          </div>
        </div>
        <p className="mt-3 text-sm leading-relaxed text-muted-foreground">{p.description}</p>
        {p.updatePending && <div className="mt-3"><Notice tone="warn">{p.updatePending}</Notice></div>}
        {p.installed ? (
          <ul aria-label="Component health" className="mt-4 divide-y divide-border rounded-lg border border-border">
            {p.components.map((c) => (
              <li key={c.name} aria-label={c.name} className="flex items-center gap-3 px-3.5 py-2.5 text-sm">
                <StatusDot tone={toneOf(stateOf(c))} label={stateOf(c)} />
                <span className="min-w-0 flex-1">
                  <span className="block truncate font-bold">{c.name}</span>
                  <span className="block truncate text-xs text-muted-foreground">{[stateOf(c), c.message].filter(Boolean).join(" · ")}</span>
                </span>
                <ComponentActions component={c} onConfigure={onConfigure} />
              </li>
            ))}
          </ul>
        ) : (
          <div className="mt-4 flex flex-wrap items-center gap-2">
            {p.components.map((c) => (
              <span key={c.name} className="rounded-md bg-muted px-2 py-0.5 text-[11px] font-semibold text-muted-foreground">
                {c.name}
              </span>
            ))}
            <span className="ml-auto">
              <Button size="sm" disabled={install.pending} onClick={async () => { const res = await install.run({ plugin: p.name }); if (res) onInstalled(res.components); }}>
                Install
              </Button>
            </span>
          </div>
        )}
        {install.error && <div className="mt-3"><Notice tone="down" title="Install failed">{install.error.rawMessage}</Notice></div>}
        {uninstall.error && <div className="mt-3"><Notice tone="down" title="Uninstall failed">{uninstall.error.rawMessage}</Notice></div>}
      </Panel>
    </li>
  );
}

/** The DB files no installed Plugin's Components use, each with Delete. */
function Orphans({ orphans }: { orphans: string[] }) {
  const del = useAction(CoreService.method.deleteOrphan);
  return (
    <div className="mt-8">
      <Panel flush kicker="Kept after an uninstall" title="Orphaned data" description="Reinstalling the Plugin picks its history up again.">
        <DataTable
          label="Orphaned data"
          density="compact"
          rows={orphans}
          rowKey={(n) => n}
          columns={[{ id: "file", header: "DB file", rowHeader: true, cell: (n) => <span className="font-mono text-xs">{n}.db</span> }]}
          actions={(name) => [
            { label: "Delete…", danger: true, onSelect: () => void del.run({ component: name }, { confirm: { text: `Delete ${name}'s data? Its DB file and config are deleted for good.`, action: "Delete" } }) },
          ]}
        />
        {del.error && <div className="p-5"><Notice tone="down" title="Delete failed">{del.error.rawMessage}</Notice></div>}
      </Panel>
    </div>
  );
}

/**
 * Opens the form of next, a Component an install added, once it turns out
 * unconfigured, reading the Catalogue at the LIVE tier meanwhile, and keeps
 * it open until closed. One that starts without config needs no form.
 */
function FormQueue({ next, onDone }: { next: string; onDone: (name: string) => void }) {
  const { data } = useQuery(CoreService.method.listCatalogue, {}, { refresh: Refresh.LIVE });
  const [opened, setOpened] = useState(false);
  const st = data && statusOf(data, next);
  const waiting = !st || st.state === "" || st.state === "starting";
  const needsForm = st?.state === "unconfigured" && st.configurable;
  if (needsForm && !opened) setOpened(true);
  const skip = !needsForm && !opened && !waiting;
  useEffect(() => {
    if (skip) onDone(next);
  }, [skip, next, onDone]);
  const v = useVariant();
  if (!(needsForm || opened)) return null;
  return v === "2" ? <ConfigDialog owner={next} onClose={() => onDone(next)} /> : <ConfigSheet owner={next} onClose={() => onDone(next)} />;
}

/** A Component's status in the Catalogue, from whichever Plugin runs it. */
function statusOf(cat: ListCatalogueResponse, name: string): ComponentStatus | undefined {
  const all = cat.plugins.flatMap((p) => p.components).filter((c) => c.name === name);
  return all.find((c) => c.state) ?? all[0];
}

/**
 * A Component's state, as the Catalogue shows it: one that doesn't run yet
 * is starting, and one unconfigured although config was saved for it (a
 * Release added a required field) needs config.
 */
const stateOf = (c: ComponentStatus) => (c.needsConfig ? "needs config" : c.state || "starting");

/** A Component, or a Plugin's bundles, of the running Release that couldn't be downloaded yet. */
const updatePending = "update pending";

const tones: Record<string, Tone> = {
  [updatePending]: "maint",
  ok: "up",
  degraded: "warn",
  unconfigured: "warn",
  down: "down",
  crashed: "down",
};

const toneOf = (state: string): Tone => tones[state] ?? (state === "needs config" ? "warn" : "idle");

const rank: Record<Tone, number> = { down: 3, warn: 2, idle: 1, maint: 1, up: 0 };

/**
 * What an installed Plugin shows: update pending while any of it couldn't be
 * downloaded, else its worst Component health.
 */
function summaryOf(p: CataloguePlugin): string | undefined {
  if (p.updatePending || p.components.some((c) => c.state === updatePending)) return updatePending;
  const worst = worstOf(p.components);
  return worst && stateOf(worst);
}

/** The Component in the worst health, the first among equals. */
function worstOf(components: ComponentStatus[]): ComponentStatus | undefined {
  return components.reduce<ComponentStatus | undefined>(
    (worst, c) => (!worst || rank[toneOf(stateOf(c))] > rank[toneOf(stateOf(worst))] ? c : worst),
    undefined,
  );
}
