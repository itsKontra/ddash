import type { App } from "@ddash/proto/apps/apps_pb";
import type { Check } from "@ddash/proto/checks/checks_pb";
import type { Server, Snapshot } from "@ddash/proto/nginx-config/nginx_config_pb";

/** One Proxy Host: a server block that forwards to an upstream, joined with its App by first server_name. */
export interface ProxyHost {
  /** "<file>:<line>", unique per block. */
  key: string;
  server: Server;
  /** The first server_name, the App's key. */
  name: string;
  /** The proxy_pass of location "/", else of the first location with one. */
  upstream: string;
  /** The servers of the upstream block proxy_pass names; empty when it names none. */
  upstreamServers: string[];
  /** Undefined while apps can't answer, null when apps lists no App for it. */
  app: App | null | undefined;
  /** The App's Check: undefined without an App or while checks can't answer, null when the App has none. */
  check: Check | null | undefined;
  /** What looks wrong, e.g. "no TLS". */
  warnings: string[];
}

/** The warning for an upstream behind no Workload. */
export const noWorkload = "upstream matches no Workload";
/** The warning for a block that listens without ssl only. */
export const noTLS = "no TLS";

/** A warning as the Warnings list says it, e.g. "upstream http://198.18.2.99 matches no Workload". */
export const describe = (host: ProxyHost, warning: string) => (warning === noWorkload ? `upstream ${host.upstream} matches no Workload` : warning);

/** The host part of a proxy_pass target: "http://grafana/" → "grafana". */
const hostOf = (target: string) => target.replace(/^[a-z]+:\/\//i, "").split("/", 1)[0];

function upstreamOf(server: Server): string {
  const locs = server.locations.filter((l) => l.proxyPass);
  return (locs.find((l) => l.path === "/") ?? locs[0])?.proxyPass ?? "";
}

/**
 * Every Proxy Host in the snapshot, by first server_name: blocks without a
 * proxy_pass (redirects, catch-alls returning a status) are none. apps
 * (undefined while it can't answer) gives each its App and Workload,
 * checks (byApp, likewise) the App's Check. A Proxy Host warns when its
 * App has no Workload, and when none of its listens has ssl.
 */
export function proxyHosts(snapshot: Snapshot, apps: readonly App[] | undefined, byApp: ReadonlyMap<string, Check> | undefined): ProxyHost[] {
  const appByName = apps && new Map(apps.filter((a) => a.serverName).map((a) => [a.serverName, a]));
  return snapshot.servers
    .filter((s) => s.locations.some((l) => l.proxyPass))
    .map((server): ProxyHost => {
      const name = server.serverNames[0] ?? "";
      const upstream = upstreamOf(server);
      const app = appByName && (appByName.get(name) ?? null);
      const warnings: string[] = [];
      if (app && !app.workload) warnings.push(noWorkload);
      if (!server.listens.some((l) => l.ssl)) warnings.push(noTLS);
      return {
        key: `${server.file}:${server.line}`,
        server,
        name,
        upstream,
        upstreamServers: snapshot.upstreams.find((u) => u.name === hostOf(upstream))?.servers ?? [],
        app,
        check: app && byApp ? (byApp.get(app.id) ?? null) : undefined,
        warnings,
      };
    })
    .sort((a, b) => a.name.localeCompare(b.name)); // stable: blocks sharing a name keep nginx's order
}
