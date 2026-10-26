import type { DescMethodUnary } from "@bufbuild/protobuf";
import { Refresh, useAction, useQuery, type Action } from "@ddash/plugin";
import { CoreService, type CataloguePlugin, type ComponentStatus, type ListCatalogueResponse } from "@ddash/proto/ddash/core/core_pb";
import {
  Button,
  cx,
  DataTable,
  EmptyState,
  Icon,
  Menu,
  Notice,
  PageTitle,
  Panel,
  Pill,
  SectionHeading,
  Skeleton,
  StatusDot,
  type IconName,
  type Tone,
} from "@ddash/ui";
import { useEffect, useState, type ReactNode } from "react";
import { ConfigSheet } from "./config-form";

/**
 * The Plugins page: the Catalogue of the running Release, read at the FAST
 * tier, as cards: the installed Plugins with each Component's health and
 * config form, then the available ones with Install, and the orphaned DB
 * files below. After an install, the forms of the Components it added that
 * turn out unconfigured open in turn.
 */
export function Catalogue() {
  const { data, error } = useQuery(CoreService.method.listCatalogue, {});
  const [configuring, setConfiguring] = useState<string>();
  const [queue, setQueue] = useState<string[]>([]);
  const done = (name: string) => setQueue((q) => q.filter((n) => n !== name));
  const onInstalled = (added: string[]) => setQueue((q) => [...q, ...added]);
  const installed = data?.plugins.filter((p) => p.installed) ?? [];
  const available = data?.plugins.filter((p) => !p.installed) ?? [];
  const card = (p: CataloguePlugin) => <PluginCard key={p.name} plugin={p} onConfigure={setConfiguring} onInstalled={onInstalled} />;
  return (
    <div>
      <PageTitle
        kicker="System"
        title="Plugins"
        subtitle={`${data?.release ? `Release ${data.release}. ` : ""}Installing a Plugin pulls in the Components it requires.`}
      />
      {data?.availableRelease && (
        <div className="mb-6">
          <Notice tone="info" title={`Release ${data.availableRelease} available`}>
            Every Plugin moves to it together.
          </Notice>
        </div>
      )}
      {error && !data && (
        <Notice tone="down" title="The Catalogue can't be read">
          {error.rawMessage}
        </Notice>
      )}
      {!error && !data && <Skeleton height="xl" />}
      {data && (
        <>
          <SectionHeading kicker={`${installed.length} of ${data.plugins.length}`} title="Installed" />
          {installed.length > 0 ? (
            <Cards label="Installed">{installed.map(card)}</Cards>
          ) : (
            <Panel>
              <EmptyState icon="package" title="No Plugin installed yet">
                Install one of the available Plugins below.
              </EmptyState>
            </Panel>
          )}
          {available.length > 0 && (
            <div className="mt-10">
              <SectionHeading title="Available" />
              <Cards label="Available">{available.map(card)}</Cards>
            </div>
          )}
          {data.orphans.length > 0 && <Orphans orphans={data.orphans} />}
        </>
      )}
      {configuring ? (
        <ConfigSheet owner={configuring} onClose={() => setConfiguring(undefined)} />
      ) : (
        queue.length > 0 && <FormQueue key={queue[0]} next={queue[0]} onDone={done} />
      )}
    </div>
  );
}

/** A grid of Plugin cards: one column on phones, two, then three. */
function Cards({ label, children }: { label: string; children: ReactNode }) {
  return (
    <ul aria-label={label} className="grid grid-cols-1 gap-4 md:grid-cols-2 2xl:grid-cols-3">
      {children}
    </ul>
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
  return needsForm || opened ? <ConfigSheet owner={next} onClose={() => onDone(next)} /> : null;
}

/** A Component's status in the Catalogue, from whichever Plugin runs it. */
function statusOf(cat: ListCatalogueResponse, name: string): ComponentStatus | undefined {
  const all = cat.plugins.flatMap((p) => p.components).filter((c) => c.name === name);
  return all.find((c) => c.state) ?? all[0];
}

/**
 * A Plugin as a card: its icon, title and author, the health of an
 * installed one with Uninstall in its menu, the description, then each
 * Component's health (installed) or the Components it pulls in and Install.
 */
function PluginCard({
  plugin: p,
  onConfigure,
  onInstalled,
}: {
  plugin: CataloguePlugin;
  onConfigure: (component: string) => void;
  onInstalled: (added: string[]) => void;
}) {
  const install = useAction(CoreService.method.install);
  const uninstall = useAction(CoreService.method.uninstall);
  const summary = summaryOf(p);
  return (
    <li aria-label={p.title} className="min-w-0">
      <Panel>
        <div className="flex items-start gap-3">
          <span
            className={cx(
              "grid h-10 w-10 shrink-0 place-items-center rounded-ui",
              p.installed ? "bg-primary-tint text-primary" : "bg-muted text-muted-foreground",
            )}
          >
            <Icon name={p.icon as IconName} size="lg" />
          </span>
          <div className="min-w-0 flex-1">
            <div className="truncate font-bold">{p.title}</div>
            {p.author && (
              <a className="text-xs text-muted-foreground hover:underline" href={`https://github.com/${p.author}`} target="_blank" rel="noreferrer">
                @{p.author}
              </a>
            )}
          </div>
          {p.installed && (
            <div className="flex shrink-0 items-center gap-1">
              {summary && (
                <Pill tone={toneOf(summary)} dot>
                  {summary}
                </Pill>
              )}
              <Menu
                label={`Actions for ${p.title}`}
                disabled={uninstall.pending}
                items={[
                  {
                    label: "Uninstall",
                    danger: true,
                    onSelect: () =>
                      void uninstall.run(({ checked }) => ({ plugin: p.name, deleteData: checked }), {
                        confirm: {
                          title: `Uninstall ${p.title}?`,
                          text: "The Components no other Plugin requires stop. Their data is kept unless you delete it.",
                          checkbox: "Also delete data",
                          action: "Uninstall",
                        },
                      }),
                  },
                ]}
              />
            </div>
          )}
        </div>
        <p className="mt-3 text-sm leading-relaxed text-muted-foreground">{p.description}</p>
        {p.installed && p.updatePending && (
          <div className="mt-3">
            <Notice tone="warn">{p.updatePending}</Notice>
          </div>
        )}
        {p.installed ? (
          <ul aria-label="Component health" className="mt-4 divide-y divide-border rounded-ui border border-border">
            {p.components.map((c) => (
              <Health key={c.name} component={c} onConfigure={onConfigure} />
            ))}
          </ul>
        ) : (
          <div className="mt-4 flex flex-wrap items-center gap-2">
            <ul aria-label="Components" className="flex min-w-0 flex-wrap gap-1.5">
              {p.components.map((c) => (
                <li key={c.name} className="inline-flex items-center gap-1.5 rounded-ui-sm bg-muted px-2 py-0.5 text-xs font-semibold text-muted-foreground">
                  {c.state && <StatusDot tone={toneOf(c.state)} label="running" />}
                  {c.name}
                </li>
              ))}
            </ul>
            <span className="ml-auto">
              <Button
                size="sm"
                disabled={install.pending}
                onClick={async () => {
                  const res = await install.run({ plugin: p.name });
                  if (res) onInstalled(res.components);
                }}
              >
                Install
              </Button>
            </span>
          </div>
        )}
        <Failed action={install} title="Install failed" />
        <Failed action={uninstall} title="Uninstall failed" />
      </Panel>
    </li>
  );
}

/**
 * One Component of an installed Plugin: its state and message, with
 * Restart when it's held down and Configure when it takes config (primary
 * while it waits for config).
 */
function Health({ component: c, onConfigure }: { component: ComponentStatus; onConfigure: (component: string) => void }) {
  const restart = useAction(CoreService.method.restart);
  const state = stateOf(c);
  return (
    <li aria-label={c.name} className="px-3.5 py-2.5 text-sm">
      <div className="flex items-center gap-3">
        <StatusDot tone={toneOf(state)} label={state} />
        <span className="min-w-0 flex-1">
          <span className="block truncate font-bold">{c.name}</span>
          <span className="block truncate text-xs text-muted-foreground">{[state, c.message].filter(Boolean).join(" · ")}</span>
        </span>
        {c.held && (
          <Button
            variant="outline"
            size="sm"
            disabled={restart.pending}
            onClick={() =>
              restart.run({ component: c.name }, { confirm: { text: `Restart ${c.name}? It stays down until restarted.`, action: "Restart" } })
            }
          >
            Restart
          </Button>
        )}
        {c.configurable && (
          <Button variant={c.state === "unconfigured" || c.needsConfig ? "default" : "outline"} size="sm" onClick={() => onConfigure(c.name)}>
            Configure
          </Button>
        )}
      </div>
      <Failed action={restart} title="Restart failed" />
    </li>
  );
}

/** The DB files no installed Plugin's Components use, each with Delete in its menu. */
function Orphans({ orphans }: { orphans: string[] }) {
  const del = useAction(CoreService.method.deleteOrphan);
  return (
    <div className="mt-10">
      <Panel flush title="Orphaned data" description="Kept after an uninstall. Reinstalling the Plugin picks its history up again.">
        <DataTable
          density="compact"
          rows={orphans}
          rowKey={(name) => name}
          columns={[{ id: "file", header: "DB file", rowHeader: true, mono: true, cell: (name) => `${name}.db` }]}
          actions={(name) => [
            {
              label: "Delete",
              danger: true,
              onSelect: () =>
                void del.run(
                  { component: name },
                  { confirm: { text: `Delete ${name}'s data? Its DB file and config are deleted for good.`, action: "Delete" } },
                ),
            },
          ]}
        />
        {del.error && (
          <div className="border-t border-border p-4">
            <Notice tone="down" title="Delete failed">
              {del.error.rawMessage}
            </Notice>
          </div>
        )}
      </Panel>
    </div>
  );
}

function Failed({ action, title }: { action: Pick<Action<DescMethodUnary>, "error">; title: string }) {
  return action.error ? (
    <div className="mt-3">
      <Notice tone="down" title={title}>
        {action.error.rawMessage}
      </Notice>
    </div>
  ) : null;
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

const rank: Record<Tone, number> = { down: 3, warn: 2, idle: 1, maint: 1, info: 0, up: 0 };

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
