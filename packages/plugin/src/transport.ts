import { getOption, hasOption, type DescMethod } from "@bufbuild/protobuf";
import { Code, ConnectError, createContextKey, type Interceptor, type Transport } from "@connectrpc/connect";
import { createConnectTransport } from "@connectrpc/connect-web";
import { action } from "@ddash/proto/ddash/options_pb";

/** Whether a method is an Action that declares itself disruptive. */
export function isDisruptive(method: DescMethod): boolean {
  return hasOption(method, action) && getOption(method, action).disruptive;
}

/**
 * The confirm text the Operator agreed to, which useAction and
 * useStreamingAction set on a disruptive call. Empty when there was no
 * confirm.
 */
export const confirmed = createContextKey("", { description: "the confirm text the Operator agreed to" });

/**
 * Refuses a disruptive call that wasn't confirmed, so a missing confirm
 * fails in dev rather than shipping. Streams too.
 */
const requireConfirm: Interceptor = (next) => (req) => {
  if (isDisruptive(req.method) && !req.contextValues.get(confirmed)) {
    throw new ConnectError(
      `${req.method.parent.typeName}.${req.method.name} is disruptive: run it with a confirm text through ${req.stream ? "useStreamingAction" : "useAction"}`,
      Code.FailedPrecondition,
    );
  }
  return next(req);
};

/** The header naming the Plugin a call comes from (router.PluginHeader). */
export const pluginHeader = "Ddash-Plugin";

/** An installed Plugin, as the transport needs it. */
export interface PluginRef {
  name: string;
  /** The Components the Plugin's UI may call. */
  requires: readonly string[];
}

export type Fetch = typeof globalThis.fetch;

/**
 * The transport for one Plugin's calls. Every call names the Plugin in the
 * Ddash-Plugin header and goes to /api/c/<component>/<package.Service>/<Method>,
 * where component is the one among the Plugin's requires whose name, without
 * hyphens, is the service's package after "ddash." (docker-api serves
 * ddash.dockerapi). A server-streaming Action goes the same way.
 */
export function pluginTransport(plugin: PluginRef, fetch?: Fetch): Transport {
  const naming: Interceptor = (next) => (req) => {
    req.header.set(pluginHeader, plugin.name);
    return next(req);
  };
  const transports = new Map<string, Transport>();
  const forService = (typeName: string): Transport => {
    const pkg = typeName.slice(0, typeName.lastIndexOf("."));
    const component = plugin.requires.find((c) => `ddash.${c.replaceAll("-", "")}` === pkg);
    if (component === undefined) {
      throw new ConnectError(
        `plugin ${plugin.name} doesn't require a Component serving ${typeName}`,
        Code.PermissionDenied,
      );
    }
    let t = transports.get(component);
    if (!t) {
      t = createConnectTransport({ baseUrl: `/api/c/${component}`, fetch, interceptors: [requireConfirm, naming] });
      transports.set(component, t);
    }
    return t;
  };
  return {
    async unary(method, ...rest) {
      return forService(method.parent.typeName).unary(method, ...rest);
    },
    async stream(method, ...rest) {
      // connect-web can't send a client stream, and no Component serves one.
      if (method.methodKind !== "server_streaming") {
        throw new ConnectError(
          `${method.parent.typeName}.${method.name} streams from the client; ddash methods are unary or server-streaming`,
          Code.Unimplemented,
        );
      }
      return forService(method.parent.typeName).stream(method, ...rest);
    },
  };
}

/** The transport for the core API at /api/core, which only the shell calls. */
export function coreTransport(fetch?: Fetch): Transport {
  return createConnectTransport({ baseUrl: "/api/core", fetch, interceptors: [requireConfirm] });
}
