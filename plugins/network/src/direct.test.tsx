import { cleanup, fireEvent, screen, within } from "@testing-library/react";
import { settle, type Answers, type Call } from "@ddash/plugin/testing";
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
const laptop = {
  mac: "02:5e:00:00:10:02",
  name: "Work laptop",
  ip: "198.18.1.21",
  online: true,
  adopted: true,
  direct: true,
  reservation: { interface: "lan", ip: "198.18.1.21", hostname: "work-laptop", description: "Work laptop" },
};
const tablet = {
  mac: "02:5e:00:00:20:02",
  ip: "198.18.1.151",
  lease: { ip: "198.18.1.151", status: "expired", onlineStatus: "idle/offline" },
};
const devices = { bypassAlias: "VPN_BYPASS", devices: [tv, laptop, tablet] };


// Shows the network Plugin at path with network answering by method name
// (Actions {} unless given), and returns the calls with their JSON bodies.
const show = (path: string, answers: Answers = {}) =>
  showNetwork(
    path,
    {
      ListDevices: () => Response.json(devices),
      ...answers,
    },
  );

const setDirect = (calls: Call[]) => calls.filter((c) => c.method === "SetDirect").map((c) => c.body as { mac: string; direct?: boolean });

it("makes a Tunnelled Device Direct with one click on its detail, with no confirm", async () => {
  const calls = await show("/network/devices/02:5e:00:00:10:01");

  const toggle = within(screen.getByRole("region", { name: "Device" })).getByRole("switch", { name: "Direct Living room TV" });
  expect(toggle.getAttribute("aria-checked")).toBe("false");
  fireEvent.click(toggle);
  await settle();

  expect(screen.queryByRole("alertdialog")).toBeNull();
  expect(setDirect(calls)).toEqual([{ mac: "02:5e:00:00:10:01", direct: true }]);
  // Every query is refetched after the Action, so the detail shows it.
  expect(calls.filter((c) => c.method === "ListDevices").length).toBeGreaterThan(1);
});

it("makes a Direct Device Tunnelled again", async () => {
  const calls = await show("/network/devices/02:5e:00:00:10:02");

  const toggle = screen.getByRole("switch", { name: "Direct Work laptop" });
  expect(toggle.getAttribute("aria-checked")).toBe("true");
  expect(screen.getByRole("region", { name: "Device" }).textContent).toMatch(/in VPN_BYPASS/);
  fireEvent.click(toggle);
  await settle();

  expect(setDirect(calls).map((b) => [b.mac, b.direct ?? false])).toEqual([["02:5e:00:00:10:02", false]]);
});

it("toggles a New Device straight away, saying it Adopts it first", async () => {
  const calls = await show("/network/devices/02:5e:00:00:20:02");

  const region = screen.getByRole("region", { name: "Device" });
  expect(region.textContent).toMatch(/Adopts it first/);
  fireEvent.click(within(region).getByRole("switch", { name: "Direct 02:5e:00:00:20:02" }));
  await settle();

  expect(screen.queryByRole("dialog")).toBeNull();
  expect(setDirect(calls)).toEqual([{ mac: "02:5e:00:00:20:02", direct: true }]);
  expect(calls.some((c) => c.method === "AdoptDevice")).toBe(false);
});

it("shows network's refusal next to the toggle", async () => {
  await show("/network/devices/02:5e:00:00:10:02", {
    SetDirect: () =>
      Response.json(
        { code: "failed_precondition", message: "Work laptop stays Direct: VPN_BYPASS holds 198.18.1.21 through 198.18.1.16/28" },
        { status: 400 },
      ),
  });

  fireEvent.click(screen.getByRole("switch", { name: "Direct Work laptop" }));
  await settle();

  expect(within(screen.getByRole("region", { name: "Device" })).getByRole("alert").textContent).toMatch(/stays Direct/);
});
