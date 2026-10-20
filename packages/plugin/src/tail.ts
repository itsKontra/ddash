import type { DescMessage, DescMethodUnary, MessageInitShape, MessageShape } from "@bufbuild/protobuf";
import type { ConnectError } from "@connectrpc/connect";
import { callUnaryMethod, createConnectQueryKey, useTransport } from "@connectrpc/connect-query";
import { useQuery as useTanstackQuery, useQueryClient, type UseQueryResult } from "@tanstack/react-query";
import { useEffect, useMemo, useRef } from "react";
import { refreshOf, tiers, type QueryOptions } from "./query";

/** How useTail asks for what's new and folds it into what it has. */
export interface Tail<I extends DescMessage, O extends DescMessage, D> {
  /** The request, from the data so far (undefined before the first answer). */
  request: (data: D | undefined) => MessageInitShape<I>;
  /** The data with an answer folded in, e.g. new samples appended and old ones dropped. */
  merge: (data: D | undefined, next: MessageShape<O>) => D;
}

/**
 * Reads a getter that answers only what's new since a point, keeping all
 * it was answered: the first call gets the whole history, every later one
 * (on the getter's tier, like useQuery) only what was added since the data
 * so far. Hooks with the same getter share one tail; a failed call keeps
 * the data next to the error.
 */
export function useTail<I extends DescMessage, O extends DescMessage, D>(
  method: DescMethodUnary<I, O>,
  tail: Tail<I, O, D>,
  options: QueryOptions = {},
): UseQueryResult<D, ConnectError> {
  const transport = useTransport();
  const client = useQueryClient();
  const latest = useRef(tail);
  useEffect(() => {
    latest.current = tail;
  });
  const queryKey = useMemo(() => [...createConnectQueryKey({ schema: method, transport, cardinality: "finite" }), "tail"], [method, transport]);
  return useTanstackQuery<D, ConnectError>({
    queryKey,
    queryFn: async ({ signal }) => {
      const data = client.getQueryData<D>(queryKey);
      const next = await callUnaryMethod(transport, method, latest.current.request(data), { signal });
      return latest.current.merge(data, next);
    },
    refetchInterval: tiers[options.refresh ?? refreshOf(method)],
    structuralSharing: false,
  });
}
