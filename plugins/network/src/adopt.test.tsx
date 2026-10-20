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
const phone = {
  mac: "02:5e:00:00:20:01",
  name: "galaxy-s24",
  ip: "198.18.1.150",
  online: true,
  lease: { ip: "198.18.1.150", hostname: "galaxy-s24", status: "active", onlineStatus: "active/online" },
};
const devices = { bypassAlias: "VPN_BYPASS", devices: [tv, phone] };
const next = { ip: "198.18.1.23", reservedRange: "198.18.1.20-198.18.1.49" };

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
      NextReservedAddress: () => Response.json(next),
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

const field = (dialog: HTMLElement, name: string) => within(dialog).getByRole("textbox", { name }) as HTMLInputElement;

it("adopts a New Device from the Devices table with the lowest free reserved address pre-filled", async () => {
  const calls = await show("/network");

  const table = screen.getByRole("table", { name: "Devices" });
  const row = within(table).getByRole("row", { name: /galaxy-s24/ });
  expect(within(within(table).getByRole("row", { name: /Living room TV/ })).queryByRole("button")).toBeNull();
  fireEvent.click(within(row).getByRole("button", { name: "Adopt galaxy-s24" }));
  await settle();

  const dialog = screen.getByRole("dialog", { name: "Adopt galaxy-s24" });
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

it("adopts a New Device from its detail; an Adopted one offers rename instead", async () => {
  await show("/network/devices/02:5e:00:00:20:01");
  const summary = screen.getByRole("region", { name: "Device" });
  expect(within(summary).getByRole("button", { name: "Adopt galaxy-s24" })).toBeTruthy();
  expect(within(summary).queryByRole("button", { name: /Rename/ })).toBeNull();
  cleanup();

  await show("/network/devices/02:5e:00:00:10:01");
  const adopted = screen.getByRole("region", { name: "Device" });
  expect(within(adopted).queryByRole("button", { name: /Adopt/ })).toBeNull();
  expect(within(adopted).getByRole("button", { name: "Rename Living room TV" })).toBeTruthy();
});

it("renames an Adopted Device from its detail, leaving the address to pfSense", async () => {
  const calls = await show("/network/devices/02:5e:00:00:10:01");

  fireEvent.click(screen.getByRole("button", { name: "Rename Living room TV" }));
  const dialog = screen.getByRole("dialog", { name: "Rename Living room TV" });
  expect(field(dialog, "Name").value).toBe("Living room TV");
  expect(dialog.textContent).toMatch(/198\.18\.1\.20/);
  expect(dialog.textContent).toMatch(/in pfSense/);
  expect(within(dialog).queryByRole("textbox", { name: "Address" })).toBeNull();

  fireEvent.change(field(dialog, "Name"), { target: { value: "Kitchen TV" } });
  fireEvent.click(within(dialog).getByRole("button", { name: "Rename" }));
  await settle();

  expect(calls.filter((c) => c.method === "RenameDevice").map((c) => c.body)).toEqual([{ mac: "02:5e:00:00:10:01", name: "Kitchen TV" }]);
  expect(screen.queryByRole("dialog")).toBeNull();
});

it("keeps the dialog open with network's refusal", async () => {
  await show("/network/devices/02:5e:00:00:20:01", {
    AdoptDevice: () => Response.json({ code: "already_exists", message: "198.18.1.23 is already held by printer" }, { status: 409 }),
  });

  fireEvent.click(screen.getByRole("button", { name: "Adopt galaxy-s24" }));
  await settle();
  const dialog = screen.getByRole("dialog", { name: "Adopt galaxy-s24" });
  fireEvent.click(within(dialog).getByRole("button", { name: "Adopt" }));
  await settle();

  expect(within(dialog).getByRole("alert").textContent).toMatch(/already held by printer/);
  expect(screen.getByRole("dialog", { name: "Adopt galaxy-s24" })).toBeTruthy();
});

it("says so when the reserved range is full", async () => {
  await show("/network/devices/02:5e:00:00:20:01", { NextReservedAddress: () => Response.json({ reservedRange: "198.18.1.20-198.18.1.49" }) });

  fireEvent.click(screen.getByRole("button", { name: "Adopt galaxy-s24" }));
  await settle();

  const dialog = screen.getByRole("dialog", { name: "Adopt galaxy-s24" });
  expect(field(dialog, "Address").value).toBe("");
  expect(dialog.textContent).toMatch(/198\.18\.1\.20-198\.18\.1\.49 is full/);
});
