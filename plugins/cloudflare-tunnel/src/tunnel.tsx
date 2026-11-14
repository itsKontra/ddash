import { useQuery } from "@ddash/plugin";
import { CloudflareApiService, TunnelStatus, type Tunnel } from "@ddash/proto/cloudflare-api/cloudflare_api_pb";
import { CloudflareTunnelService, DnsState, type PublicHostname } from "@ddash/proto/cloudflare-tunnel/cloudflare_tunnel_pb";
import { EmptyState, Panel, type Tone } from "@ddash/ui";
import { Link } from "react-router";

/** The Tunnel as cloudflare-api reads it, and whether cloudflare-api is set up. */
export function useTunnel() {
  const q = useQuery(CloudflareApiService.method.getTunnel, {});
  // Not set up: an empty config (set_up false), or none saved yet.
  const notSetUp = q.data ? !q.data.setUp : q.error?.rawMessage === "not configured";
  return { ...q, tunnel: q.data?.tunnel, notSetUp };
}

/** The Public Hostnames as cloudflare-tunnel lists them. */
export function usePublicHostnames() {
  return useQuery(CloudflareTunnelService.method.listPublicHostnames, {});
}

/** How the Tunnel's state reads: up, degraded or down. */
export const tunnelLook: Record<TunnelStatus, { tone: Tone; label: string; title: string }> = {
  [TunnelStatus.UNSPECIFIED]: { tone: "idle", label: "Unknown", title: "Cloudflare doesn't say how the Tunnel is" },
  [TunnelStatus.HEALTHY]: { tone: "up", label: "Up", title: "Every connector is connected" },
  [TunnelStatus.DEGRADED]: { tone: "warn", label: "Degraded", title: "Connected, with some connections down" },
  [TunnelStatus.DOWN]: { tone: "down", label: "Down", title: "No connector is connected" },
  [TunnelStatus.INACTIVE]: { tone: "down", label: "Down", title: "No connector has ever connected" },
};

/** The connections of every connector. */
export const connections = (t: Tunnel) => t.connectors.reduce((n, k) => n + k.connections.length, 0);

/** How a Public Hostname's DNS record reads. */
export const dnsLook: Record<DnsState, { tone: Tone; label: string }> = {
  [DnsState.UNSPECIFIED]: { tone: "idle", label: "–" },
  [DnsState.OK]: { tone: "up", label: "Proxied CNAME" },
  [DnsState.NOT_PROXIED]: { tone: "warn", label: "DNS only" },
  [DnsState.MISSING]: { tone: "down", label: "No DNS record" },
  [DnsState.ELSEWHERE]: { tone: "down", label: "Points elsewhere" },
  [DnsState.NO_ZONE]: { tone: "down", label: "In no zone" },
};

/** A Public Hostname as the Operator reads it: its hostname, and its path when it has one. */
export const nameOf = (p: PublicHostname) => (p.fields?.path ? `${p.fields.hostname} (path ${p.fields.path})` : (p.fields?.hostname ?? ""));

/** "Set up the Cloudflare Tunnel", while cloudflare-api has no account, Tunnel and token. */
export function SetUp() {
  return (
    <Panel>
      <EmptyState
        icon="cloud"
        title="Set up the Cloudflare Tunnel"
        action={
          <Link to="/plugins" className="text-sm font-semibold text-primary hover:underline">
            Configure it in Plugins
          </Link>
        }
      >
        Give cloudflare-api the account ID, the Tunnel&apos;s ID and an API token to see its connectors and Public Hostnames.
      </EmptyState>
    </Panel>
  );
}
