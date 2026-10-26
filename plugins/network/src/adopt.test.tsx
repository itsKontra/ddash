import { cleanup, fireEvent, screen, within } from "@testing-library/react";
import { settle, type Answers } from "@ddash/plugin/testing";
import { afterEach, expect, it } from "vitest";
import { show as showNetwork } from "./testing";

afterEach(cleanup);

const tv = {
  mac: "02:5e:00:00:10:01",
  name: "Living room TV",
  ip: "198.18.1.20",
  online: true,
  adopted: true,
  reservation: { interface: "lan", ip: "198.18.1.20", hostname: "living-room-tv", description: "Living room TV" },
};
const phone = {
  mac: "02:5e:00:00:20:01",
  name: "galaxy-s24",
  ip: "198.18.1.150",
  online: true,
  lease: { ip: "198.18.1.150", hostname: "galaxy-s24", status: "active", onlineStatus: "active/online" },
};
const devices = { bypassAlias: "VPN_BYPASS", devices: [tv, phone] };
const next = { ip: "198.18.1.23", reservedRange: "198.18.1.20-198.18.1.49" };


// Shows the network Plugin at path with network answering by method name
// (Actions {} unless given), and returns the calls with their JSON bodies.
const show = (path: string, answers: Answers = {}) =>
  showNetwork(
    path,
    {
      ListDevices: () => Response.json(devices),
      NextReservedAddress: () => Response.json(next),
      ...answers,
    },
  );

const field = (dialog: HTMLElement, name: string) => within(dialog).getByRole("textbox", { name }) as HTMLInputElement;

const table = () => within(screen.getByRole("region", { name: "Devices" })).getByRole("table");
const arrow = (device: string) => within(table()).getByRole("button", { name: `Actions for ${device}` });
/** Opens a Device's strip in the Devices table and returns it. */
function open(device: string) {
  fireEvent.click(arrow(device));
  return within(table()).getByRole("group", { name: `Actions for ${device}` });
}

it("adopts a New Device from the Devices table with the lowest free reserved address pre-filled", async () => {
  const calls = await show("/network");

  const row = within(table()).getByRole("row", { name: /galaxy-s24/ });
  // Row actions live in the row's strip, behind its arrow.
  expect(within(row).queryByRole("button", { name: /Adopt/ })).toBeNull();
  const strip = open("galaxy-s24");
  expect(within(strip).queryByRole("button", { name: "Rename" })).toBeNull();
  fireEvent.click(within(strip).getByRole("button", { name: "Adopt…" }));
  await settle();

  const dialog = screen.getByRole("dialog", { name: "Adopt galaxy-s24" });
  expect(dialog.textContent).toMatch(/restarts the firewall's DHCP server/);
  expect(field(dialog, "Name").value).toBe("galaxy-s24");
  expect(field(dialog, "Address").value).toBe("198.18.1.23");
  expect(dialog.textContent).toMatch(/198\.18\.1\.20-198\.18\.1\.49/);
  expect(dialog.textContent).toMatch(/moves to it when its lease renews/);

  fireEvent.change(field(dialog, "Name"), { target: { value: "Anna's phone" } });
  fireEvent.change(field(dialog, "Address"), { target: { value: "198.18.1.30" } });
  fireEvent.click(within(dialog).getByRole("button", { name: "Adopt" }));
  await settle();

  expect(calls.filter((c) => c.method === "AdoptDevice").map((c) => c.body)).toEqual([{ mac: "02:5e:00:00:20:01", name: "Anna's phone", ip: "198.18.1.30" }]);
  expect(screen.queryByRole("dialog")).toBeNull();
  // Every query is refetched after the Action, so the table shows it.
  expect(calls.filter((c) => c.method === "ListDevices").length).toBeGreaterThan(1);
});

it("renames an Adopted Device in its strip: Enter renames, and the focus goes back to the arrow", async () => {
  const calls = await show("/network");

  const strip = open("Living room TV");
  expect(within(strip).queryByRole("button", { name: /Adopt/ })).toBeNull();
  fireEvent.click(within(strip).getByRole("button", { name: "Rename" }));

  // No Sheet: the strip turns into the Name field, focused.
  expect(screen.queryByRole("dialog")).toBeNull();
  const name = field(strip, "Name");
  expect(name.value).toBe("Living room TV");
  expect(document.activeElement).toBe(name);
  expect(strip.textContent).toMatch(/198\.18\.1\.20; change that in pfSense/);
  fireEvent.change(name, { target: { value: "Kitchen TV" } });
  fireEvent.submit(name);
  await settle();

  expect(calls.filter((c) => c.method === "RenameDevice").map((c) => c.body)).toEqual([{ mac: "02:5e:00:00:10:01", name: "Kitchen TV" }]);
  expect(within(strip).queryByRole("textbox")).toBeNull();
  expect(strip.textContent).toMatch(/Tunnelled via the VPN Tunnel/);
  expect(document.activeElement).toBe(arrow("Living room TV"));
});

it("cancels a rename with Escape, keeping the strip open until a second Escape", async () => {
  const calls = await show("/network");

  const strip = open("Living room TV");
  fireEvent.click(within(strip).getByRole("button", { name: "Rename" }));
  fireEvent.change(field(strip, "Name"), { target: { value: "Kitchen TV" } });
  fireEvent.keyDown(field(strip, "Name"), { key: "Escape" });
  await settle();

  expect(within(strip).queryByRole("textbox")).toBeNull();
  expect(within(strip).getByRole("button", { name: "Rename" })).toBeTruthy();
  expect(arrow("Living room TV").getAttribute("aria-expanded")).toBe("true");
  expect(document.activeElement).toBe(arrow("Living room TV"));
  expect(calls.some((c) => c.method === "RenameDevice")).toBe(false);

  fireEvent.keyDown(arrow("Living room TV"), { key: "Escape" });
  expect(arrow("Living room TV").getAttribute("aria-expanded")).toBe("false");
});

it("cancels a rename with Cancel", async () => {
  await show("/network");

  const strip = open("Living room TV");
  fireEvent.click(within(strip).getByRole("button", { name: "Rename" }));
  fireEvent.click(within(strip).getByRole("button", { name: "Cancel" }));

  expect(within(strip).queryByRole("textbox")).toBeNull();
  expect(within(strip).getByRole("button", { name: "Rename" })).toBeTruthy();
});

it("keeps the Name field with network's refusal", async () => {
  await show("/network", {
    RenameDevice: () => Response.json({ code: "invalid_argument", message: "a name can't be empty" }, { status: 400 }),
  });

  const strip = open("Living room TV");
  fireEvent.click(within(strip).getByRole("button", { name: "Rename" }));
  fireEvent.change(field(strip, "Name"), { target: { value: "" } });
  fireEvent.click(within(strip).getByRole("button", { name: "Rename" }));
  await settle();

  expect(within(strip).getByRole("alert").textContent).toMatch(/can't be empty/);
  expect(field(strip, "Name")).toBeTruthy();
});

it("adopts a New Device from its detail; an Adopted one offers rename instead", async () => {
  await show("/network/devices/02:5e:00:00:20:01");
  const summary = screen.getByRole("region", { name: "Device" });
  expect(within(summary).getByRole("button", { name: "Adopt…" })).toBeTruthy();
  expect(within(summary).queryByRole("button", { name: /Rename/ })).toBeNull();
  cleanup();

  await show("/network/devices/02:5e:00:00:10:01");
  const adopted = screen.getByRole("region", { name: "Device" });
  expect(within(adopted).queryByRole("button", { name: /Adopt/ })).toBeNull();
  expect(within(adopted).getByRole("button", { name: "Rename" })).toBeTruthy();
});

it("renames an Adopted Device from its detail, leaving the address to pfSense", async () => {
  const calls = await show("/network/devices/02:5e:00:00:10:01");

  fireEvent.click(screen.getByRole("button", { name: "Rename" }));
  // In place, as in the Devices table's strip: there's no Rename Sheet.
  expect(screen.queryByRole("dialog")).toBeNull();
  const summary = screen.getByRole("region", { name: "Device" });
  expect(field(summary, "Name").value).toBe("Living room TV");
  expect(summary.textContent).toMatch(/198\.18\.1\.20/);
  expect(summary.textContent).toMatch(/in pfSense/);
  expect(within(summary).queryByRole("textbox", { name: "Address" })).toBeNull();

  fireEvent.change(field(summary, "Name"), { target: { value: "Kitchen TV" } });
  fireEvent.click(within(summary).getByRole("button", { name: "Rename" }));
  await settle();

  expect(calls.filter((c) => c.method === "RenameDevice").map((c) => c.body)).toEqual([{ mac: "02:5e:00:00:10:01", name: "Kitchen TV" }]);
  expect(within(summary).queryByRole("textbox")).toBeNull();
  expect(document.activeElement).toBe(within(summary).getByRole("button", { name: "Rename" }));
});

it("keeps the dialog open with network's refusal", async () => {
  await show("/network/devices/02:5e:00:00:20:01", {
    AdoptDevice: () => Response.json({ code: "already_exists", message: "198.18.1.23 is already held by printer" }, { status: 409 }),
  });

  fireEvent.click(screen.getByRole("button", { name: "Adopt…" }));
  await settle();
  const dialog = screen.getByRole("dialog", { name: "Adopt galaxy-s24" });
  fireEvent.click(within(dialog).getByRole("button", { name: "Adopt" }));
  await settle();

  expect(within(dialog).getByRole("alert").textContent).toMatch(/already held by printer/);
  expect(screen.getByRole("dialog", { name: "Adopt galaxy-s24" })).toBeTruthy();
});

it("says so when the reserved range is full", async () => {
  await show("/network/devices/02:5e:00:00:20:01", { NextReservedAddress: () => Response.json({ reservedRange: "198.18.1.20-198.18.1.49" }) });

  fireEvent.click(screen.getByRole("button", { name: "Adopt…" }));
  await settle();

  const dialog = screen.getByRole("dialog", { name: "Adopt galaxy-s24" });
  expect(field(dialog, "Address").value).toBe("");
  expect(dialog.textContent).toMatch(/198\.18\.1\.20-198\.18\.1\.49 is full/);
});
