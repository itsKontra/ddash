import { CoreScope, InstalledPlugins, PluginHost, PluginScope, useQuery, type Fetch, type PluginDefinition, type QueryClient } from "@ddash/plugin";
import { CoreService } from "@ddash/proto/ddash/core/core_pb";
import { Badge, Button, cx, PageHeader, StatusDot } from "@ddash/ui";
import { useState, type ReactNode } from "react";
import { NavLink, Route, Routes, useLocation, useParams, useRoutes } from "react-router";
import { useAppearance } from "./appearance";
import { ErrorBoundary, ErrorFrame } from "./boundary";
import { Catalogue } from "./catalogue";
import { icon } from "./icons";
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
      <div className="flex min-h-screen bg-background font-sans text-foreground">
        <Sidebar items={navItems(plugins.loaded.values())} fetch={fetch} />
        <main className="min-w-0 flex-1 p-8">
          <Unreachable />
          <Routes>
            <Route index element={<Overview plugins={plugins} overview={overview} fetch={fetch} />} />
            <Route path="plugins" element={<Catalogue />} />
            <Route path="settings/*" element={<Settings />} />
            <Route path=":plugin/*" element={<PluginPage plugins={plugins} fetch={fetch} />} />
          </Routes>
        </main>
      </div>
    </InstalledPlugins>
  );
}

/** The banner while ddash doesn't answer. */
function Unreachable() {
  const reach = useReach();
  if (!reach.unreachable) return null;
  return (
    <div role="status" className="mb-6 rounded-ui border border-warn bg-card px-4 py-2 text-sm font-medium text-warn-text">
      {unreachableText(reach)}
    </div>
  );
}

function Sidebar({ items, fetch }: { items: NavItem[]; fetch?: Fetch }) {
  return (
    <aside className="sticky top-0 flex h-screen w-60 shrink-0 flex-col border-r border-border bg-card p-3">
      <div className="mb-4 px-3 py-2 text-lg font-bold tracking-tight">ddash</div>
      <nav aria-label="Main" className="flex flex-1 flex-col">
        <Link to="/" label="Overview" icon="layout-dashboard" end />
        <div className="mt-4 flex flex-col gap-0.5">
          {items.map((item) => (
            <Link
              key={item.plugin.name}
              to={`/${item.plugin.name}`}
              label={item.label}
              icon={item.icon}
              aside={
                item.broken ? (
                  <StatusDot tone="warn" label="failed to load" />
                ) : (
                  item.useBadge && (
                    <PluginScope plugin={item.plugin} fetch={fetch}>
                      <ErrorBoundary fallback={() => null}>
                        <NavBadge useBadge={item.useBadge} />
                      </ErrorBoundary>
                    </PluginScope>
                  )
                )
              }
            />
          ))}
        </div>
        <div className="mt-auto flex flex-col gap-0.5 border-t border-border pt-3">
          <Link to="/plugins" label="Plugins" icon="puzzle" />
          <Link to="/settings" label="Settings" icon="settings" />
        </div>
      </nav>
      <Operator />
    </aside>
  );
}

/** Who is signed in, and Sign out: the host ends the session and Keycloak's. */
function Operator() {
  const { data } = useQuery(CoreService.method.getOperator, {});
  return (
    <section aria-label="Operator" className="mt-3 flex items-center gap-2 border-t border-border px-3 pt-3 text-sm">
      <span className="min-w-0 flex-1 truncate text-muted-foreground">{data?.operator?.name}</span>
      <form method="post" action="/auth/logout">
        <Button type="submit" variant="ghost" size="sm">
          Sign out
        </Button>
      </form>
    </section>
  );
}

function Link({ to, label, icon: name, aside, end }: { to: string; label: string; icon: string; aside?: ReactNode; end?: boolean }) {
  const Icon = icon(name);
  return (
    <NavLink
      to={to}
      end={end}
      className={({ isActive }) =>
        cx("flex items-center gap-3 rounded-ui px-3 py-2 text-sm", isActive ? "bg-primary text-primary-foreground" : "hover:bg-muted")
      }
    >
      <Icon className="h-4 w-4" aria-hidden />
      {label}
      {aside && <span className="ml-auto flex items-center">{aside}</span>}
    </NavLink>
  );
}

function NavBadge({ useBadge }: { useBadge: NonNullable<NavItem["useBadge"]> }) {
  const badge = useBadge();
  return badge ? <Badge tone={badge.tone}>{"text" in badge ? badge.text : badge.count}</Badge> : null;
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
  return <PageHeader title="Not found" subtitle="No installed Plugin has a Page here." />;
}

