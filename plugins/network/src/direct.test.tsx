import { act, cleanup, fireEvent, render, screen, within } from "@testing-library/react";
import { createQueryClient, PluginHost, PluginScope } from "@ddash/plugin";
import { MemoryRouter, Route, Routes, useRoutes } from "react-router";
import { afterEach, expect, it, vi } from "vitest";
import plugin from "./index";

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

interface Call {
  method: string;
  body: unknown;
}

const settle = () => act(() => new Promise((r) => setTimeout(r, 50)));

function PluginPages() {
  return useRoutes(plugin.pages!);
}

// Shows the network Plugin at path with network answering by method name
// (Actions {} unless given), and returns the calls with their JSON bodies.
async function show(path: string, answers: Record<string, () => Response> = {}) {
  const calls: Call[] = [];
  const fetch = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const method = String(input).split("/").pop()!;
    const raw = init?.body;
    calls.push({ method, body: raw ? JSON.parse(typeof raw === "string" ? raw : new TextDecoder().decode(raw as Uint8Array)) : undefined });
    const all: Record<string, () => Response> = {
      ListDevices: () => Response.json(devices),
      ...answers,
    };
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
  await settle();
  return calls;
}

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
