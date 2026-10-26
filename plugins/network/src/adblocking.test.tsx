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

// Shows the network Plugin at path, answering by method name ({} unless
// given), and returns the calls with their JSON bodies.
const show = (path: string, answers: Answers = {}) =>
  showNetwork(
    path,
    {
      ListDevices: () => Response.json(devices),
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

const table = () => within(screen.getByRole("region", { name: "Devices" })).getByRole("table");
/** Opens a Device's strip in the Devices table and returns it. */
function open(device: string) {
  fireEvent.click(within(table()).getByRole("button", { name: `Actions for ${device}` }));
  return within(table()).getByRole("group", { name: `Actions for ${device}` });
}

it("turns ad-blocking off from the Devices table's strip, pressing Ads blocked off at once", async () => {
  const calls = await show("/network");

  const strip = open("Work laptop");
  expect(within(strip).getByRole("status").textContent).toBe("Tunnelled via the VPN Tunnel · ads blocked");
  const toggle = within(strip).getByRole("button", { name: "Ads blocked" });
  expect(toggle.getAttribute("aria-pressed")).toBe("true");
  fireEvent.click(toggle);
  await settle();

  expect(screen.queryByRole("alertdialog")).toBeNull();
  expect(calls.filter((c) => c.method === "SetAdBlocking").map((c) => c.body)).toEqual([{ mac: "02:5e:00:00:10:02" }]);
});

it("turns ad-blocking back on from the strip", async () => {
  const calls = await show("/network");

  const strip = open("Living room TV");
  expect(within(strip).getByRole("status").textContent).toBe("Tunnelled via the VPN Tunnel · ads allowed");
  fireEvent.click(within(strip).getByRole("button", { name: "Ads blocked", pressed: false }));
  await settle();

  expect(calls.filter((c) => c.method === "SetAdBlocking").map((c) => c.body)).toEqual([{ mac: "02:5e:00:00:10:01", enabled: true }]);
});

it("shows why the strip's toggle failed", async () => {
  await show("/network", { SetAdBlocking: () => unavailable("AdGuard Home unreachable: connection refused") });

  const strip = open("Work laptop");
  fireEvent.click(within(strip).getByRole("button", { name: "Ads blocked" }));
  await settle();

  expect(within(strip).getByRole("alert").textContent).toContain("AdGuard Home unreachable");
});

it("disables the strip's Ads blocked while AdGuard's clients can't be read, saying why", async () => {
  await show("/network", {
    ListDevices: () => Response.json({ ...devices, adguardError: "can't read AdGuard's clients: AdGuard Home unreachable" }),
  });

  const strip = open("Work laptop");
  expect((within(strip).getByRole("button", { name: "Ads blocked" }) as HTMLButtonElement).disabled).toBe(true);
  // The summary doesn't guess.
  expect(within(strip).getByText("Tunnelled via the VPN Tunnel")).toBeTruthy();
  expect(within(strip).getByText(/can't read AdGuard's clients/)).toBeTruthy();
});

it("has no Ads blocked in the strip without AdGuard", async () => {
  await showNetwork("/network", { ListDevices: () => Response.json(devices) });

  const strip = open("Work laptop");
  expect(within(strip).queryByRole("button", { name: "Ads blocked" })).toBeNull();
  expect(within(strip).getByRole("status").textContent).toBe("Tunnelled via the VPN Tunnel");
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
