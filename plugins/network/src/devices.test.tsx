import { cleanup, fireEvent, screen, within } from "@testing-library/react";
import { pending, renderScoped, settle, unavailable, type Answers } from "@ddash/plugin/testing";
import { useLocation, useNavigate, useRoutes } from "react-router";
import { afterEach, expect, it } from "vitest";
import plugin from "./index";
import { network, show as showNetwork } from "./testing";

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

// Shows the network Plugin's pages at path, as the shell mounts them under
// /network/, with network and pfsense-api answering by method.
const show = (path: string, answers: Answers = {}) =>
  showNetwork(path, {
    ListDevices: () => Response.json(devices),
    ListGateways: () => Response.json(gateways),
    GetVpnTunnel: () => Response.json(tunnel),
    ...answers,
  });

// The Page's URL search, as the Devices table last wrote it.
let search = "";

function Where() {
  search = useLocation().search;
  return null;
}

/** The browser's Back button. */
function Back() {
  const navigate = useNavigate();
  return (
    <button type="button" onClick={() => void navigate(-1)}>
      Browser back
    </button>
  );
}

function Pages() {
  return useRoutes([{ path: "/network/*", children: plugin.pages ?? [] }]);
}

// Shows the network Plugin's pages at path (e.g. "/network?devices.q=tv"),
// keeping the URL's search in search.
async function showAt(path: string) {
  renderScoped(
    network,
    <>
      <Pages />
      <Where />
      <Back />
    </>,
    {
      path,
      answers: { ListDevices: () => Response.json(devices), ListGateways: () => Response.json(gateways), GetVpnTunnel: () => Response.json(tunnel) },
    },
  );
  await settle();
}

const panel = () => screen.getByRole("region", { name: "Devices" });
const table = () => within(panel()).getByRole("table");
/** The Devices' names, in the table's order. */
const names = () => within(table()).queryAllByRole("rowheader").map((h) => h.textContent);
const searchbox = () => within(panel()).getByRole("searchbox", { name: "Search Devices" });
const tabs = () => within(panel()).getByRole("tablist", { name: "Show" });

it("lists every Device with name, IP, MAC, online, Adopted or New, and Tunnelled or Direct", async () => {
  const calls = await show("/network");

  expect(calls.map((c) => c.url)).toContain(
    "/api/c/network/ddash.network.NetworkService/ListDevices",
  );
  const table = within(screen.getByRole("region", { name: "Devices" })).getByRole("table");
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
  await settle();

  expect(screen.getByRole("heading", { level: 1, name: "Living room TV" })).toBeTruthy();
  // The kicker sits right above the title.
  expect(screen.getByRole("heading", { level: 1, name: "Living room TV" }).previousElementSibling?.textContent).toBe("Device");
});

it("shows pfsense-api's trouble in place of the Devices", async () => {
  await show("/network", {
    ListDevices: () =>
      Response.json({ code: "failed_precondition", message: 'bypass alias: the firewall has no alias "VPN_BYPASS"; pick another in network\'s config' }, { status: 400 }),
  });

  const section = screen.getByRole("region", { name: "Devices" });
  expect(within(section).getByRole("alert").textContent).toMatch(/no alias "VPN_BYPASS"/);
});

it("says so when the firewall's DHCP knows no Devices", async () => {
  await show("/network", { ListDevices: () => Response.json({ bypassAlias: "VPN_BYPASS", devices: [] }) });

  const section = screen.getByRole("region", { name: "Devices" });
  expect(within(section).getByText("No Devices")).toBeTruthy();
  expect(within(section).queryByRole("table")).toBeNull();
});

it("goes back to the Network Page from a Device's detail", async () => {
  await show("/network/devices/02:5e:00:00:10:01");

  fireEvent.click(screen.getByRole("link", { name: "Network" }));
  await settle();

  expect(screen.getByRole("heading", { level: 1, name: "Network" })).toBeTruthy();
});

it("shows a Device's detail loading, then network's error in its place", async () => {
  await show("/network/devices/02:5e:00:00:10:01", { ListDevices: pending });
  expect(screen.getAllByRole("status", { name: "Loading" }).length).toBeGreaterThan(0);
  cleanup();

  await show("/network/devices/02:5e:00:00:10:01", { ListDevices: () => unavailable("network is down") });
  expect(screen.getByRole("heading", { level: 1, name: "02:5e:00:00:10:01" })).toBeTruthy();
  expect(screen.getByRole("alert").textContent).toMatch(/network is down/);
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

  expect(screen.getByRole("heading", { level: 1, name: "02:5e:00:00:99:99" })).toBeTruthy();
  expect(screen.getByText(/No Device has the MAC 02:5e:00:00:99:99/)).toBeTruthy();
  expect(screen.getByRole("link", { name: "Network" })).toBeTruthy();
});

it("badges the Nav Entry with the New Devices, and with nothing when there are none", async () => {
  const useBadge = plugin.nav!.useBadge!;
  const seen: unknown[] = [];
  function Probe() {
    seen.push(useBadge());
    return null;
  }
  for (const answer of [devices, { ...devices, devices: devices.devices.slice(0, 2) }]) {
    renderScoped(network, <Probe />, { answers: { ListDevices: () => Response.json(answer) } });
    await settle();
    cleanup();
  }

  expect(seen).toContain(null);
  expect(seen).toContainEqual({ tone: "warn", text: "2 new" });
  expect(seen.at(-1)).toBeNull();
});

it("sorts the Devices by name until another sort is chosen, and by IP as addresses, not text, kept in the URL", async () => {
  await showAt("/network");

  expect(names()).toEqual(["02:5e:00:00:20:02", "galaxy-s24", "Living room TV", "Work laptop"]);
  expect(within(table()).getByRole("columnheader", { name: "Name" }).getAttribute("aria-sort")).toBe("ascending");
  expect(search).toBe("");

  fireEvent.click(within(table()).getByRole("button", { name: "IP" }));
  // As text, 198.18.1.150 would come before 198.18.1.20.
  expect(names()).toEqual(["Living room TV", "Work laptop", "galaxy-s24", "02:5e:00:00:20:02"]);
  expect(search).toBe("?devices.sort=ip");

  fireEvent.click(within(table()).getByRole("button", { name: "Address" }));
  expect(names()).toEqual(["02:5e:00:00:20:02", "galaxy-s24", "Living room TV", "Work laptop"]);
  expect(search).toBe("?devices.sort=address");
});

it("sorts by Online, MAC and Routing, ties by name", async () => {
  await showAt("/network?devices.sort=online");
  expect(names()).toEqual(["galaxy-s24", "Living room TV", "Work laptop", "02:5e:00:00:20:02"]);
  cleanup();

  await showAt("/network?devices.sort=-mac");
  expect(names()).toEqual(["02:5e:00:00:20:02", "galaxy-s24", "Work laptop", "Living room TV"]);
  cleanup();

  await showAt("/network?devices.sort=routing");
  expect(names()).toEqual(["02:5e:00:00:20:02", "Living room TV", "galaxy-s24", "Work laptop"]);
});

it("searches the Devices by name, IP, MAC with or without colons, or an alias, kept in the URL", async () => {
  await showAt("/network");

  for (const [q, found] of [
    ["living", ["Living room TV"]],
    ["198.18.1.15", ["02:5e:00:00:20:02", "galaxy-s24"]],
    ["025e00001002", ["Work laptop"]],
    ["kill_switch", ["Living room TV"]],
  ] as const) {
    fireEvent.change(searchbox(), { target: { value: q } });
    expect(names()).toEqual(found);
  }
  expect(search).toBe("?devices.q=kill_switch");
});

it("says so when no Device matches the search", async () => {
  await showAt("/network?devices.q=printer");

  expect(searchbox()).toHaveProperty("value", "printer");
  expect(names()).toEqual([]);
  expect(table().textContent).toContain('No Devices match "printer"');
});

it("shows All, New, Online, Tunnelled or Direct Devices in Tabs with their counts, kept in the URL", async () => {
  await showAt("/network");

  const counts = () => within(tabs()).getAllByRole("tab").map((t) => t.textContent);
  expect(counts()).toEqual(["All4", "New2", "Online3", "Tunnelled2", "Direct2"]);

  fireEvent.click(within(tabs()).getByRole("tab", { name: /New/ }));
  expect(names()).toEqual(["02:5e:00:00:20:02", "galaxy-s24"]);
  expect(search).toBe("?devices.tab=new");
  fireEvent.click(within(tabs()).getByRole("tab", { name: /Online/ }));
  expect(names()).toEqual(["galaxy-s24", "Living room TV", "Work laptop"]);
  fireEvent.click(within(tabs()).getByRole("tab", { name: /Tunnelled/ }));
  expect(names()).toEqual(["02:5e:00:00:20:02", "Living room TV"]);
  fireEvent.click(within(tabs()).getByRole("tab", { name: /Direct/ }));
  expect(names()).toEqual(["galaxy-s24", "Work laptop"]);

  // The counts follow the search.
  fireEvent.change(searchbox(), { target: { value: "198.18.1.15" } });
  expect(counts()).toEqual(["All2", "New2", "Online1", "Tunnelled1", "Direct1"]);
});

it("opens on the search, Tab and sort a link carries, and has them back after a Device's detail and Back", async () => {
  await showAt("/network?devices.q=198.18&devices.tab=online&devices.sort=-name");

  const shown = ["Work laptop", "Living room TV", "galaxy-s24"];
  expect(names()).toEqual(shown);
  expect(within(tabs()).getByRole("tab", { name: /Online/ }).getAttribute("aria-selected")).toBe("true");

  fireEvent.click(screen.getByRole("link", { name: "Work laptop" }));
  await settle();
  expect(screen.getByRole("heading", { level: 1, name: "Work laptop" })).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: "Browser back" }));
  await settle();

  expect(names()).toEqual(shown);
  expect(searchbox()).toHaveProperty("value", "198.18");
});

it("has no ⋯ Menu: each row's Actions sit behind its arrow, named for the Device", async () => {
  await showAt("/network");

  expect(within(table()).queryByRole("button", { name: "Actions for 02:5e:00:00:10:01" })).toBeNull();
  const arrow = within(table()).getByRole("button", { name: "Actions for Living room TV" });
  expect(arrow.getAttribute("aria-expanded")).toBe("false");
  fireEvent.click(arrow);
  expect(within(table()).getByRole("group", { name: "Actions for Living room TV" }).textContent).toMatch(/Tunnelled via the VPN Tunnel/);
});
