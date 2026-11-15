import { timestampMs } from "@bufbuild/protobuf/wkt";
import { Tier, type GetVpnTunnelResponse, type RemoteAccessPeer } from "@ddash/proto/network/network_pb";
import type { Gateway } from "@ddash/proto/pfsense-api/pfsense_api_pb";
import type { Tone } from "@ddash/ui";
import { members, ms } from "./tunnel";

/**
 * The WAN's gateways: the firewall's gateways outside the VPN Tunnel's
 * failover group. None while the group isn't read, since then nothing
 * tells the two apart.
 */
export function wanGateways(tunnel: GetVpnTunnelResponse, gateways: readonly Gateway[]): Gateway[] {
  if (tunnel.gateways.length === 0) return [];
  const group = new Set(tunnel.gateways.map((g) => g.name));
  return gateways.filter((g) => !group.has(g.name));
}

/** Down when every WAN gateway pfSense monitors is down; not down while none is monitored. */
export function wanDown(wan: readonly Gateway[]): boolean {
  const monitored = wan.filter((g) => g.status !== "none");
  return monitored.length > 0 && monitored.every((g) => g.status === "down");
}

/**
 * How the home network gets out: a tone, the sentence that says it, and
 * the status summary, which says why when it isn't up (the Widget's
 * header shows it above the headline, on one line).
 */
export interface Route {
  tone: Tone;
  headline: string;
  summary: string;
}

/**
 * The route out. Down only when the network is: the WAN is down, or the
 * VPN Tunnel is down with no fallback left. On the OpenVPN fallback it is
 * online, but warn. wan is undefined while pfSense's gateways aren't read.
 */
export function routeOut(tunnel: GetVpnTunnelResponse, wan: readonly Gateway[] | undefined): Route {
  if (wan && wanDown(wan)) return { tone: "down", headline: "The WAN is down", summary: "No route to the internet" };
  switch (tunnel.tier) {
    case Tier.WIREGUARD:
      return { tone: "up", headline: "Online via WireGuard", summary: "Online via WireGuard" };
    case Tier.OPENVPN:
      return { tone: "warn", headline: "Online via the OpenVPN fallback", summary: "WireGuard is down" };
    case Tier.DOWN:
      return { tone: "down", headline: "The VPN Tunnel is down", summary: "WireGuard and OpenVPN down" };
    default:
      return { tone: "warn", headline: "The VPN Tunnel isn't read yet", summary: "VPN Tunnel not read yet" };
  }
}

/** A latency in whole ms, with the loss when there is any. */
function delay(g: { delayMs?: number; lossPercent?: number }): string {
  const loss = g.lossPercent ? `, ${Math.round(g.lossPercent)}% loss` : "";
  return `${ms(g.delayMs, 0)}${loss}`;
}

/**
 * The line under the headline, as its parts: the VPN Tunnel's active
 * member and the WAN (its first online gateway), each with its latency
 * (and loss, when there is any), then "no loss" when neither loses
 * packets. E.g. ["VPN Tunnel 16 ms", "WAN 5 ms", "no loss"].
 */
export function latency(tunnel: GetVpnTunnelResponse, wan: readonly Gateway[] | undefined): string[] {
  const parts: string[] = [];
  const losses: (number | undefined)[] = [];
  if (tunnel.tier !== Tier.UNSPECIFIED) {
    const active = members(tunnel).find((m) => m.state === "active")?.gateway;
    parts.push(active ? `VPN Tunnel ${delay(active)}` : "VPN Tunnel down");
    if (active) losses.push(active.lossPercent);
  }
  const monitored = wan?.find((g) => g.status === "online") ?? wan?.find((g) => g.status !== "none");
  if (monitored) {
    parts.push(monitored.status === "down" ? "WAN down" : `WAN ${delay(monitored)}`);
    if (monitored.status !== "down") losses.push(monitored.lossPercent);
  }
  if (losses.length > 0 && losses.every((l) => l === 0)) parts.push("no loss");
  return parts;
}

/** The Remote Access peer that handshook last, and when; undefined when none ever did. */
export function lastSeen(peers: readonly RemoteAccessPeer[]): RemoteAccessPeer | undefined {
  let last: RemoteAccessPeer | undefined;
  for (const p of peers) {
    if (p.latestHandshake && (!last?.latestHandshake || timestampMs(p.latestHandshake) > timestampMs(last.latestHandshake))) last = p;
  }
  return last;
}
