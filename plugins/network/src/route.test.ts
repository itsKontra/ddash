import { create } from "@bufbuild/protobuf";
import { timestampFromMs } from "@bufbuild/protobuf/wkt";
import { GetVpnTunnelResponseSchema, RemoteAccessPeerSchema, Tier, TunnelGatewaySchema } from "@ddash/proto/network/network_pb";
import { GatewaySchema } from "@ddash/proto/pfsense-api/pfsense_api_pb";
import { expect, it } from "vitest";
import { lastSeen, latency, routeOut, wanDown, wanGateways } from "./route";

const member = (name: string, tier: number, up: boolean, delayMs?: number, lossPercent?: number) =>
  create(TunnelGatewaySchema, { name, tier, up, status: up ? "online" : "down", delayMs, lossPercent });

const tunnel = (tier: Tier, wgUp: boolean, ovpnUp: boolean) =>
  create(GetVpnTunnelResponseSchema, {
    tier,
    group: "VPN_FAILOVER",
    gateways: [member("WG_VPN", 1, wgUp, wgUp ? 16.2 : undefined, wgUp ? 0 : 100), member("OVPN_VPN", 2, ovpnUp, ovpnUp ? 33.2 : undefined, ovpnUp ? 0 : 100)],
  });

const gateway = (name: string, status: string, delayMs?: number, lossPercent?: number) => create(GatewaySchema, { name, status, delayMs, lossPercent });

const gateways = [gateway("WG_VPN", "online", 16.2, 0), gateway("OVPN_VPN", "online", 33.2, 0), gateway("WANGW", "online", 4.8, 0)];

it("takes the WAN's gateways to be those outside the VPN Tunnel's group, and none while the group isn't read", () => {
  expect(wanGateways(tunnel(Tier.WIREGUARD, true, true), gateways).map((g) => g.name)).toEqual(["WANGW"]);
  expect(wanGateways(create(GetVpnTunnelResponseSchema, { error: "pfSense unreachable" }), gateways)).toEqual([]);
});

it("has the WAN down only when every gateway pfSense monitors on it is down", () => {
  expect(wanDown([gateway("WAN_DHCP", "down")])).toBe(true);
  expect(wanDown([gateway("WAN_DHCP", "online"), gateway("WAN_DHCP6", "down")])).toBe(false);
  expect(wanDown([gateway("WAN_DHCP", "down"), gateway("WAN_DHCP6", "none")])).toBe(true);
  expect(wanDown([gateway("WAN_DHCP", "none")])).toBe(false);
  expect(wanDown([])).toBe(false);
});

it("says how the network gets out, down only with the WAN down or the VPN Tunnel down without a fallback", () => {
  const wan = [gateway("WANGW", "online")];
  expect(routeOut(tunnel(Tier.WIREGUARD, true, true), wan)).toEqual({ tone: "up", headline: "Online via WireGuard", summary: "Online via WireGuard" });
  expect(routeOut(tunnel(Tier.OPENVPN, false, true), wan)).toEqual({ tone: "warn", headline: "Online via the OpenVPN fallback", summary: "WireGuard is down" });
  expect(routeOut(tunnel(Tier.DOWN, false, false), wan)).toEqual({ tone: "down", headline: "The VPN Tunnel is down", summary: "WireGuard and OpenVPN down" });
  expect(routeOut(tunnel(Tier.DOWN, false, false), [gateway("WANGW", "down")])).toEqual({ tone: "down", headline: "The WAN is down", summary: "No route to the internet" });
  expect(routeOut(tunnel(Tier.WIREGUARD, true, true), undefined)).toEqual({ tone: "up", headline: "Online via WireGuard", summary: "Online via WireGuard" });
  expect(routeOut(create(GetVpnTunnelResponseSchema, {}), undefined)).toEqual({ tone: "warn", headline: "The VPN Tunnel isn't read yet", summary: "VPN Tunnel not read yet" });
});

it("gives the active member's and the WAN's latency, then no loss when neither loses packets", () => {
  const wan = [gateway("WANGW", "online", 4.8, 0)];
  expect(latency(tunnel(Tier.WIREGUARD, true, true), wan)).toEqual(["VPN Tunnel 16 ms", "WAN 5 ms", "no loss"]);
  expect(latency(tunnel(Tier.OPENVPN, false, true), wan)).toEqual(["VPN Tunnel 33 ms", "WAN 5 ms", "no loss"]);
});

it("names the loss where there is some, and says what is down", () => {
  expect(latency(tunnel(Tier.WIREGUARD, true, true), [gateway("WANGW", "online", 12.4, 3)])).toEqual(["VPN Tunnel 16 ms", "WAN 12 ms, 3% loss"]);
  expect(latency(tunnel(Tier.DOWN, false, false), [gateway("WANGW", "online", 4.8, 0)])).toEqual(["VPN Tunnel down", "WAN 5 ms", "no loss"]);
  expect(latency(tunnel(Tier.DOWN, false, false), [gateway("WANGW", "down", undefined, 100)])).toEqual(["VPN Tunnel down", "WAN down"]);
});

it("leaves out what isn't read: the WAN until pfSense answers, the VPN Tunnel until network does", () => {
  expect(latency(tunnel(Tier.WIREGUARD, true, true), undefined)).toEqual(["VPN Tunnel 16 ms", "no loss"]);
  expect(latency(create(GetVpnTunnelResponseSchema, {}), undefined)).toEqual([]);
});

it("finds the Remote Access peer that handshook last", () => {
  const peer = (name: string, at?: number) => create(RemoteAccessPeerSchema, { name, latestHandshake: at === undefined ? undefined : timestampFromMs(at) });
  expect(lastSeen([peer("Phone", 1_000), peer("Laptop", 5_000), peer("Tablet")])?.name).toBe("Laptop");
  expect(lastSeen([peer("Tablet")])).toBeUndefined();
  expect(lastSeen([])).toBeUndefined();
});
