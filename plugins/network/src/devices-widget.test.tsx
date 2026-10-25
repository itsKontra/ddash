import { act, cleanup, render, screen, within } from "@testing-library/react";
import { createQueryClient, PluginHost, PluginScope, type Status, type Widget } from "@ddash/plugin";
import { afterEach, expect, it, vi } from "vitest";
import plugin from "./index";

afterEach(cleanup);

const device = (mac: string, extra: Record<string, unknown>) => ({ mac, ip: "198.18.1.20", ...extra });

// What network answers ListDevices with: three Adopted (two online), two New (one online).
const devices = {
  bypassAlias: "VPN_BYPASS",
  devices: [
    device("02:5e:00:00:10:01", { adopted: true, online: true }),
    device("02:5e:00:00:10:02", { adopted: true, online: true, direct: true }),
    device("02:5e:00:00:10:03", { adopted: true }),
    device("02:5e:00:00:20:01", { online: true }),
    device("02:5e:00:00:20:02", {}),
  ],
};

const widget = () => plugin.widgets!.find((w) => w.id === "devices")!;

async function show(answer: () => Response) {
  const w = widget();
  const fetch = vi.fn(async (_input: RequestInfo | URL) => answer());
  const reported: { status?: Status } = {};
  function Probe({ widget }: { widget: Widget }) {
    reported.status = widget.useStatus();
    return null;
  }
  render(
    <PluginHost client={createQueryClient()}>
      <PluginScope plugin={{ name: "network", requires: ["network", "pfsense-api"] }} fetch={fetch}>
        <Probe widget={w} />
        <section aria-label="widget">
          <w.Body size="S" />
        </section>
      </PluginScope>
    </PluginHost>,
  );
  await act(() => new Promise((r) => setTimeout(r, 50)));
  return { reported, fetch };
}

it("is the devices Widget, S only", () => {
  const w = widget();
  expect([w.title, w.sizes, w.defaultSize]).toEqual(["Devices", ["S"], "S"]);
});

it("asks network for the Devices", async () => {
  const { fetch } = await show(() => Response.json(devices));

  expect(new URL(String(fetch.mock.calls[0][0]), "http://ddash.test").pathname).toBe("/api/c/network/ddash.network.NetworkService/ListDevices");
});

it("reports up with the counts, and shows Devices, online and new", async () => {
  const { reported } = await show(() => Response.json(devices));

  expect(reported.status).toEqual({ tone: "up", summary: "5 Devices, 3 online, 2 new" });
  const body = screen.getByRole("region", { name: "widget" });
  expect(within(body).getByRole("group", { name: "Devices" }).textContent).toMatch(/5/);
  expect(within(body).getByRole("group", { name: "online" }).textContent).toMatch(/3/);
  expect(within(body).getByRole("group", { name: "new" }).textContent).toMatch(/2/);
});

it("shows each count loading until network first answers", async () => {
  await show(() => new Promise<Response>(() => {}) as unknown as Response);

  const body = screen.getByRole("region", { name: "widget" });
  for (const name of ["Devices", "online", "new"]) expect(within(within(body).getByRole("group", { name })).getByRole("status", { name: "Loading" })).toBeTruthy();
});

it("leaves New out of the summary when there are none", async () => {
  const { reported } = await show(() => Response.json({ ...devices, devices: devices.devices.slice(0, 3) }));

  expect(reported.status).toEqual({ tone: "up", summary: "3 Devices, 2 online" });
});

it("warns when network can't list the Devices", async () => {
  const { reported } = await show(() => Response.json({ code: "unavailable", message: "can't read the DHCP leases: pfSense unreachable" }, { status: 503 }));

  expect(reported.status).toEqual({ tone: "warn", summary: "Devices unavailable: can't read the DHCP leases: pfSense unreachable" });
});
