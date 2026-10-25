import { cleanup, fireEvent, screen, within } from "@testing-library/react";
import { settle, unavailable, type Answers } from "@ddash/plugin/testing";
import { afterEach, expect, it } from "vitest";
import { show as showNetwork, withAdguard } from "./testing";

afterEach(cleanup);

const tv = {
  mac: "02:5e:00:00:10:01",
  name: "Living room TV",
  ip: "198.18.1.20",
  online: true,
  adopted: true,
  adBlockingOff: true,
  reservation: { interface: "lan", ip: "198.18.1.20", hostname: "living-room-tv", description: "Living room TV" },
};
const laptop = {
  mac: "02:5e:00:00:10:02",
  name: "Work laptop",
  ip: "198.18.1.21",
  online: true,
  adopted: true,
  reservation: { interface: "lan", ip: "198.18.1.21", hostname: "work-laptop", description: "Work laptop" },
};
const phone = {
  mac: "02:5e:00:00:20:01",
  name: "galaxy-s24",
  ip: "198.18.1.150",
  online: true,
  lease: { ip: "198.18.1.150", hostname: "galaxy-s24", status: "active", onlineStatus: "active/online" },
};
const devices = { bypassAlias: "VPN_BYPASS", devices: [tv, laptop, phone] };

const query = (domain: string, extra: Record<string, unknown> = {}) => ({
  time: "2026-10-20T09:59:50.123456Z",
  domain,
  type: "A",
  client: "198.18.1.20",
  reason: "NotFilteredNotFound",
  status: "NOERROR",
  elapsedMs: 4.5,
  ...extra,
});
const queries = {
  queries: [query("site1.example"), query("site2.example", { blocked: true, reason: "FilteredBlackList", rule: "||site2.example^" }), query("site3.example")],
  oldest: "2026-10-20T09:50:20.123456Z",
};
const blocked = { queries: [queries.queries[1]], oldest: "" };


// Shows the network Plugin at path, answering by method name ({} unless
// given), and returns the calls with their JSON bodies.
const show = (path: string, answers: Answers = {}) =>
  showNetwork(
    path,
    {
      ListDevices: () => Response.json(devices),
      ListQueries: (b) => Response.json((b as { blockedOnly?: boolean }).blockedOnly ? blocked : queries),
      ...answers,
    },
    withAdguard,
  );

const deviceRegion = () => screen.getByRole("region", { name: "Device" });

it("shows the Devices table's ad-blocking, off where the Operator turned it off", async () => {
  await show("/network");

  const table = within(screen.getByRole("region", { name: "Devices" })).getByRole("table");
  expect(within(within(table).getByRole("row", { name: /Living room TV/ })).getByText("Ads allowed")).toBeTruthy();
  expect(within(within(table).getByRole("row", { name: /Work laptop/ })).queryByText("Ads allowed")).toBeNull();
});

it("turns ad-blocking off for a Device at once, without confirming", async () => {
  const calls = await show("/network/devices/02:5e:00:00:10:02", {
    SetAdBlocking: () => Response.json({ device: { ...laptop, adBlockingOff: true } }),
  });

  const toggle = within(deviceRegion()).getByRole("switch", { name: /^Ad-blocking / });
  expect(toggle.getAttribute("aria-checked")).toBe("true");
  fireEvent.click(toggle);
  await settle();

  expect(screen.queryByRole("alertdialog")).toBeNull();
  expect(calls.filter((c) => c.method === "SetAdBlocking").map((c) => c.body)).toEqual([{ mac: "02:5e:00:00:10:02" }]);
  // The Action refreshes the Devices.
  expect(calls.filter((c) => c.method === "ListDevices").length).toBe(2);
});

it("turns ad-blocking back on", async () => {
  const calls = await show("/network/devices/02:5e:00:00:10:01", { SetAdBlocking: () => Response.json({ device: { ...tv, adBlockingOff: false } }) });

  const toggle = within(deviceRegion()).getByRole("switch", { name: /^Ad-blocking / });
  expect(toggle.getAttribute("aria-checked")).toBe("false");
  fireEvent.click(toggle);
  await settle();

  expect(calls.filter((c) => c.method === "SetAdBlocking").map((c) => c.body)).toEqual([{ mac: "02:5e:00:00:10:01", enabled: true }]);
});

it("says a toggle on a New Device Adopts it first", async () => {
  await show("/network/devices/02:5e:00:00:20:01");

  expect(within(deviceRegion()).getByText(/ad-blocking off Adopts it first/)).toBeTruthy();
});

it("shows why a toggle failed", async () => {
  await show("/network/devices/02:5e:00:00:10:02", {
    SetAdBlocking: () => unavailable("AdGuard Home unreachable: connection refused"),
  });

  fireEvent.click(within(deviceRegion()).getByRole("switch", { name: /^Ad-blocking / }));
  await settle();

  expect(within(deviceRegion()).getByRole("alert").textContent).toContain("AdGuard Home unreachable");
});

it("disables the toggle while AdGuard's clients can't be read, saying why", async () => {
  await show("/network/devices/02:5e:00:00:10:02", {
    ListDevices: () => Response.json({ ...devices, adguardError: "can't read AdGuard's clients: AdGuard Home unreachable" }),
  });

  expect((within(deviceRegion()).getByRole("switch", { name: /^Ad-blocking / }) as HTMLButtonElement).disabled).toBe(true);
  expect(within(deviceRegion()).getByRole("status").textContent).toMatch(/can't read AdGuard's clients/);
});

it("shows the Device's last 100 queries, and only the blocked ones when asked", async () => {
  const calls = await show("/network/devices/02:5e:00:00:10:01");

  expect(calls.filter((c) => c.method === "ListQueries").map((c) => c.body)).toEqual([{ client: "198.18.1.20", limit: 100 }]);
  const region = screen.getByRole("region", { name: "Queries" });
  const table = within(region).getByRole("table");
  expect(within(table).getAllByRole("row").slice(1).map((r) => within(r).getByRole("rowheader").textContent)).toEqual([
    "site1.example",
    "site2.example",
    "site3.example",
  ]);
  const site2 = within(table).getByRole("row", { name: /site2\.example/ });
  expect(within(site2).getByText("Blocked")).toBeTruthy();
  expect(within(site2).getByText("||site2.example^")).toBeTruthy();

  fireEvent.click(within(region).getByRole("checkbox", { name: "Blocked only" }));
  await settle();

  expect(calls.filter((c) => c.method === "ListQueries").map((c) => c.body)).toContainEqual({ client: "198.18.1.20", limit: 100, blockedOnly: true });
  expect(within(within(region).getByRole("table")).getAllByRole("row")).toHaveLength(2);
});

it("says when the Device made no queries", async () => {
  await show("/network/devices/02:5e:00:00:10:01", { ListQueries: () => Response.json({ queries: [] }) });

  expect(within(screen.getByRole("region", { name: "Queries" })).getByText(/No queries/)).toBeTruthy();
});
