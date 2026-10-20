import type { DescMessage, DescMethodUnary, MessageInitShape, MessageShape } from "@bufbuild/protobuf";
import { ConnectError, createContextValues } from "@connectrpc/connect";
import { useTransport } from "@connectrpc/connect-query";
import { useQueryClient } from "@tanstack/react-query";
import type { ConfirmRequest } from "@ddash/ui";
import { useCallback, useState } from "react";
import { useConfirm } from "./scope";
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
      const confirm = typeof options.confirm === "string" ? { text: options.confirm } : options.confirm;
      let answer = { checked: false };
      const values = createContextValues();
      if (isDisruptive(method) && confirm) {
        const a = await ask(confirm);
        if (!a) return undefined;
        answer = a;
        values.set(confirmed, confirm.text);
      }
      const input = typeof request === "function" ? request(answer) : request;
      setState({ pending: true });
      try {
        const res = await transport.unary(method, undefined, undefined, undefined, input, values);
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
