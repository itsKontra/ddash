import type { Status, Widget } from "@ddash/plugin";
import { Tier } from "@ddash/proto/network/network_pb";
import { StatusDot } from "@ddash/ui";
import { members, ms, stateTone, tiers, useTunnel } from "./tunnel";

/**
 * Up via WireGuard, warn on the OpenVPN fallback, down when both tiers are
 * down. Warn when network can't read the group or is unavailable.
 */
function useStatus(): Status | undefined {
  const { data, error } = useTunnel();
  if (error) return { tone: "warn", summary: `network unavailable: ${error.rawMessage}` };
  if (!data) return undefined;
  switch (data.tier) {
    case Tier.WIREGUARD:
      return { tone: "up", summary: tiers[Tier.WIREGUARD].label };
    case Tier.OPENVPN:
      return { tone: "warn", summary: `${tiers[Tier.OPENVPN].label}: WireGuard is down` };
    case Tier.DOWN:
      return { tone: "down", summary: "Down: WireGuard and the OpenVPN fallback are both down" };
    default:
      return { tone: "warn", summary: `VPN Tunnel unknown: ${data.error || "not read yet"}` };
  }
}

/** S: each tier's state and latency. */
function Body() {
  const { data } = useTunnel();
  if (!data) return null;
  if (data.tier === Tier.UNSPECIFIED) return <p className="text-sm text-warn-text">{data.error || "Not read yet"}</p>;
  return (
    <ul className="flex flex-col gap-2 text-sm">
      {members(data).map((m) => (
        <li key={m.gateway.name} aria-label={m.kind} className="flex items-center gap-2">
          <StatusDot tone={stateTone[m.state]} label={m.state} />
          <span className="font-medium">{m.kind}</span>
          <span className="ml-auto text-muted-foreground tabular-nums">
            {m.state}
            {m.state === "active" && ` · ${ms(m.gateway.delayMs, 0)}`}
          </span>
        </li>
      ))}
    </ul>
  );
}

/** The Overview Widget: which tier carries the VPN Tunnel. */
export const vpn: Widget = {
  id: "vpn",
  title: "VPN Tunnel",
  sizes: ["S"],
  defaultSize: "S",
  Body,
  useStatus,
};
