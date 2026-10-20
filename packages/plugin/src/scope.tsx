import type { DescService } from "@bufbuild/protobuf";
import { createClient, type Client } from "@connectrpc/connect";
import { TransportProvider, useTransport } from "@connectrpc/connect-query";
import { QueryClientProvider, type QueryClient } from "@tanstack/react-query";
import { ConfirmDialog, type ConfirmAnswer, type ConfirmRequest } from "@ddash/ui";
import { createContext, useCallback, useContext, useMemo, useRef, useState, type ReactNode } from "react";
import { coreTransport, pluginTransport, type Fetch, type PluginRef } from "./transport";

/**
 * The shell wraps its whole tree in PluginHost, with its one QueryClient.
 * It draws the standard confirm dialog for every disruptive Action.
 */
export function PluginHost({ client, children }: { client: QueryClient; children: ReactNode }) {
  const [asking, setAsking] = useState<Question | null>(null);
  const current = useRef<Question | null>(null);
  const ask = useCallback(
    (request: ConfirmRequest) =>
      new Promise<ConfirmAnswer>((resolve) => {
        current.current?.answer(null); // one question at a time: a newer one cancels it
        current.current = { request, answer: resolve };
        setAsking(current.current);
      }),
    [],
  );
  return (
    <QueryClientProvider client={client}>
      <ConfirmContext.Provider value={ask}>
        {children}
        {asking && (
          <ConfirmDialog
            request={asking.request}
            onAnswer={(a) => {
              asking.answer(a);
              current.current = null;
              setAsking(null);
            }}
          />
        )}
      </ConfirmContext.Provider>
    </QueryClientProvider>
  );
}

type Question = { request: ConfirmRequest; answer: (a: ConfirmAnswer) => void };

/** Asks the Operator to confirm, through PluginHost's dialog. */
export type Ask = (request: ConfirmRequest) => Promise<ConfirmAnswer>;

const ConfirmContext = createContext<Ask | null>(null);

/** PluginHost's confirm dialog. */
export function useConfirm(): Ask {
  const ask = useContext(ConfirmContext);
  if (!ask) throw new Error("useConfirm: not inside PluginHost");
  return ask;
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
