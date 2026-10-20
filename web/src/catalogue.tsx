import type { DescMethodUnary } from "@bufbuild/protobuf";
import { useAction, useQuery, type Action } from "@ddash/plugin";
import { CoreService, type CataloguePlugin, type ComponentStatus } from "@ddash/proto/ddash/core/core_pb";
import { Button, PageHeader, StatusDot, type Tone } from "@ddash/ui";
import { ChevronDown } from "lucide-react";
import { useState } from "react";
import { ErrorFrame } from "./boundary";
import { icon } from "./icons";

/**
 * The Plugins page: the Catalogue of the running Release, read at the FAST
 * tier, with Install and Uninstall, each installed Plugin's health, and the
 * orphaned DB files below.
 */
export function Catalogue() {
  const { data, error } = useQuery(CoreService.method.listCatalogue, {});
  return (
    <div>
      <PageHeader
        title="Plugins"
        subtitle={`${data?.release ? `Release ${data.release}. ` : ""}Installing a Plugin pulls in the Components it requires.`}
      />
      {error && !data && <ErrorFrame title="The Catalogue can't be read" error={error} />}
      {data && (
        <ul aria-label="Catalogue" className="divide-y divide-border rounded-ui border border-border bg-card">
          {data.plugins.map((p) => (
            <Entry key={p.name} plugin={p} />
          ))}
        </ul>
      )}
      {data && data.orphans.length > 0 && <Orphans orphans={data.orphans} />}
    </div>
  );
}

function Entry({ plugin: p }: { plugin: CataloguePlugin }) {
  const install = useAction(CoreService.method.install);
  const uninstall = useAction(CoreService.method.uninstall);
  const [open, setOpen] = useState(false);
  const Icon = icon(p.icon);
  const worst = worstOf(p.components);
  return (
    <li aria-label={p.title} className="px-4 py-3">
      <div className="flex items-start gap-4">
        <Icon className="mt-0.5 h-5 w-5 shrink-0 text-muted-foreground" aria-hidden />
        <div className="min-w-0 flex-1">
          <div className="flex items-baseline gap-2">
            <span className="font-medium">{p.title}</span>
            {p.author && (
              <a className="text-xs text-muted-foreground hover:underline" href={`https://github.com/${p.author}`} target="_blank" rel="noreferrer">
                @{p.author}
              </a>
            )}
          </div>
          <div className="text-sm text-muted-foreground">{p.description}</div>
          <ul aria-label="Components" className="mt-2 flex flex-wrap gap-1.5">
            {p.components.map((c) => (
              <li key={c.name} className="inline-flex items-center gap-1.5 rounded-full bg-muted px-2 py-0.5 text-xs">
                {c.state && <StatusDot tone={toneOf(c.state)} label="running" />}
                {c.name}
              </li>
            ))}
          </ul>
        </div>
        <div className="flex shrink-0 items-center gap-3">
          {p.installed && worst && (
            <button
              type="button"
              aria-expanded={open}
              onClick={() => setOpen(!open)}
              className="flex cursor-pointer items-center gap-2 rounded-ui px-2 py-1 text-sm hover:bg-muted"
            >
              <StatusDot tone={toneOf(worst.state)} label={stateOf(worst)} />
              {stateOf(worst)}
              <ChevronDown className={open ? "h-4 w-4 rotate-180" : "h-4 w-4"} aria-hidden />
            </button>
          )}
          {p.installed ? (
            <Button
              variant="outline"
              size="sm"
              disabled={uninstall.pending}
              onClick={() =>
                uninstall.run(({ checked }) => ({ plugin: p.name, deleteData: checked }), {
                  confirm: {
                    text: `Uninstall ${p.title}? The Components no other Plugin requires stop. Their data is kept unless you delete it.`,
                    checkbox: "Also delete data",
                    action: "Uninstall",
                  },
                })
              }
            >
              Uninstall
            </Button>
          ) : (
            <Button size="sm" disabled={install.pending} onClick={() => install.run({ plugin: p.name })}>
              Install
            </Button>
          )}
        </div>
      </div>
      {p.installed && open && (
        <ul aria-label="Component health" className="mt-3 divide-y divide-border rounded-ui border border-border">
          {p.components.map((c) => (
            <Health key={c.name} component={c} />
          ))}
        </ul>
      )}
      <Failed action={install} title="Install failed" />
      <Failed action={uninstall} title="Uninstall failed" />
    </li>
  );
}

/** One Component of an installed Plugin, with Restart when it's held down. */
function Health({ component: c }: { component: ComponentStatus }) {
  const restart = useAction(CoreService.method.restart);
  return (
    <li className="px-3 py-2 text-sm">
      <div className="flex items-center gap-3">
        <StatusDot tone={toneOf(c.state)} label={stateOf(c)} />
        <span className="w-32 shrink-0 font-medium">{c.name}</span>
        <span className="w-24 shrink-0 text-muted-foreground">{stateOf(c)}</span>
        <span className="min-w-0 flex-1 truncate text-muted-foreground">{c.message}</span>
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
      </div>
      <Failed action={restart} title="Restart failed" />
    </li>
  );
}

/** The DB files no installed Plugin's Components use, each with Delete. */
function Orphans({ orphans }: { orphans: string[] }) {
  return (
    <section className="mt-8">
      <h2 className="mb-1 text-sm font-semibold">Orphaned data</h2>
      <p className="mb-3 text-sm text-muted-foreground">
        Kept after an uninstall. Reinstalling the Plugin picks its history up again.
      </p>
      <ul aria-label="Orphaned data" className="divide-y divide-border rounded-ui border border-border bg-card">
        {orphans.map((name) => (
          <Orphan key={name} name={name} />
        ))}
      </ul>
    </section>
  );
}

function Orphan({ name }: { name: string }) {
  const del = useAction(CoreService.method.deleteOrphan);
  return (
    <li className="px-4 py-2 text-sm">
      <div className="flex items-center gap-3">
        <span className="font-mono">{name}.db</span>
        <Button
          variant="outline"
          size="sm"
          className="ml-auto"
          disabled={del.pending}
          onClick={() =>
            del.run(
              { component: name },
              { confirm: { text: `Delete ${name}'s data? Its DB file and config are deleted for good.`, action: "Delete" } },
            )
          }
        >
          Delete
        </Button>
      </div>
      <Failed action={del} title="Delete failed" />
    </li>
  );
}

function Failed({ action, title }: { action: Pick<Action<DescMethodUnary>, "error">; title: string }) {
  return action.error ? (
    <div className="mt-2">
      <ErrorFrame title={title} error={action.error} />
    </div>
  ) : null;
}

/** A Component's state, as the Catalogue shows it; one that doesn't run yet is starting. */
const stateOf = (c: ComponentStatus) => c.state || "starting";

const tones: Record<string, Tone> = {
  ok: "up",
  degraded: "warn",
  unconfigured: "warn",
  down: "down",
  crashed: "down",
};

const toneOf = (state: string): Tone => tones[state] ?? "idle";

const rank: Record<Tone, number> = { down: 3, warn: 2, idle: 1, maint: 1, up: 0 };

/** The Component in the worst health, the first among equals. */
function worstOf(components: ComponentStatus[]): ComponentStatus | undefined {
  return components.reduce<ComponentStatus | undefined>(
    (worst, c) => (!worst || rank[toneOf(stateOf(c))] > rank[toneOf(stateOf(worst))] ? c : worst),
    undefined,
  );
}
