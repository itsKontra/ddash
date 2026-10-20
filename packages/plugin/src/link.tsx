import { createContext, useContext, useMemo, type ReactNode } from "react";

const InstalledContext = createContext<ReadonlySet<string> | undefined>(undefined);

/**
 * The shell wraps its tree in InstalledPlugins with the installed Plugins'
 * names (undefined until the core first answered), so usePluginLink knows
 * which Plugins have Pages.
 */
export function InstalledPlugins({ names, children }: { names: readonly string[] | undefined; children: ReactNode }) {
  const key = names?.join(",");
  const installed = useMemo(() => (key === undefined ? undefined : new Set(key ? key.split(",") : [])), [key]);
  return <InstalledContext.Provider value={installed}>{children}</InstalledContext.Provider>;
}

/**
 * The URL of another Plugin's Page, path relative to its root (e.g.
 * `usePluginLink("docker", "hosts/media")` → "/docker/hosts/media"), or
 * null when that Plugin isn't installed. Only URLs pass between Plugins,
 * never data.
 */
export function usePluginLink(plugin: string, path = ""): string | null {
  const installed = useContext(InstalledContext);
  if (!installed?.has(plugin)) return null;
  const rest = path.replace(/^\/+/, "");
  return rest ? `/${plugin}/${rest}` : `/${plugin}`;
}
