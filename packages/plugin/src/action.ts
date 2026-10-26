import type { DescMessage, DescMethod, DescMethodServerStreaming, DescMethodUnary, MessageInitShape, MessageShape } from "@bufbuild/protobuf";
import { Code, ConnectError, createContextValues } from "@connectrpc/connect";
import { createAsyncIterable } from "@connectrpc/connect/protocol";
import { useTransport } from "@connectrpc/connect-query";
import { useQueryClient } from "@tanstack/react-query";
import type { ConfirmRequest } from "@ddash/ui";
import { useCallback, useEffect, useRef, useState } from "react";
import { useConfirm, type Ask } from "./scope";
import { confirmed, isDisruptive } from "./transport";

export type { ConfirmRequest };

export interface RunOptions {
  /**
   * What the confirm dialog asks before a disruptive Action, e.g.
   * "Stop container plex on media?". A disruptive Action run without it is
   * refused by the transport. Ignored for a non-disruptive one.
   */
  confirm?: string | ConfirmRequest;
}

/** The request, or a function of the confirm dialog's checkbox giving it. */
export type ActionRequest<I extends DescMessage> = MessageInitShape<I> | ((answer: { checked: boolean }) => MessageInitShape<I>);

export interface Action<M extends DescMethodUnary> {
  /**
   * Runs the Action, after the Operator confirmed it if it is disruptive.
   * It resolves to the response, or to undefined when the Operator
   * cancelled or the call failed (see error). Success invalidates every
   * query, so what's on screen refetches.
   */
  run: (request: ActionRequest<M["input"]>, options?: RunOptions) => Promise<MessageShape<M["output"]> | undefined>;
  /** A call is in flight. */
  pending: boolean;
  /** Why the last run failed; cleared by the next run. */
  error?: ConnectError;
}

/**
 * Acts through an Action of one of the Plugin's Components (or, in the
 * shell, of the core API):
 *
 *     const stop = useAction(DockerApiService.method.stopContainer);
 *     stop.run({ host, id }, { confirm: `Stop container ${name} on ${host}?` });
 */
export function useAction<I extends DescMessage, O extends DescMessage>(method: DescMethodUnary<I, O>): Action<DescMethodUnary<I, O>> {
  const transport = useTransport();
  const client = useQueryClient();
  const ask = useConfirm();
  const [state, setState] = useState<{ pending: boolean; error?: ConnectError }>({ pending: false });

  const run = useCallback(
    async (request: ActionRequest<I>, options: RunOptions = {}): Promise<MessageShape<O> | undefined> => {
      const call = await confirmFirst(method, ask, request, options);
      if (!call) return undefined;
      setState({ pending: true });
      try {
        const res = await transport.unary(method, undefined, undefined, undefined, call.input, call.values);
        setState({ pending: false });
        await client.invalidateQueries();
        return res.message;
      } catch (err) {
        setState({ pending: false, error: ConnectError.from(err) });
        return undefined;
      }
    },
    [method, transport, client, ask],
  );

  return { run, ...state };
}

export interface StreamingAction<M extends DescMethodServerStreaming> {
  /**
   * Runs the Action as useAction's run does, confirm and all, following its
   * stream. It resolves to the last message, or to undefined when the
   * Operator cancelled, the call failed (see error) or the component
   * unmounted. A clean end invalidates every query, and so does unmounting
   * mid-run, since the Action runs on to its end on the server all the same.
   */
  run: (request: ActionRequest<M["input"]>, options?: RunOptions) => Promise<MessageShape<M["output"]> | undefined>;
  /** The newest message of the current run. It stays after the run ends; the next run clears it. */
  last?: MessageShape<M["output"]>;
  /** A stream is open. */
  pending: boolean;
  /** Why the last run failed, details and all; cleared by the next run. */
  error?: ConnectError;
}

/**
 * Acts through a server-streaming Action of one of the Plugin's Components,
 * showing each message as it comes:
 *
 *     const apply = useStreamingAction(AppsService.method.runAction);
 *     apply.run({ appId, plan });
 *     apply.last?.steps
 */
export function useStreamingAction<I extends DescMessage, O extends DescMessage>(
  method: DescMethodServerStreaming<I, O>,
): StreamingAction<DescMethodServerStreaming<I, O>> {
  const transport = useTransport();
  const client = useQueryClient();
  const ask = useConfirm();
  const [state, setState] = useState<{ pending: boolean; last?: MessageShape<O>; error?: ConnectError }>({ pending: false });
  // Each run's own controller: leaving a for await doesn't abort its fetch.
  const runs = useRef(new Set<AbortController>());
  useEffect(() => {
    const open = runs.current;
    return () => open.forEach((run) => run.abort());
  }, []);

  const run = useCallback(
    async (request: ActionRequest<I>, options: RunOptions = {}): Promise<MessageShape<O> | undefined> => {
      const call = await confirmFirst(method, ask, request, options);
      if (!call) return undefined;
      const abort = new AbortController();
      runs.current.add(abort);
      setState({ pending: true });
      let last: MessageShape<O> | undefined;
      try {
        const res = await transport.stream(method, abort.signal, undefined, undefined, createAsyncIterable([call.input]), call.values);
        for await (const message of res.message) {
          last = message;
          setState({ pending: true, last });
        }
        if (!last) throw new ConnectError(`${method.name} ended without a result`, Code.Internal);
        setState({ pending: false, last });
        await client.invalidateQueries();
        return last;
      } catch (err) {
        // Unmounted mid-run: cancelling isn't failing.
        if (abort.signal.aborted) {
          await client.invalidateQueries();
          return undefined;
        }
        setState({ pending: false, last, error: ConnectError.from(err) });
        return undefined;
      } finally {
        runs.current.delete(abort);
      }
    },
    [method, transport, client, ask],
  );

  return { run, ...state };
}

/**
 * Asks the Operator first when the Action is disruptive and has a confirm,
 * and gives the request and the call's context values, or undefined when
 * they cancelled.
 */
async function confirmFirst<I extends DescMessage>(method: DescMethod, ask: Ask, request: ActionRequest<I>, options: RunOptions) {
  const confirm = typeof options.confirm === "string" ? { text: options.confirm } : options.confirm;
  let answer = { checked: false };
  const values = createContextValues();
  if (isDisruptive(method) && confirm) {
    const a = await ask(confirm);
    if (!a) return undefined;
    answer = a;
    values.set(confirmed, confirm.text);
  }
  return { input: typeof request === "function" ? request(answer) : request, values };
}
