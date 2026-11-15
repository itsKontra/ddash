import { act, cleanup, fireEvent, screen, within } from "@testing-library/react";
import type { Concern } from "@ddash/plugin";
import { pending, renderScoped, settle, unavailable, type Answers } from "@ddash/plugin/testing";
import { useState } from "react";
import { afterEach, expect, it } from "vitest";
import { leaseStart } from "./concerns";
import plugin from "./index";
import { network } from "./testing";

afterEach(cleanup);

// What useConcerns reported last, its Actions as labels, "(not a fix)" after a secondary one.
let reported: Concern[] | undefined;
const plain = () => reported?.map(({ actions, ...c }) => ({ ...c, actions: actions?.map((a) => (a.secondary ? `${a.label} (not a fix)` : a.label)) }));

// Runs the network Plugin's useConcerns as the Overview does, drawing the
// Sheet of the Action asked for with open(id, label).
let open: (id: string, label: string) => void = () => {};
function Probe() {
  const concerns = plugin.useConcerns!();
  const [shown, setShown] = useState<{ id: string; label: string } | null>(null);
  reported = concerns;
  open = (id, label) => setShown({ id, label });
  const action = shown && concerns?.find((c) => c.id === shown.id)?.actions?.find((a) => a.label === shown.label);
  return action && "sheet" in action ? action.sheet(() => setShown(null)) : null;
}

async function show(answers: Answers) {
  const calls = renderScoped(network, <Probe />, { answers: { NextReservedAddress: () => Response.json({ ip: "198.18.1.23", reservedRange: "198.18.1.20-198.18.1.49" }), ...answers } });
  await settle();
  return calls;
}

/** A time as pfSense writes a lease's start, in local time. */
function firewallTime(ms: number) {
  const d = new Date(ms);
  const p = (n: number) => String(n).padStart(2, "0");
  return `${d.getFullYear()}/${p(d.getMonth() + 1)}/${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}:${p(d.getSeconds())}`;
}

const tv = { mac: "02:5e:00:00:10:01", name: "Living room TV", ip: "198.18.1.20", online: true, adopted: true };
const phone = {
  mac: "02:5e:00:00:20:01",
  name: "galaxy-s24",
  ip: "198.18.1.150",
  online: true,
  lease: { ip: "198.18.1.150", hostname: "galaxy-s24", starts: firewallTime(Date.now() - 2 * 3600_000 - 60_000), status: "active" },
};
const unnamed = { mac: "02:5e:00:00:20:02", ip: "198.18.1.151", lease: { ip: "198.18.1.151", status: "active" } };
const listed = (devices: object[], blockAlias = "NO_INTERNET") => () => Response.json({ bypassAlias: "VPN_BYPASS", blockAlias, devices });

it("reports each New Device with its MAC, address, hostname and lease, to Adopt or Block", async () => {
  await show({ ListDevices: listed([tv, phone, unnamed]) });

  expect(plain()).toEqual([
    {
      id: "02:5e:00:00:20:01",
      tone: "info",
      subject: "galaxy-s24",
      title: "New device on the network",
      cause: "02:5e:00:00:20:01 · 198.18.1.150 · galaxy-s24 · leased 2 h ago",
      icon: "circle-question-mark",
      href: "/network/devices/02:5e:00:00:20:01",
      actions: ["Adopt", "Block (not a fix)"],
    },
    {
      id: "02:5e:00:00:20:02",
      tone: "info",
      subject: "02:5e:00:00:20:02",
      title: "New device on the network",
      cause: "02:5e:00:00:20:02 · 198.18.1.151",
      icon: "circle-question-mark",
      href: "/network/devices/02:5e:00:00:20:02",
      actions: ["Adopt", "Block (not a fix)"],
    },
  ]);
});

it("offers no Block without a block alias, as the Page offers no No Internet", async () => {
  await show({ ListDevices: listed([phone], "") });

  expect(plain()?.map((c) => c.actions)).toEqual([["Adopt"]]);
});

it("reports none while every Device is Adopted, nothing until network answered, and none when it can't be read", async () => {
  await show({ ListDevices: listed([tv]) });
  expect(reported).toEqual([]);

  cleanup();
  await show({ ListDevices: () => pending() });
  expect(reported).toBeUndefined();

  cleanup();
  await show({ ListDevices: () => unavailable("pfSense is restarting") });
  expect(reported).toEqual([]);
});

it("adopts a New Device with the name asked for in the Page's Adopt Sheet, and drops it once Adopted", async () => {
  let devices: object[] = [tv, phone];
  const calls = await show({
    ListDevices: () => listed(devices)(),
    AdoptDevice: () => {
      devices = [tv, { ...phone, name: "Phone", adopted: true }];
      return Response.json({});
    },
  });

  act(() => open(phone.mac, "Adopt"));
  await settle();
  const sheet = screen.getByRole("dialog", { name: "Adopt galaxy-s24" });
  fireEvent.change(within(sheet).getByRole("textbox", { name: "Name" }), { target: { value: "Phone" } });
  fireEvent.click(within(sheet).getByRole("button", { name: "Adopt" }));
  await settle();

  expect(calls.filter((c) => c.method === "AdoptDevice").map((c) => c.body)).toEqual([{ mac: phone.mac, name: "Phone", ip: "198.18.1.23" }]);
  expect(reported).toEqual([]);
});

it("blocks a New Device as the Page's No Internet does once confirmed, and drops it once network Adopted it", async () => {
  let devices: object[] = [tv, phone];
  const calls = await show({
    ListDevices: () => listed(devices)(),
    SetInternetPath: () => {
      devices = [tv, { ...phone, adopted: true, internetPath: "INTERNET_PATH_NO_INTERNET" }];
      return Response.json({});
    },
  });

  const sets = () => calls.filter((c) => c.method === "SetInternetPath").map((c) => c.body);
  const block = () => {
    const b = reported![0].actions![1];
    act(() => void ("run" in b && b.run()));
  };

  // Nothing is sent until the Operator confirms, nor when they cancel.
  block();
  await settle();
  const dialog = screen.getByRole("alertdialog");
  expect(dialog.textContent).toContain("Block galaxy-s24?");
  expect(dialog.textContent).toContain(
    "network Adopts 02:5e:00:00:20:01 (198.18.1.150, galaxy-s24) at the lowest free reserved address and adds it to NO_INTERNET: it loses Internet access.",
  );
  expect(sets()).toEqual([]);
  fireEvent.click(within(dialog).getByRole("button", { name: "Cancel" }));
  await settle();
  expect(sets()).toEqual([]);

  block();
  await settle();
  fireEvent.click(within(screen.getByRole("alertdialog")).getByRole("button", { name: "Block" }));
  await settle();

  expect(sets()).toEqual([{ mac: phone.mac, path: "INTERNET_PATH_NO_INTERNET" }]);
  expect(reported).toEqual([]);
});

it("says why Block failed on its Device's row", async () => {
  await show({ ListDevices: listed([phone, unnamed]), SetInternetPath: () => unavailable("pfSense refused the change") });

  const block = reported![1].actions![1];
  act(() => void ("run" in block && block.run()));
  await settle();
  fireEvent.click(within(screen.getByRole("alertdialog")).getByRole("button", { name: "Block" }));
  await settle();

  expect(reported!.map((c) => c.actions![1].error)).toEqual([undefined, "pfSense refused the change"]);
});

it("reads a lease's start as pfSense writes it", () => {
  expect(leaseStart("2026/11/15 08:30:05")).toBe(new Date(2026, 10, 15, 8, 30, 5).getTime());
  expect(leaseStart("")).toBeUndefined();
  expect(leaseStart(undefined)).toBeUndefined();
});
