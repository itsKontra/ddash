import { useQuery } from "@ddash/plugin";
import { NetworkService, Tier, type GetVpnTunnelResponse, type TunnelGateway } from "@ddash/proto/network/network_pb";
import type { Tone } from "@ddash/ui";

/** network's read of the VPN Tunnel, shared by the Page and the Widget. */
export function useTunnel() {
  return useQuery(NetworkService.method.getVpnTunnel, {});
}

/** How each tier reads, and its tone. */
export const tiers: Record<Tier, { label: string; tone: Tone }> = {
  [Tier.UNSPECIFIED]: { label: "Unknown", tone: "warn" },
  [Tier.WIREGUARD]: { label: "Via WireGuard", tone: "up" },
  [Tier.OPENVPN]: { label: "Via the OpenVPN fallback", tone: "warn" },
  [Tier.DOWN]: { label: "Down", tone: "down" },
};

/** One member of the failover group, as the VPN Tunnel sees it. */
export interface Member {
  gateway: TunnelGateway;
  /** "WireGuard" for the group's first tier, "OpenVPN" for any later one. */
  kind: "WireGuard" | "OpenVPN";
  /** Active carries the Tunnel, standby could, down can't. */
  state: "active" | "standby" | "down";
}

/** The group's members with their kind and state, first tier first. */
export function members(t: GetVpnTunnelResponse): Member[] {
  const first = Math.min(...t.gateways.map((g) => g.tier));
  const activeTier = t.gateways.filter((g) => g.up).reduce((lowest, g) => Math.min(lowest, g.tier), Infinity);
  return t.gateways.map((g) => ({
    gateway: g,
    kind: g.tier === first ? "WireGuard" : "OpenVPN",
    state: !g.up ? "down" : g.tier === activeTier ? "active" : "standby",
  }));
}

export const stateTone: Record<Member["state"], Tone> = { active: "up", standby: "idle", down: "down" };

/** A latency in ms to digits decimals, or a dash when it isn't measured. */
export const ms = (v: number | undefined, digits = 1) => (v === undefined ? "—" : `${v.toFixed(digits)} ms`);

/** A packet loss in percent, or a dash when it isn't measured. */
export const percent = (v: number | undefined) => (v === undefined ? "—" : `${Math.round(v)}%`);
