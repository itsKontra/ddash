import { cleanup, fireEvent, screen, within } from "@testing-library/react";
import { metricValue, pending, settle, showWidget, unavailable, type Answers } from "@ddash/plugin/testing";
import { afterEach, expect, it } from "vitest";
import plugin from "./index";
import { withAdguard } from "./testing";

afterEach(cleanup);

// What network and pfsense-api answer (Connect JSON).
const member = (name: string, tier: number, up: boolean, delayMs: number) => ({ name, tier, up, status: up ? "online" : "down", delayMs, lossPercent: up ? 0 : 100 });
const tunnel = (tier: string, wgUp: boolean, ovpnUp: boolean) => ({
  tier,
  group: "VPN_FAILOVER",
  gateways: [member("WG_VPN", 1, wgUp, 16.2), member("OVPN_VPN", 2, ovpnUp, 33.2)],
});
const gateway = (name: string, up: boolean, delayMs: number) => ({ name, status: up ? "online" : "down", substatus: up ? "none" : "highloss", delayMs, lossPercent: up ? 0 : 100 });
const gateways = (wanUp = true, wgUp = true, ovpnUp = true) => ({
  gateways: [gateway("WG_VPN", wgUp, 16.2), gateway("OVPN_VPN", ovpnUp, 33.2), gateway("WANGW", wanUp, 4.8)],
});

const sample = (secs: number, wanIn: number, wanOut: number) => ({
  at: new Date(Date.now() - (60 - secs) * 1000).toISOString(),
  wanInBytesPerSecond: wanIn,
  wanOutBytesPerSecond: wanOut,
  lanInBytesPerSecond: 0,
  lanOutBytesPerSecond: 0,
});
const throughput = { samples: [sample(0, 10_000, 20_000), sample(5, 14_500, 27_000)] };

const device = (mac: string, extra: Record<string, unknown>) => ({ mac, ip: "198.18.1.20", ...extra });
const devices = {
  devices: [
    device("02:5e:00:00:10:01", { adopted: true, online: true }),
    device("02:5e:00:00:10:02", { adopted: true, online: true }),
    device("02:5e:00:00:10:03", { adopted: true }),
    device("02:5e:00:00:20:01", { online: true }),
  ],
};

const on = { running: true, protectionEnabled: true };
const stats = { dnsQueries: "61122", blockedFiltering: "2445" };

const hoursAgo = (h: number) => new Date(Date.now() - h * 3_600_000 - 5_000).toISOString();
const remoteAccess = {
  tunnel: "tun_wg1",
  peers: [
    { name: "Phone", publicKey: "a", latestHandshake: hoursAgo(29) },
    { name: "Laptop", publicKey: "b", latestHandshake: hoursAgo(50) },
    { name: "Tablet", publicKey: "c" },
  ],
};

const answers = (over: Answers = {}): Answers => ({
  GetVpnTunnel: () => Response.json(tunnel("TIER_WIREGUARD", true, true)),
  ListGateways: () => Response.json(gateways()),
  GetThroughput: () => Response.json(throughput),
  ListDevices: () => Response.json(devices),
  GetStatus: () => Response.json(on),
  GetStats: () => Response.json(stats),
  GetRemoteAccess: () => Response.json(remoteAccess),
  ...over,
});

const widget = () => plugin.widgets!.find((w) => w.id === "network")!;

const show = (over?: Answers, size?: "S" | "M") => showWidget(withAdguard, widget(), { answers: answers(over), size });

const body = () => screen.getByRole("region", { name: "widget" });

it("is the Network Widget, S or M, first among network's Widgets", () => {
  const w = widget();
  expect([w.title, w.sizes, w.defaultSize, w.defaultOrder]).toEqual(["Network", ["S", "M"], "M", 30]);
  expect(plugin.widgets![0]).toBe(w);
});

it("says the network is online via WireGuard, with the VPN Tunnel's and the WAN's latency and no loss", async () => {
  const { reported } = await show();

  expect(reported.status).toEqual({ tone: "up", summary: "Online via WireGuard" });
  expect(within(body()).getByText("Online via WireGuard")).toBeTruthy();
  expect(within(body()).getByText("Online via WireGuard").nextElementSibling!.textContent).toBe("VPN Tunnel 16 ms · WAN 5 ms · no loss");
});

it("warns while online via the OpenVPN fallback, saying WireGuard is down", async () => {
  const { reported } = await show({
    GetVpnTunnel: () => Response.json(tunnel("TIER_OPENVPN", false, true)),
    ListGateways: () => Response.json(gateways(true, false, true)),
  });

  expect(reported.status).toEqual({ tone: "warn", summary: "WireGuard is down" });
  expect(within(body()).getByText("Online via the OpenVPN fallback").nextElementSibling!.textContent).toBe("VPN Tunnel 33 ms · WAN 5 ms · no loss");
});

it("is down when the VPN Tunnel is down with no fallback left", async () => {
  const { reported } = await show({
    GetVpnTunnel: () => Response.json(tunnel("TIER_DOWN", false, false)),
    ListGateways: () => Response.json(gateways(true, false, false)),
  });

  expect(reported.status).toEqual({ tone: "down", summary: "WireGuard and OpenVPN down" });
  expect(within(body()).getByText("The VPN Tunnel is down").nextElementSibling!.textContent).toBe("VPN Tunnel down · WAN 5 ms · no loss");
});

it("is down when the WAN is down", async () => {
  const { reported } = await show({
    GetVpnTunnel: () => Response.json(tunnel("TIER_DOWN", false, false)),
    ListGateways: () => Response.json(gateways(false, false, false)),
  });

  expect(reported.status).toEqual({ tone: "down", summary: "No route to the internet" });
  expect(within(body()).getByText("The WAN is down")).toBeTruthy();
});

it("still says the route out while pfSense's gateways can't be read", async () => {
  const { reported } = await show({ ListGateways: () => unavailable("pfSense unreachable") });

  expect(reported.status).toEqual({ tone: "up", summary: "Online via WireGuard" });
});

it("warns when network is unavailable", async () => {
  const { reported } = await show({ GetVpnTunnel: () => unavailable("network unreachable") });

  expect(reported.status).toEqual({ tone: "warn", summary: "network unavailable: network unreachable" });
});

it("draws the WAN's last hour with its current download and upload", async () => {
  await show();

  expect(within(body()).getByRole("img", { name: "WAN over the last hour" })).toBeTruthy();
  expect(within(body()).getByRole("img", { name: "Download" }).nextElementSibling!.textContent).toBe("116 kbit/s");
  expect(within(body()).getByRole("img", { name: "Upload" }).nextElementSibling!.textContent).toBe("216 kbit/s");
});

it("shows Devices online of all with the New ones, the share of queries blocked with the switch, and the peer seen last", async () => {
  await show();

  expect(metricValue("Devices", body())).toBe("3 of 4 online1 new");
  expect(metricValue("Ad blocking", body())).toBe("4% of 61,122");
  expect(within(body()).getByRole("switch", { name: "AdGuard protection" }).getAttribute("aria-checked")).toBe("true");
  expect(metricValue("Remote Access", body())).toBe("Phone · 29 h ago");
});

it("pauses ad blocking from the switch alone, without the pauses or Resume beside it", async () => {
  let status: object = on;
  const { calls } = await show({
    GetStatus: () => Response.json(status),
    PauseProtection: () => ((status = { ...on, protectionEnabled: false, protectionPausedFor: "600s" }), Response.json({})),
  });

  expect(within(body()).queryByRole("button", { name: /Pause protection/ })).toBeNull();
  fireEvent.click(within(body()).getByRole("switch", { name: "AdGuard protection" }));
  await settle();

  expect(calls.filter((c) => c.method === "PauseProtection").map((c) => c.body)).toEqual([{ duration: "600s" }]);
  expect(metricValue("Ad blocking", body())).toBe("paused 10 min");
  expect(within(body()).queryByRole("button", { name: "Resume" })).toBeNull();
});

it("says when ad blocking is off or AdGuard isn't running", async () => {
  await show({ GetStatus: () => Response.json({ ...on, protectionEnabled: false }) });
  expect(metricValue("Ad blocking", body())).toBe("off");
  cleanup();

  await show({ GetStatus: () => Response.json({ ...on, running: false }) });
  expect(metricValue("Ad blocking", body())).toBe("not running");
  expect(within(body()).queryByRole("switch")).toBeNull();
});

it("leaves the New pill out when there are none, and says when no peer has connected", async () => {
  await show({
    ListDevices: () => Response.json({ devices: devices.devices.slice(0, 3) }),
    GetRemoteAccess: () => Response.json({ ...remoteAccess, peers: [remoteAccess.peers[2]] }),
  });

  expect(metricValue("Devices", body())).toBe("2 of 3 online");
  expect(metricValue("Remote Access", body())).toBe("never connected");
});

it("shows each row loading until it is answered, and unavailable when it can't be", async () => {
  await show({ ListDevices: pending, GetStatus: pending, GetRemoteAccess: pending });
  for (const name of ["Devices", "Ad blocking", "Remote Access"]) expect(metricValue(name, body())).toBeUndefined();
  cleanup();

  await show({ ListDevices: () => unavailable("x"), GetStatus: () => unavailable("x"), GetRemoteAccess: () => unavailable("x") });
  for (const name of ["Devices", "Ad blocking", "Remote Access"]) expect(metricValue(name, body())).toBe("unavailable");
});
