import { cleanup, screen, within } from "@testing-library/react";
import { pending, showWidget, unavailable, type Answer } from "@ddash/plugin/testing";
import { afterEach, expect, it } from "vitest";
import plugin from "./index";
import { network } from "./testing";

afterEach(cleanup);

const minutesAgo = (m: number) => new Date(Date.now() - m * 60_000 - 5_000).toISOString();

const peer = (name: string, extra: Record<string, unknown> = {}) => ({ name, publicKey: `${name}-key`, addresses: ["198.18.40.2/32"], ...extra });

// What network answers GetRemoteAccess with: one peer connected, one not
// lately, one never.
const remoteAccess = {
  tunnel: "tun_wg1",
  interface: "REMOTEACCESS",
  peers: [peer("Phone", { latestHandshake: minutesAgo(1) }), peer("Laptop", { latestHandshake: minutesAgo(90) }), peer("Tablet")],
};

const widget = () => plugin.widgets!.find((w) => w.id === "remote-access")!;

const show = (answer: Answer, size?: "S" | "M") => showWidget(network, widget(), { answers: { GetRemoteAccess: answer }, size });

it("is the Remote Access Widget, S or M", () => {
  const w = widget();
  expect([w.title, w.sizes, w.defaultSize]).toEqual(["Remote Access", ["S", "M"], "S"]);
});

it("reports up with the peers and how many are connected, and lists each with its last handshake", async () => {
  const { reported, calls } = await show(() => Response.json(remoteAccess));

  expect(calls[0].url).toBe("/api/c/network/ddash.network.NetworkService/GetRemoteAccess");
  expect(reported.status).toEqual({ tone: "up", summary: "3 peers, 1 connected" });
  const items = within(screen.getByRole("region", { name: "widget" }))
    .getAllByRole("listitem")
    .map((li) => li.textContent);
  expect(items).toEqual(["Phone1 min ago", "Laptop1 h ago", "Tabletnever connected"]);
  expect(screen.getByRole("button", { name: "Add a device" })).toBeTruthy();
});

it("lists 3 peers at S, and says how many more there are", async () => {
  const many = { ...remoteAccess, peers: [...remoteAccess.peers, peer("Watch"), peer("Car")] };

  await show(() => Response.json(many));

  const body = screen.getByRole("region", { name: "widget" });
  expect(within(body).getAllByRole("listitem")).toHaveLength(3);
  expect(body.textContent).toMatch(/and 2 more/);
});

it("lists more peers at M", async () => {
  const many = { ...remoteAccess, peers: [...remoteAccess.peers, peer("Watch"), peer("Car")] };

  await show(() => Response.json(many), "M");

  expect(within(screen.getByRole("region", { name: "widget" })).getAllByRole("listitem")).toHaveLength(5);
});

it("invites the first device when there are no peers", async () => {
  await show(() => Response.json({ ...remoteAccess, peers: [] }));

  expect(screen.getByRole("region", { name: "widget" }).textContent).toMatch(/No device has Remote Access yet/);
  expect(screen.getByRole("button", { name: "Add a device" })).toBeTruthy();
});

it("loads until network first answers", async () => {
  const { reported } = await show(pending);

  expect(reported.status).toBeUndefined();
  expect(screen.queryByRole("button", { name: "Add a device" })).toBeNull();
});

it("warns when the firewall has no Remote Access tunnel", async () => {
  const error = "pfSense has no Remote Access tunnel (a WireGuard tunnel on an interface without a gateway)";

  const { reported } = await show(() => Response.json({ error }));

  expect(reported.status).toEqual({ tone: "warn", summary: error });
  expect(screen.getByRole("region", { name: "widget" }).textContent).toContain(error);
  expect(screen.queryByRole("button", { name: "Add a device" })).toBeNull();
});

it("warns when network can't read Remote Access", async () => {
  const { reported } = await show(() => unavailable("can't read the WireGuard tunnels: pfSense unreachable"));

  expect(reported.status).toEqual({ tone: "warn", summary: "Remote Access unavailable: can't read the WireGuard tunnels: pfSense unreachable" });
});
