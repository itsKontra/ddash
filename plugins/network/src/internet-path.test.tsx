import { cleanup, fireEvent, screen, within } from "@testing-library/react";
import { pending, settle, type Answers, type Call } from "@ddash/plugin/testing";
import { afterEach, expect, it } from "vitest";
import { show as showNetwork } from "./testing";

afterEach(cleanup);

const tv = {
  mac: "02:5e:00:00:10:01",
  name: "Living room TV",
  ip: "198.18.1.20",
  online: true,
  adopted: true,
  internetPath: "INTERNET_PATH_TUNNELLED",
  reservation: { interface: "lan", ip: "198.18.1.20", hostname: "living-room-tv", description: "Living room TV" },
};
const laptop = {
  mac: "02:5e:00:00:10:02",
  name: "Work laptop",
  ip: "198.18.1.21",
  online: true,
  adopted: true,
  internetPath: "INTERNET_PATH_DIRECT",
  reservation: { interface: "lan", ip: "198.18.1.21", hostname: "work-laptop", description: "Work laptop" },
};
const camera = {
  mac: "02:5e:00:00:10:04",
  name: "Doorbell camera",
  ip: "198.18.1.24",
  online: true,
  adopted: true,
  internetPath: "INTERNET_PATH_NO_INTERNET",
  reservation: { interface: "lan", ip: "198.18.1.24", hostname: "doorbell", description: "Doorbell camera" },
};
const tablet = {
  mac: "02:5e:00:00:20:02",
  ip: "198.18.1.151",
  internetPath: "INTERNET_PATH_TUNNELLED",
  lease: { ip: "198.18.1.151", status: "expired", onlineStatus: "idle/offline" },
};
const devices = { bypassAlias: "VPN_BYPASS", blockAlias: "NO_INTERNET", devices: [tv, laptop, camera, tablet] };
/** As network answers before the No Internet alias is set. */
const withoutBlock = { bypassAlias: "VPN_BYPASS", devices: [tv, laptop, tablet] };

// Shows the network Plugin at path with network answering by method name
// (Actions {} unless given), and returns the calls with their JSON bodies.
const show = (path: string, answers: Answers = {}) =>
  showNetwork(path, {
    ListDevices: () => Response.json(devices),
    ...answers,
  });

const moves = (calls: Call[]) => calls.filter((c) => c.method === "SetInternetPath").map((c) => c.body);

const table = () => within(screen.getByRole("region", { name: "Devices" })).getByRole("table");
/** Opens a Device's strip in the Devices table and returns it. */
function open(device: string) {
  fireEvent.click(within(table()).getByRole("button", { name: `Actions for ${device}` }));
  return within(table()).getByRole("group", { name: `Actions for ${device}` });
}
const picker = (strip: HTMLElement) => within(strip).getByRole("radiogroup", { name: "Internet path" });
const choices = (strip: HTMLElement) => within(picker(strip)).getAllByRole("radio").map((r) => r.textContent);
const checked = (strip: HTMLElement) => within(picker(strip)).getByRole("radio", { checked: true }).textContent;

it("offers the three internet paths in a Device's strip, its own checked, saying where it is", async () => {
  await show("/network");

  const strip = open("Doorbell camera");
  expect(choices(strip)).toEqual(["Tunnelled", "Direct", "No Internet"]);
  expect(checked(strip)).toBe("No Internet");
  expect(within(strip).getByRole("status").textContent).toBe("No Internet, in NO_INTERNET");
});

it("moves a Device to another path with one click, with no confirm", async () => {
  const calls = await show("/network");

  const strip = open("Living room TV");
  expect(within(strip).getByRole("status").textContent).toBe("Tunnelled via the VPN Tunnel");
  fireEvent.click(within(picker(strip)).getByRole("radio", { name: "No Internet" }));
  await settle();

  expect(screen.queryByRole("alertdialog")).toBeNull();
  expect(moves(calls)).toEqual([{ mac: "02:5e:00:00:10:01", path: "INTERNET_PATH_NO_INTERNET" }]);
  // Every query is refetched after the Action, so the table shows it.
  expect(calls.filter((c) => c.method === "ListDevices").length).toBeGreaterThan(1);
});

it("tunnels a Direct Device again from its strip, which says where it is", async () => {
  const calls = await show("/network");

  const strip = open("Work laptop");
  expect(within(strip).getByRole("status").textContent).toBe("Direct, in VPN_BYPASS");
  fireEvent.click(within(picker(strip)).getByRole("radio", { name: "Tunnelled" }));
  await settle();

  expect(moves(calls)).toEqual([{ mac: "02:5e:00:00:10:02", path: "INTERNET_PATH_TUNNELLED" }]);
});

it("holds the picker busy while the move applies", async () => {
  const calls = await show("/network", { SetInternetPath: pending });

  const strip = open("Living room TV");
  fireEvent.click(within(picker(strip)).getByRole("radio", { name: "Direct" }));
  await settle();

  expect(picker(strip).getAttribute("aria-busy")).toBe("true");
  for (const radio of within(picker(strip)).getAllByRole("radio")) expect((radio as HTMLButtonElement).disabled).toBe(true);
  fireEvent.click(within(picker(strip)).getByRole("radio", { name: "No Internet" }));
  await settle();
  expect(moves(calls)).toEqual([{ mac: "02:5e:00:00:10:01", path: "INTERNET_PATH_DIRECT" }]);
});

it("offers only Tunnelled and Direct until the No Internet alias is set", async () => {
  await show("/network", { ListDevices: () => Response.json(withoutBlock) });

  expect(choices(open("Living room TV"))).toEqual(["Tunnelled", "Direct"]);
  expect(within(screen.getByRole("region", { name: "Devices" })).queryByRole("tab", { name: /No Internet/ })).toBeNull();
});

it("says in a New Device's strip that a change Adopts it first", async () => {
  await show("/network");

  expect(within(open("02:5e:00:00:20:02")).getByRole("status").textContent).toBe("Tunnelled via the VPN Tunnel · New: a change Adopts it first");
});

it("shows network's refusal in the strip", async () => {
  await show("/network", {
    SetInternetPath: () => Response.json({ code: "failed_precondition", message: "Work laptop stays Direct: VPN_BYPASS holds 198.18.1.21 through 198.18.1.16/28" }, { status: 400 }),
  });

  const strip = open("Work laptop");
  fireEvent.click(within(picker(strip)).getByRole("radio", { name: "Tunnelled" }));
  await settle();

  expect(within(strip).getByRole("alert").textContent).toMatch(/stays Direct/);
});

it("shows each Device's path in the table, with a No Internet Tab", async () => {
  await show("/network");

  const region = screen.getByRole("region", { name: "Devices" });
  expect(within(table()).getByRole("row", { name: /Doorbell camera/ }).textContent).toMatch(/No Internet/);
  fireEvent.click(within(region).getByRole("tab", { name: /No Internet/ }));
  await settle();

  const names = within(table())
    .getAllByRole("rowheader")
    .map((r) => r.textContent);
  expect(names).toEqual(["Doorbell camera"]);
});

it("picks the path on the Device's detail too, saying a New Device is Adopted first", async () => {
  const calls = await show("/network/devices/02:5e:00:00:20:02");

  const region = screen.getByRole("region", { name: "Device" });
  expect(region.textContent).toMatch(/Adopts it first/);
  fireEvent.click(within(picker(region)).getByRole("radio", { name: "Direct" }));
  await settle();

  expect(screen.queryByRole("dialog")).toBeNull();
  expect(moves(calls)).toEqual([{ mac: "02:5e:00:00:20:02", path: "INTERNET_PATH_DIRECT" }]);
  expect(calls.some((c) => c.method === "AdoptDevice")).toBe(false);
});

it("shows network's refusal next to the detail's picker", async () => {
  await show("/network/devices/02:5e:00:00:10:02", {
    SetInternetPath: () =>
      Response.json({ code: "failed_precondition", message: "Work laptop stays Direct: VPN_BYPASS holds 198.18.1.21 through 198.18.1.16/28" }, { status: 400 }),
  });

  const region = screen.getByRole("region", { name: "Device" });
  expect(region.textContent).toMatch(/in VPN_BYPASS/);
  fireEvent.click(within(picker(region)).getByRole("radio", { name: "Tunnelled" }));
  await settle();

  expect(within(region).getByRole("alert").textContent).toMatch(/stays Direct/);
});
