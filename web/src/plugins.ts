import { useQuery, type Fetch, type PluginDefinition } from "@ddash/plugin";
import { CoreService, type InstalledPlugin } from "@ddash/proto/ddash/core/core_pb";
import { useEffect, useRef, useState } from "react";

/** Imports a bundle's ESM entry; the module's default export is its definePlugin(...). */
export type ImportBundle = (url: string) => Promise<{ default?: unknown }>;

export const importBundle: ImportBundle = (url) => import(/* @vite-ignore */ url);

/** An installed Plugin and what its bundle gave, once it loaded or failed. */
export type LoadedPlugin = InstalledPlugin & {
  definition?: PluginDefinition;
  /** Why the bundle failed to load; such a Plugin is broken. */
  error?: Error;
};

export interface Plugins {
  /** Undefined until the core first answered. */
  installed?: InstalledPlugin[];
  /** The installed Plugins whose bundle has loaded or failed, by name. */
  loaded: Map<string, LoadedPlugin>;
}

/**
 * The installed Plugins, read from the core at the FAST tier. Each new
 * bundle is imported (with its stylesheet) as soon as it's listed, all in
 * parallel, and each is imported only once. import() bypasses fetch, so a
 * bundle that fails to load is asked for again through fetch (the shell's
 * watched one), and a host that fails shows as unreachable.
 */
export function usePlugins(load: ImportBundle, fetch?: Fetch): Plugins {
  const { data } = useQuery(CoreService.method.listInstalledPlugins, {});
  const started = useRef(new Set<string>());
  const [loaded, setLoaded] = useState(() => new Map<string, LoadedPlugin>());

  useEffect(() => {
    for (const p of data?.plugins ?? []) {
      if (started.current.has(p.script)) continue;
      started.current.add(p.script);
      addStylesheet(p.style);
      const settle = (result: Partial<LoadedPlugin>) =>
        setLoaded((prev) => new Map(prev).set(p.name, { ...p, ...result }));
      load(p.script).then(
        (mod) => settle(isDefinition(mod.default) ? { definition: mod.default } : { error: new Error("its bundle has no definePlugin default export") }),
        (err: unknown) => {
          settle({ error: err instanceof Error ? err : new Error(String(err)) });
          fetch?.(p.script, { method: "HEAD" }).catch(() => {});
        },
      );
    }
  }, [data, load, fetch]);

  return { installed: data?.plugins, loaded };
}

function isDefinition(v: unknown): v is PluginDefinition {
  return typeof v === "object" && v !== null;
}

function addStylesheet(href: string) {
  if (!href) return;
  const link = document.createElement("link");
  link.rel = "stylesheet";
  link.href = href;
  document.head.append(link);
}
