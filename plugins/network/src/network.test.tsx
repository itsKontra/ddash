import { act, cleanup, render, screen, within } from "@testing-library/react";
import { createQueryClient, PluginHost, PluginScope } from "@ddash/plugin";
import { afterEach, expect, it, vi } from "vitest";
import plugin from "./index";

afterEach(cleanup);

// What pfsense-api answers ListGateways with (Connect JSON).
const gateways = {
  gateways: [
    { name: "WAN_DHCP", status: "online", substatus: "none", delayMs: 8.412, stddevMs: 0.7, lossPercent: 0 },
    { name: "WG_VPN", status: "down", substatus: "highloss", delayMs: 0, stddevMs: 0, lossPercent: 100 },
    { name: "OVPN_VPN", status: "online", substatus: "loss", delayMs: 33.204, stddevMs: 2.6, lossPercent: 4 },
    { name: "LAB_GW", status: "none", substatus: "none" },
  ],
};

// What network answers GetVpnTunnel with.
const tunnel = {
  tier: "TIER_OPENVPN",
  group: "VPN_FAILOVER",
  gateways: [
    { name: "WG_VPN", tier: 1, up: false, status: "down", substatus: "highloss", delayMs: 0, lossPercent: 100 },
    { name: "OVPN_VPN", tier: 2, up: true, status: "online", substatus: "loss", delayMs: 33.204, lossPercent: 4 },
  ],
};

async function showPage(answers: Record<string, () => Response>) {
  const paths: string[] = [];
  const fetch = vi.fn(async (input: RequestInfo | URL) => {
    const path = new URL(String(input), "http://ddash.test").pathname;
    paths.push(path);
    const method = path.split("/").pop()!;
    return answers[method]();
  });
  const Page = plugin.pages![0].Component!;
  render(
    <PluginHost client={createQueryClient()}>
      <PluginScope plugin={{ name: "network", requires: ["network", "pfsense-api"] }} fetch={fetch}>
        <Page />
      </PluginScope>
    </PluginHost>,
  );
  await act(() => new Promise((r) => setTimeout(r, 50)));
  return paths;
}

it("is the network Nav Entry at order 30 (its icon, network, is in plugin.yaml)", () => {
  expect(plugin.nav?.order).toBe(30);
});

it("shows every gateway with its status, latency and loss, read from pfsense-api", async () => {
  const paths = await showPage({ ListGateways: () => Response.json(gateways), GetVpnTunnel: () => Response.json(tunnel) });

  expect(paths).toContain("/api/c/pfsense-api/ddash.pfsenseapi.PfsenseApiService/ListGateways");
  const table = screen.getByRole("table", { name: "Gateways" });
  const wan = within(table).getByRole("row", { name: /WAN_DHCP/ });
  expect(within(wan).getByRole("img", { name: "online" })).toBeTruthy();
  expect(wan.textContent).toMatch(/8\.4 ms.*0%/);
  const wg = within(table).getByRole("row", { name: /WG_VPN/ });
  expect(within(wg).getByRole("img", { name: "down: highloss" })).toBeTruthy();
  expect(wg.textContent).toMatch(/100%/);
  const ovpn = within(table).getByRole("row", { name: /OVPN_VPN/ });
  expect(within(ovpn).getByRole("img", { name: "online: loss" })).toBeTruthy();
  expect(ovpn.textContent).toMatch(/33\.2 ms.*4%/);
  const lab = within(table).getByRole("row", { name: /LAB_GW/ });
  expect(within(lab).getByRole("img", { name: "not monitored" })).toBeTruthy();
  expect(lab.textContent).toMatch(/—.*—/);
});

it("shows the VPN Tunnel's tier from network, and which gateways carry each tier", async () => {
  const paths = await showPage({ ListGateways: () => Response.json(gateways), GetVpnTunnel: () => Response.json(tunnel) });

  expect(paths).toContain("/api/c/network/ddash.network.NetworkService/GetVpnTunnel");
  const vpn = screen.getByRole("region", { name: "VPN Tunnel" });
  expect(within(vpn).getByText("Via the OpenVPN fallback")).toBeTruthy();
  expect(within(vpn).getByRole("listitem", { name: "WireGuard" }).textContent).toMatch(/WG_VPN.*down/);
  expect(within(vpn).getByRole("listitem", { name: "OpenVPN" }).textContent).toMatch(/OVPN_VPN.*active/);
});

it("says why the tier is unknown when network can't read the group", async () => {
  await showPage({
    ListGateways: () => Response.json(gateways),
    GetVpnTunnel: () => Response.json({ error: "pfSense has no failover gateway group (a group with gateways on two tiers)" }),
  });

  const vpn = screen.getByRole("region", { name: "VPN Tunnel" });
  expect(within(vpn).getByText("Unknown")).toBeTruthy();
  expect(within(vpn).getByText(/no failover gateway group/)).toBeTruthy();
});

it("shows pfsense-api's error in place of the gateways", async () => {
  await showPage({
    ListGateways: () => Response.json({ code: "unavailable", message: "pfSense unreachable: connection refused" }, { status: 503 }),
    GetVpnTunnel: () => Response.json(tunnel),
  });

  const section = screen.getByRole("region", { name: "Gateways" });
  expect(within(section).getByText(/pfSense unreachable/)).toBeTruthy();
});
