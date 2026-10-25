import { cleanup, screen } from "@testing-library/react";
import { metricValue, pending, showWidget, unavailable, type Answer } from "@ddash/plugin/testing";
import { afterEach, expect, it } from "vitest";
import plugin from "./index";
import { network } from "./testing";

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

const show = (answer: Answer) => showWidget(network, widget(), { answers: { ListDevices: answer } });

it("is the devices Widget, S only", () => {
  const w = widget();
  expect([w.title, w.sizes, w.defaultSize]).toEqual(["Devices", ["S"], "S"]);
});

it("asks network for the Devices", async () => {
  const { calls } = await show(() => Response.json(devices));

  expect(calls[0].url).toBe("/api/c/network/ddash.network.NetworkService/ListDevices");
});

it("reports up with the counts, and shows Devices, online and new", async () => {
  const { reported } = await show(() => Response.json(devices));

  expect(reported.status).toEqual({ tone: "up", summary: "5 Devices, 3 online, 2 new" });
  const body = screen.getByRole("region", { name: "widget" });
  expect(metricValue("Devices", body)).toMatch(/5/);
  expect(metricValue("online", body)).toMatch(/3/);
  expect(metricValue("new", body)).toMatch(/2/);
});

it("shows each count loading until network first answers", async () => {
  await show(pending);

  const body = screen.getByRole("region", { name: "widget" });
  for (const name of ["Devices", "online", "new"]) expect(metricValue(name, body)).toBeUndefined();
});

it("leaves New out of the summary when there are none", async () => {
  const { reported } = await show(() => Response.json({ ...devices, devices: devices.devices.slice(0, 3) }));

  expect(reported.status).toEqual({ tone: "up", summary: "3 Devices, 2 online" });
});

it("warns when network can't list the Devices", async () => {
  const { reported } = await show(() => unavailable("can't read the DHCP leases: pfSense unreachable"));

  expect(reported.status).toEqual({ tone: "warn", summary: "Devices unavailable: can't read the DHCP leases: pfSense unreachable" });
});
