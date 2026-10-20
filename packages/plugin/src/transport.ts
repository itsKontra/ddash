import { Code, ConnectError, type Interceptor, type Transport } from "@connectrpc/connect";
import { createConnectTransport } from "@connectrpc/connect-web";

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
 * ddash.dockerapi).
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
      t = createConnectTransport({ baseUrl: `/api/c/${component}`, fetch, interceptors: [naming] });
      transports.set(component, t);
    }
    return t;
  };
  return {
    async unary(method, ...rest) {
      return forService(method.parent.typeName).unary(method, ...rest);
    },
    async stream() {
      throw new ConnectError("ddash methods are unary only", Code.Unimplemented);
    },
  };
}

/** The transport for the core API at /api/core, which only the shell calls. */
export function coreTransport(fetch?: Fetch): Transport {
  return createConnectTransport({ baseUrl: "/api/core", fetch });
}
