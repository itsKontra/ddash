import { act, cleanup, render, screen, within } from "@testing-library/react";
import { createQueryClient, PluginHost, PluginScope } from "@ddash/plugin";
import { MemoryRouter } from "react-router";
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
    return (answers[method] ?? (() => Response.json({})))();
  });
  const Page = plugin.pages![0].Component!;
  render(
    <PluginHost client={createQueryClient()}>
      <PluginScope plugin={{ name: "network", requires: ["network", "pfsense-api"] }} fetch={fetch}>
        <MemoryRouter>
          <Page />
        </MemoryRouter>
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

it("shows WAN and LAN throughput over the last hour, read from network", async () => {
  const at = (secs: number) => new Date(Date.now() - 60_000 + secs * 1000).toISOString();
  const samples = [5, 10].map((secs) => ({
    at: at(secs),
    wanInBytesPerSecond: 2_500_000,
    wanOutBytesPerSecond: 250_000,
    lanInBytesPerSecond: 300_000,
    lanOutBytesPerSecond: 2_400_000,
  }));
  const paths = await showPage({ GetThroughput: () => Response.json({ samples }) });

  expect(paths).toContain("/api/c/network/ddash.network.NetworkService/GetThroughput");
  const section = screen.getByRole("region", { name: "Throughput" });
  expect(within(section).getByRole("listitem", { name: "WAN" }).textContent).toMatch(/↓ 20 Mbit\/s.*↑ 2 Mbit\/s/);
  expect(within(section).getByRole("listitem", { name: "LAN" }).textContent).toMatch(/↓ 19 Mbit\/s.*↑ 2.4 Mbit\/s/);
  expect(within(section).getByRole("img", { name: "WAN over the last hour" })).toBeTruthy();
});

// What network answers GetRemoteAccess with.
const remoteAccess = () => ({
  tunnel: "tun_wg1",
  interface: "REMOTEACCESS",
  peers: [
    {
      name: "Phone",
      publicKey: "cGhvbmUtcGVlci1wdWJsaWMta2V5LWZpeHR1cmUtMDE=",
      addresses: ["198.18.40.2/32"],
      endpoint: "203.0.113.77:41234",
      latestHandshake: new Date(Date.now() - 2 * 60_000 - 5_000).toISOString(),
      rxBytes: "1003948",
      txBytes: "8384712",
      reaches: [
        { destination: "198.18.1.0/24", label: "LAN subnet", protocol: "any", rule: "Phone reaches the LAN" },
        { destination: "198.18.1.20", protocol: "tcp", ports: "443", rule: "Peers reach the reverse proxy" },
      ],
    },
    { name: "", publicKey: "bGFwdG9wLXBlZXItcHVibGljLWtleS1maXh0dXJlLTI=", addresses: ["198.18.40.3/32"] },
  ],
});

it("lists the Remote Access peers with their last handshake, traffic and what each can reach", async () => {
  const paths = await showPage({ GetRemoteAccess: () => Response.json(remoteAccess()) });

  expect(paths).toContain("/api/c/network/ddash.network.NetworkService/GetRemoteAccess");
  const section = screen.getByRole("region", { name: "Remote Access" });
  const table = within(section).getByRole("table", { name: "Remote Access peers" });
  const phone = within(table).getByRole("row", { name: /Phone/ });
  expect(within(phone).getByRole("img", { name: "connected" })).toBeTruthy();
  expect(phone.textContent).toMatch(/198\.18\.40\.2.*2 min ago.*1\.0 MB.*8\.4 MB/);
  const reach = within(phone).getByRole("list", { name: "Phone can reach" });
  expect(within(reach).getAllByRole("listitem").map((li) => li.textContent)).toEqual(["198.18.1.0/24 LAN subnet · any", "198.18.1.20 · tcp 443"]);
  // A peer without a name goes by the start of its key; it never connected and reaches nothing.
  const laptop = within(table).getByRole("row", { name: /bGFwdG9w/ });
  expect(within(laptop).getByRole("img", { name: "never connected" })).toBeTruthy();
  expect(laptop.textContent).toMatch(/never/);
  expect(laptop.textContent).toMatch(/nothing/);
});

it("says why there's no Remote Access", async () => {
  await showPage({ GetRemoteAccess: () => Response.json({ error: "pfSense has no Remote Access tunnel (a WireGuard tunnel on an interface without a gateway)" }) });

  const section = screen.getByRole("region", { name: "Remote Access" });
  expect(within(section).getByText(/no Remote Access tunnel/)).toBeTruthy();
});

it("shows network's error in place of the Remote Access peers", async () => {
  await showPage({ GetRemoteAccess: () => Response.json({ code: "unavailable", message: "can't read the WireGuard tunnels: pfSense unreachable" }, { status: 503 }) });

  const section = screen.getByRole("region", { name: "Remote Access" });
  expect(within(section).getByText(/can't read the WireGuard tunnels/)).toBeTruthy();
});
