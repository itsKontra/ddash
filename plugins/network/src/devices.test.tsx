import { act, cleanup, fireEvent, render, screen, within } from "@testing-library/react";
import { createQueryClient, PluginHost, PluginScope } from "@ddash/plugin";
import { MemoryRouter, Route, Routes, useRoutes } from "react-router";
import { afterEach, expect, it, vi } from "vitest";
import plugin from "./index";

afterEach(cleanup);

// What network answers ListDevices with (Connect JSON).
const devices = {
  bypassAlias: "VPN_BYPASS",
  devices: [
    {
      mac: "02:5e:00:00:10:01",
      name: "Living room TV",
      ip: "198.18.1.20",
      online: true,
      adopted: true,
      lease: { ip: "198.18.1.20", hostname: "living-room-tv", interface: "lan", status: "static", onlineStatus: "active/online" },
      reservation: { interface: "lan", ip: "198.18.1.20", hostname: "living-room-tv", description: "Living room TV" },
      aliases: [{ name: "KILL_SWITCH", description: "Never leaves through the WAN" }],
    },
    {
      mac: "02:5e:00:00:10:02",
      name: "Work laptop",
      ip: "198.18.1.21",
      online: true,
      adopted: true,
      direct: true,
      reservation: { interface: "lan", ip: "198.18.1.21", hostname: "work-laptop", description: "Work laptop" },
    },
    {
      mac: "02:5e:00:00:20:01",
      name: "galaxy-s24",
      ip: "198.18.1.150",
      online: true,
      direct: true,
      lease: { ip: "198.18.1.150", hostname: "galaxy-s24", starts: "2026/10/20 08:02:11", ends: "2026/10/20 10:02:11", status: "active", onlineStatus: "active/online" },
    },
    {
      mac: "02:5e:00:00:20:02",
      ip: "198.18.1.151",
      lease: { ip: "198.18.1.151", starts: "2026/10/19 21:40:55", ends: "2026/10/19 23:40:55", status: "expired", onlineStatus: "idle/offline" },
    },
  ],
};

const gateways = { gateways: [] };
const tunnel = { tier: "TIER_WIREGUARD", group: "VPN_FAILOVER", gateways: [] };

function PluginPages() {
  return useRoutes(plugin.pages!);
}

// Shows the network Plugin's pages at path, as the shell mounts them under
// /network/, with network and pfsense-api answering by method.
async function show(path: string, answers: Record<string, () => Response> = {}) {
  const all: Record<string, () => Response> = {
    ListDevices: () => Response.json(devices),
    ListGateways: () => Response.json(gateways),
    GetVpnTunnel: () => Response.json(tunnel),
    ...answers,
  };
  const fetch = vi.fn(async (input: RequestInfo | URL) => {
    const method = new URL(String(input), "http://ddash.test").pathname.split("/").pop()!;
    return all[method]?.() ?? Response.json({});
  });
  render(
    <PluginHost client={createQueryClient()}>
      <PluginScope plugin={{ name: "network", requires: ["network", "pfsense-api"] }} fetch={fetch}>
        <MemoryRouter initialEntries={[path]}>
          <Routes>
            <Route path="/network/*" element={<PluginPages />} />
          </Routes>
        </MemoryRouter>
      </PluginScope>
    </PluginHost>,
  );
  await act(() => new Promise((r) => setTimeout(r, 50)));
  return fetch;
}

it("lists every Device with name, IP, MAC, online, Adopted or New, and Tunnelled or Direct", async () => {
  const fetch = await show("/network");

  expect(fetch.mock.calls.map(([input]) => new URL(String(input), "http://ddash.test").pathname)).toContain(
    "/api/c/network/ddash.network.NetworkService/ListDevices",
  );
  const table = screen.getByRole("table", { name: "Devices" });
  const tv = within(table).getByRole("row", { name: /Living room TV/ });
  expect(tv.textContent).toMatch(/198\.18\.1\.20.*02:5e:00:00:10:01.*Adopted.*Tunnelled/);
  expect(within(tv).getByRole("img", { name: "online" })).toBeTruthy();
  const phone = within(table).getByRole("row", { name: /galaxy-s24/ });
  expect(phone.textContent).toMatch(/198\.18\.1\.150.*New.*Direct/);
  const unnamed = within(table).getByRole("row", { name: /02:5e:00:00:20:02/ });
  expect(within(unnamed).getByRole("img", { name: "offline" })).toBeTruthy();
  expect(unnamed.textContent).toMatch(/New.*Tunnelled/);
});

it("counts the New Devices in the section's header", async () => {
  await show("/network");

  expect(within(screen.getByRole("region", { name: "Devices" })).getByText("2 new")).toBeTruthy();
});

it("opens a Device's detail from its name", async () => {
  await show("/network");

  fireEvent.click(screen.getByRole("link", { name: "Living room TV" }));
  await act(() => new Promise((r) => setTimeout(r, 20)));

  expect(screen.getByRole("heading", { level: 1, name: "Living room TV" })).toBeTruthy();
});

it("shows pfsense-api's trouble in place of the Devices", async () => {
  await show("/network", {
    ListDevices: () =>
      Response.json({ code: "failed_precondition", message: 'bypass alias: the firewall has no alias "VPN_BYPASS"; pick another in network\'s config' }, { status: 400 }),
  });

  const section = screen.getByRole("region", { name: "Devices" });
  expect(within(section).getByText(/no alias "VPN_BYPASS"/)).toBeTruthy();
});

it("shows a Device's lease and reservation facts and its other aliases, read-only", async () => {
  await show("/network/devices/02:5e:00:00:10:01");

  expect(screen.getByRole("heading", { level: 1, name: "Living room TV" })).toBeTruthy();
  const summary = screen.getByRole("region", { name: "Device" });
  expect(summary.textContent).toMatch(/198\.18\.1\.20/);
  expect(summary.textContent).toMatch(/02:5e:00:00:10:01/);
  expect(summary.textContent).toMatch(/Adopted/);
  expect(summary.textContent).toMatch(/Tunnelled/);
  const lease = screen.getByRole("region", { name: "Lease" });
  expect(lease.textContent).toMatch(/living-room-tv/);
  expect(lease.textContent).toMatch(/static/);
  const reservation = screen.getByRole("region", { name: "Reservation" });
  expect(reservation.textContent).toMatch(/Living room TV/);
  expect(reservation.textContent).toMatch(/lan/);
  const aliases = screen.getByRole("region", { name: "Other aliases" });
  expect(within(aliases).getByRole("listitem").textContent).toMatch(/KILL_SWITCH.*Never leaves through the WAN/);
  expect(within(aliases).queryByRole("button")).toBeNull();
  expect(within(aliases).queryByRole("switch")).toBeNull();
});

it("shows a New Device's lease times and that it has no reservation", async () => {
  await show("/network/devices/02:5e:00:00:20:01");

  const lease = screen.getByRole("region", { name: "Lease" });
  expect(lease.textContent).toMatch(/2026\/10\/20 08:02:11/);
  expect(lease.textContent).toMatch(/2026\/10\/20 10:02:11/);
  expect(screen.getByRole("region", { name: "Reservation" }).textContent).toMatch(/New Device/);
  expect(screen.getByRole("region", { name: "Device" }).textContent).toMatch(/Direct/);
  expect(screen.getByRole("region", { name: "Other aliases" }).textContent).toMatch(/None/);
});

it("says so for a MAC that is no Device", async () => {
  await show("/network/devices/02:5e:00:00:99:99");

  expect(screen.getByText(/No Device has the MAC 02:5e:00:00:99:99/)).toBeTruthy();
});

it("badges the Nav Entry with the New Devices, and with nothing when there are none", async () => {
  const useBadge = plugin.nav!.useBadge!;
  const seen: unknown[] = [];
  function Probe() {
    seen.push(useBadge());
    return null;
  }
  for (const answer of [devices, { ...devices, devices: devices.devices.slice(0, 2) }]) {
    const fetch = vi.fn(async () => Response.json(answer));
    render(
      <PluginHost client={createQueryClient()}>
        <PluginScope plugin={{ name: "network", requires: ["network", "pfsense-api"] }} fetch={fetch}>
          <Probe />
        </PluginScope>
      </PluginHost>,
    );
    await act(() => new Promise((r) => setTimeout(r, 50)));
    cleanup();
  }

  expect(seen).toContain(null);
  expect(seen).toContainEqual({ tone: "warn", text: "2 new" });
  expect(seen.at(-1)).toBeNull();
});
