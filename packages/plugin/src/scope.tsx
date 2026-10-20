import type { DescService } from "@bufbuild/protobuf";
import { createClient, type Client } from "@connectrpc/connect";
import { TransportProvider, useTransport } from "@connectrpc/connect-query";
import { QueryClientProvider, type QueryClient } from "@tanstack/react-query";
import { createContext, useContext, useMemo, type ReactNode } from "react";
import { coreTransport, pluginTransport, type Fetch, type PluginRef } from "./transport";

/** The shell wraps its whole tree in PluginHost, with its one QueryClient. */
export function PluginHost({ client, children }: { client: QueryClient; children: ReactNode }) {
  return <QueryClientProvider client={client}>{children}</QueryClientProvider>;
}

const PluginContext = createContext<PluginRef | null>(null);

/**
 * The shell wraps each Plugin's Pages, badge and Widgets in PluginScope, so
 * their calls go out on that Plugin's own transport.
 */
export function PluginScope({ plugin, fetch, children }: { plugin: PluginRef; fetch?: Fetch; children: ReactNode }) {
  // By value, so a re-render with an equal plugin keeps the transport.
  const requires = plugin.requires.join(",");
  const ref = useMemo(() => ({ name: plugin.name, requires: requires ? requires.split(",") : [] }), [plugin.name, requires]);
  const transport = useMemo(() => pluginTransport(ref, fetch), [ref, fetch]);
  return (
    <PluginContext.Provider value={ref}>
      <TransportProvider transport={transport}>{children}</TransportProvider>
    </PluginContext.Provider>
  );
}

/** The shell wraps its own UI in CoreScope, so its calls go to the core API. */
export function CoreScope({ fetch, children }: { fetch?: Fetch; children: ReactNode }) {
  const transport = useMemo(() => coreTransport(fetch), [fetch]);
  return <TransportProvider transport={transport}>{children}</TransportProvider>;
}

/** The Plugin whose UI is rendering. */
export function usePlugin(): PluginRef {
  const p = useContext(PluginContext);
  if (!p) throw new Error("usePlugin: not inside a Plugin's PluginScope");
  return p;
}

/** A client for one of the Plugin's Components' services. */
export function useClient<S extends DescService>(service: S): Client<S> {
  const transport = useTransport();
  return useMemo(() => createClient(service, transport), [service, transport]);
}
