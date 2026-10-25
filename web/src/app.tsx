import { CoreScope, InstalledPlugins, PluginHost, PluginScope, type Fetch, type PluginDefinition, type QueryClient } from "@ddash/plugin";
import { cx, ErrorFrame, Notice, PageTitle, StatusDot, type Tone } from "@ddash/ui";
import { useState } from "react";
import { Route, Routes, useLocation, useParams, useRoutes } from "react-router";
import { useAppearance } from "./appearance";
import { ErrorBoundary } from "./boundary";
import { Catalogue } from "./catalogue";
import { Chrome } from "./chrome";
import { navItems, type NavItem } from "./nav";
import { Overview, useOverviewLayout } from "./overview";
import { HostWatch, ReachContext, unreachableText, useReach } from "./reach";
import { Settings } from "./settings";
import { importBundle as defaultImport, usePlugins, type ImportBundle, type Plugins } from "./plugins";

export interface AppProps {
  /** The shell's one QueryClient (createQueryClient()). */
  client: QueryClient;
  /** Replaces window.fetch for every call, in tests. */
  fetch?: Fetch;
  /** Replaces the dynamic import of bundles, in tests. */
  importBundle?: ImportBundle;
  /** Replaces window.location.reload, which a 401 calls, in tests. */
  reload?: () => void;
}

/** The shell, inside a router. */
export function App({ client, fetch, importBundle = defaultImport, reload = () => window.location.reload() }: AppProps) {
  // Every call to the host goes through one watch, which tells whether ddash answers.
  const [watch] = useState(() => new HostWatch(fetch ?? ((input, init) => globalThis.fetch(input, init)), reload));
  return (
    <ReachContext.Provider value={watch}>
      <PluginHost client={client}>
        <CoreScope fetch={watch.fetch}>
          <Shell fetch={watch.fetch} importBundle={importBundle} />
        </CoreScope>
      </PluginHost>
    </ReachContext.Provider>
  );
}

function Shell({ fetch, importBundle }: { fetch: Fetch; importBundle: ImportBundle }) {
  const plugins = usePlugins(importBundle, fetch);
  const overview = useOverviewLayout(plugins);
  useAppearance();
  return (
    <InstalledPlugins names={plugins.installed?.map((p) => p.name)}>
      <Chrome items={navItems(plugins.loaded.values())} aside={(item) => <NavAside item={item} fetch={fetch} />} banner={<Unreachable />}>
        <Routes>
          <Route index element={<Overview plugins={plugins} overview={overview} fetch={fetch} />} />
          <Route path="plugins" element={<Catalogue />} />
          <Route path="settings/*" element={<Settings />} />
          <Route path=":plugin/*" element={<PluginPage plugins={plugins} fetch={fetch} />} />
        </Routes>
      </Chrome>
    </InstalledPlugins>
  );
}

/** A Plugin Nav Entry's end: failed to load, or its live badge in the Plugin's scope. */
function NavAside({ item, fetch }: { item: NavItem; fetch?: Fetch }) {
  if (item.broken) return <StatusDot tone="warn" label="failed to load" />;
  if (!item.useBadge) return null;
  return (
    <PluginScope plugin={item.plugin} fetch={fetch}>
      <ErrorBoundary fallback={() => null}>
        <NavBadge useBadge={item.useBadge} />
      </ErrorBoundary>
    </PluginScope>
  );
}

/** The banner while ddash doesn't answer. */
function Unreachable() {
  const reach = useReach();
  if (!reach.unreachable) return null;
  return (
    <div className="mb-6">
      <Notice tone="warn">{unreachableText(reach)}</Notice>
    </div>
  );
}

// A count pill's fill on the nav surface, by its tone.
const navBadgeFill: Record<Exclude<Tone, "idle">, string> = {
  up: "bg-up",
  down: "bg-down",
  warn: "bg-warn",
  maint: "bg-maint",
  info: "bg-info",
};

/** A Nav Entry's badge as a count pill, filled in its tone. */
function NavBadge({ useBadge }: { useBadge: NonNullable<NavItem["useBadge"]> }) {
  const badge = useBadge();
  if (!badge) return null;
  return (
    <span
      className={cx(
        "inline-flex h-5 min-w-5 items-center justify-center rounded-full px-1.5 text-[11px] font-bold tabular-nums",
        badge.tone === "idle" ? "bg-nav-hover text-nav-strong" : cx(navBadgeFill[badge.tone], "text-primary-foreground"),
      )}
    >
      {"text" in badge ? badge.text : badge.count}
    </span>
  );
}


/** /<plugin-name>/*: the Plugin's Pages, in its scope, behind an error boundary. */
function PluginPage({ plugins, fetch }: { plugins: Plugins; fetch?: Fetch }) {
  const name = useParams().plugin!;
  const { pathname } = useLocation();
  const plugin = plugins.loaded.get(name);
  if (!plugin) {
    const listed = plugins.installed?.some((p) => p.name === name);
    return listed || !plugins.installed ? <p className="text-sm text-muted-foreground">Loading…</p> : <NotFound />;
  }
  if (plugin.error) return <ErrorFrame title={`${plugin.title} failed to load`} error={plugin.error} />;
  return (
    <PluginScope plugin={plugin} fetch={fetch}>
      <ErrorBoundary resetKey={pathname} fallback={(error) => <ErrorFrame title={`${plugin.title} crashed`} error={error} />}>
        <Pages pages={plugin.definition?.pages ?? []} />
      </ErrorBoundary>
    </PluginScope>
  );
}

function Pages({ pages }: { pages: NonNullable<PluginDefinition["pages"]> }) {
  return useRoutes(pages) ?? <NotFound />;
}

function NotFound() {
  return <PageTitle title="Not found" subtitle="No installed Plugin has a Page here." />;
}

