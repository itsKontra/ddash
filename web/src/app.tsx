import { CoreScope, PluginHost, PluginScope, type Fetch, type PluginDefinition, type QueryClient } from "@ddash/plugin";
import { Badge, cx, PageHeader, StatusDot } from "@ddash/ui";
import type { ReactNode } from "react";
import { NavLink, Route, Routes, useLocation, useParams, useRoutes } from "react-router";
import { ErrorBoundary, ErrorFrame } from "./boundary";
import { icon } from "./icons";
import { navItems, type NavItem } from "./nav";
import { Settings } from "./settings";
import { importBundle as defaultImport, usePlugins, type ImportBundle, type Plugins } from "./plugins";

export interface AppProps {
  /** The shell's one QueryClient (createQueryClient()). */
  client: QueryClient;
  /** Replaces window.fetch for every call, in tests. */
  fetch?: Fetch;
  /** Replaces the dynamic import of bundles, in tests. */
  importBundle?: ImportBundle;
}

/** The shell, inside a router. */
export function App({ client, fetch, importBundle = defaultImport }: AppProps) {
  return (
    <PluginHost client={client}>
      <CoreScope fetch={fetch}>
        <Shell fetch={fetch} importBundle={importBundle} />
      </CoreScope>
    </PluginHost>
  );
}

function Shell({ fetch, importBundle }: { fetch?: Fetch; importBundle: ImportBundle }) {
  const plugins = usePlugins(importBundle);
  return (
    <div className="flex min-h-screen bg-background font-sans text-foreground">
      <Sidebar items={navItems(plugins.loaded.values())} fetch={fetch} />
      <main className="min-w-0 flex-1 p-8">
        <Routes>
          <Route index element={<PageHeader title="Overview" />} />
          <Route path="plugins" element={<Installed plugins={plugins} />} />
          <Route path="settings/*" element={<Settings />} />
          <Route path=":plugin/*" element={<PluginPage plugins={plugins} fetch={fetch} />} />
        </Routes>
      </main>
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
    </aside>
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
  return badge ? <Badge tone={badge.tone}>{badge.count}</Badge> : null;
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

/** The installed Plugins, until the Catalogue replaces this Page. */
function Installed({ plugins }: { plugins: Plugins }) {
  return (
    <div>
      <PageHeader title="Plugins" subtitle="Installed Plugins." />
      <ul className="divide-y divide-border rounded-ui border border-border bg-card">
        {plugins.installed?.map((p) => (
          <li key={p.name} className="px-4 py-3">
            <div className="font-medium">{p.title}</div>
            <div className="text-sm text-muted-foreground">{p.description}</div>
          </li>
        ))}
      </ul>
    </div>
  );
}

