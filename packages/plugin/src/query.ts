import { getOption, hasOption, type DescMessage, type DescMethodUnary, type MessageInitShape } from "@bufbuild/protobuf";
import { useQuery as useConnectQuery } from "@connectrpc/connect-query";
import { QueryClient } from "@tanstack/react-query";

export type { QueryClient };
import { getter, Getter_Refresh } from "@ddash/proto/ddash/options_pb";

export { Getter_Refresh as Refresh };

/** How often each refresh tier refetches while on screen; NONE never does. */
export const tiers: Record<Getter_Refresh, number | false> = {
  [Getter_Refresh.LIVE]: 5_000,
  [Getter_Refresh.FAST]: 15_000,
  [Getter_Refresh.SLOW]: 60_000,
  [Getter_Refresh.NONE]: false,
};

/**
 * The one QueryClient the shell owns. Queries don't retry (the next tick
 * is the retry, and the last good data stays next to the error), don't
 * poll while the tab is hidden, and refetch when it shows again or the
 * browser reconnects.
 */
export function createQueryClient(): QueryClient {
  return new QueryClient({
    defaultOptions: {
      queries: {
        retry: false,
        refetchIntervalInBackground: false,
        refetchOnWindowFocus: true,
        refetchOnReconnect: true,
      },
    },
  });
}

/** The refresh tier a getter declares. It throws for an Action. */
export function refreshOf(method: DescMethodUnary): Getter_Refresh {
  if (!hasOption(method, getter)) {
    throw new Error(`${method.parent.typeName}.${method.name} is an Action, not a getter: call it with useAction`);
  }
  return getOption(method, getter).refresh;
}

export interface QueryOptions {
  /** Overrides the getter's tier, in exceptional cases only. */
  refresh?: Getter_Refresh;
}

/**
 * Reads a getter of one of the Plugin's Components, refetching on the
 * getter's refresh tier while mounted. Hooks with the same getter and
 * request share one query.
 */
export function useQuery<I extends DescMessage, O extends DescMessage>(
  method: DescMethodUnary<I, O>,
  request: MessageInitShape<I>,
  options: QueryOptions = {},
) {
  return useConnectQuery(method, request, { refetchInterval: tiers[options.refresh ?? refreshOf(method)] });
}
