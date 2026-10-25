import { cleanup, screen, within } from "@testing-library/react";
import { pending, showWidget, unavailable, type Answer } from "@ddash/plugin/testing";
import { afterEach, expect, it } from "vitest";
import plugin from "./index";
import { network } from "./testing";

afterEach(cleanup);

const wg = (up: boolean) => ({ name: "WG_VPN", tier: 1, up, status: up ? "online" : "down", substatus: up ? "none" : "highloss", delayMs: up ? 24.1 : 0, lossPercent: up ? 0 : 100 });
const ovpn = (up: boolean) => ({ name: "OVPN_VPN", tier: 2, up, status: up ? "online" : "down", substatus: up ? "none" : "highloss", delayMs: up ? 33.2 : 0, lossPercent: up ? 0 : 100 });

// What network answers GetVpnTunnel with (Connect JSON).
const tunnel = (tier: string, wgUp: boolean, ovpnUp: boolean) => ({ tier, group: "VPN_FAILOVER", gateways: [wg(wgUp), ovpn(ovpnUp)] });

const tier = (kind: string) => within(screen.getByRole("region", { name: "widget" })).getByText(kind).closest("li")!;

const vpn = () => plugin.widgets!.find((w) => w.id === "vpn")!;

// Renders the Widget's body against network answering with answer, and
// returns what its useStatus reported last.
const show = (answer: Answer) => showWidget(network, vpn(), { answers: { GetVpnTunnel: answer } });

it("is the vpn Widget, S only", () => {
  const w = vpn();
  expect([w.title, w.sizes, w.defaultSize]).toEqual(["VPN Tunnel", ["S"], "S"]);
});

it("asks network for the VPN Tunnel", async () => {
  const { calls } = await show(() => Response.json(tunnel("TIER_WIREGUARD", true, true)));

  expect(calls[0].url).toBe("/api/c/network/ddash.network.NetworkService/GetVpnTunnel");
});

it("reports up via WireGuard, and shows WireGuard active with the fallback standing by", async () => {
  const { reported } = await show(() => Response.json(tunnel("TIER_WIREGUARD", true, true)));

  expect(reported.status).toEqual({ tone: "up", summary: "Via WireGuard" });
  expect(within(tier("WireGuard")).getByRole("img", { name: "up" })).toBeTruthy();
  expect(tier("WireGuard").textContent).toMatch(/active · 24 ms/);
  expect(tier("OpenVPN").textContent).toMatch(/standby/);
});

it("reports warn on the OpenVPN fallback", async () => {
  const { reported } = await show(() => Response.json(tunnel("TIER_OPENVPN", false, true)));

  expect(reported.status).toEqual({ tone: "warn", summary: "Via the OpenVPN fallback: WireGuard is down" });
  expect(within(tier("WireGuard")).getByRole("img", { name: "down" })).toBeTruthy();
  expect(tier("OpenVPN").textContent).toMatch(/active · 33 ms/);
});

it("reports down when both tiers are down", async () => {
  const { reported } = await show(() => Response.json(tunnel("TIER_DOWN", false, false)));

  expect(reported.status).toEqual({ tone: "down", summary: "Down: WireGuard and the OpenVPN fallback are both down" });
});

it("reports warn when network can't read the gateway group", async () => {
  const { reported } = await show(() => Response.json({ error: "can't read the gateways: pfSense unreachable" }));

  expect(reported.status).toEqual({ tone: "warn", summary: "VPN Tunnel unknown: can't read the gateways: pfSense unreachable" });
  expect(within(screen.getByRole("region", { name: "widget" })).getByRole("status").textContent).toMatch(/pfSense unreachable/);
});

it("reports warn when network is unavailable", async () => {
  const { reported } = await show(() => unavailable("network is down"));

  expect(reported.status).toEqual({ tone: "warn", summary: "network unavailable: network is down" });
});

it("reports nothing until network first answers", async () => {
  const { reported } = await show(pending);

  expect(reported.status).toBeUndefined();
  expect(within(screen.getByRole("region", { name: "widget" })).getByRole("status", { name: "Loading" })).toBeTruthy();
});
