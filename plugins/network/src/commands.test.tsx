import { create } from "@bufbuild/protobuf";
import { cleanup, fireEvent, screen, within } from "@testing-library/react";
import { pending, settle, showCommands, type Answers } from "@ddash/plugin/testing";
import { DeviceSchema } from "@ddash/proto/network/network_pb";
import { afterEach, expect, it, vi } from "vitest";
import { networkCommands } from "./commands";
import plugin from "./index";
import { network } from "./testing";

afterEach(cleanup);

const devices = {
  devices: [
    { mac: "02:5e:00:00:10:01", name: "nas", ip: "198.18.1.20", online: true, adopted: true },
    { mac: "02:5e:00:00:10:02", ip: "198.18.1.150", adopted: false, lease: { ip: "198.18.1.150", hostname: "espressif" } },
  ],
};
const remoteAccess = { tunnel: "tun_wg1", interface: "REMOTEACCESS", peers: [] };
const base: Answers = { ListDevices: () => Response.json(devices), GetRemoteAccess: () => Response.json(remoteAccess) };

it("offers each Device, found by its address, MAC or hostname, and Add a Remote Access device", async () => {
  const shown = await showCommands(plugin, network, { answers: base });

  expect(shown.commands()?.map((c) => [c.title, c.hint])).toEqual([
    ["Go to nas", "Device · 198.18.1.20"],
    ["Go to 02:5e:00:00:10:02", "New Device · 198.18.1.150"],
    ["Add a Remote Access device", "WireGuard on tun_wg1"],
  ]);
  expect(shown.commands()?.[1]?.keywords).toEqual(expect.arrayContaining(["198.18.1.150", "02:5e:00:00:10:02", "espressif", "new"]));
});

it("goes to the Device's detail", () => {
  const go = vi.fn();
  const [nas] = networkCommands([create(DeviceSchema, devices.devices[0])], undefined, go);
  if (!nas || !("run" in nas)) throw new Error("no Go to nas");
  nas.run();
  expect(go).toHaveBeenCalledWith("/network/devices/02:5e:00:00:10:01");
});

it("opens the Page's own Add a device Dialog on any Page, adding nothing before it is submitted", async () => {
  const shown = await showCommands(plugin, network, { answers: base });

  await shown.choose("Add a Remote Access device");
  const dialog = screen.getByRole("dialog", { name: "Add a device" });
  expect(shown.calls.filter((c) => c.method === "AddRemoteAccessPeer")).toEqual([]);
  fireEvent.click(within(dialog).getByRole("button", { name: "Cancel" }));
  await settle();
  expect(screen.queryByRole("dialog", { name: "Add a device" })).toBeNull();
});

it("offers no Add a Remote Access device without a Remote Access tunnel, and nothing until the Devices are listed", async () => {
  const without = await showCommands(plugin, network, { answers: { ...base, GetRemoteAccess: () => Response.json({ error: "no WireGuard tunnel without a gateway" }) } });
  expect(without.commands()?.map((c) => c.title)).toEqual(["Go to nas", "Go to 02:5e:00:00:10:02"]);

  cleanup();
  const listing = await showCommands(plugin, network, { answers: { ...base, ListDevices: () => pending() } });
  expect(listing.commands()).toBeUndefined();
});
