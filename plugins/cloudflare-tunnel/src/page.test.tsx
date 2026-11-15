import { PlanSchema, RunActionResponseSchema, System } from "@ddash/proto/cloudflare-tunnel/cloudflare_tunnel_pb";
import { notConfigured, settle, unavailable } from "@ddash/plugin/testing";
import { cleanup, fireEvent, screen, within } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import { bodies, failing, listed, plan, ranThrough, show, step, tunnel } from "./testing";

afterEach(cleanup);

const panel = (name: string) => screen.getByRole("region", { name });
const row = (hostname: string) => screen.getByRole("rowheader", { name: hostname }).closest("tr")!;
const dialog = () => within(screen.getByRole("dialog"));
/** The Dialog's steps as their text. */
const steps = () =>
  dialog()
    .getAllByRole("listitem")
    .filter((li) => li.parentElement?.tagName === "OL")
    .map((li) => li.textContent);
const openStrip = async (hostname: string) => {
  fireEvent.click(within(row(hostname)).getByRole("button", { name: `Actions for ${hostname}` }));
  await settle();
  return within(screen.getByRole("group", { name: `Actions for ${hostname}` }));
};

describe("the connectors", () => {
  it("show the Tunnel's state and each cloudflared with its connections", async () => {
    await show();
    const p = within(panel("Connectors"));

    expect(p.getByText("Up")).toBeTruthy();
    expect(p.getByText("home: Every connector is connected.")).toBeTruthy();
    expect(p.getByText("cloudflared 2025.10.0 · linux_amd64")).toBeTruthy();
    expect(p.getByText("2 connections: fra06, ams08 · from 203.0.113.7")).toBeTruthy();
    expect(p.getByText("cloudflared 2025.9.1 · linux_arm64")).toBeTruthy();
    expect(p.getByText("2 connectors, 4 connections in all.")).toBeTruthy();
  });

  it("say the Tunnel is down without a connector", async () => {
    await show({ GetTunnel: () => Response.json({ setUp: true, tunnel: { ...tunnel.tunnel, status: "TUNNEL_STATUS_DOWN", connectors: [] } }) });
    const p = within(panel("Connectors"));

    expect(p.getByText("Down")).toBeTruthy();
    expect(p.getByText(/No connector is connected: run cloudflared/)).toBeTruthy();
  });

  it("say why cloudflare-api can't read the Tunnel", async () => {
    await show({ GetTunnel: () => unavailable("Cloudflare refused the API token: Authentication error (10000)") });

    expect(within(panel("Connectors")).getByText(/can't read the Tunnel: Cloudflare refused the API token/)).toBeTruthy();
  });
});

describe("the Public Hostnames", () => {
  it("list each hostname with its local service, state and DNS", async () => {
    await show();

    const cells = (h: string) => [...row(h).querySelectorAll("th, td")].map((c) => c.textContent);
    expect(cells("request.home.example").slice(0, 4)).toEqual(["request.home.example", "http://198.18.1.212:5055", "Enabled", "Proxied CNAME"]);
    expect(cells("ha.home.example").slice(0, 4)).toEqual(["ha.home.example", "http://198.18.1.101:8123", "Enabled", "DNS only"]);
    expect(cells("video.home.example").slice(0, 4)).toEqual(["video.home.example", "http://198.18.1.212:8096", "Disabled", "–"]);
    expect(within(panel("Public Hostnames")).getByText(/A Public Hostname's DNS doesn't route to the Tunnel/)).toBeTruthy();
    expect(within(panel("Public Hostnames")).getByText("Any other hostname gets http_status:404.")).toBeTruthy();
  });

  it("are added through a Plan Dialog", async () => {
    const calls = await show({ RunAction: ranThrough(2) });
    fireEvent.click(screen.getByRole("button", { name: "Add Public Hostname" }));
    await settle();
    fireEvent.change(dialog().getByLabelText("Hostname"), { target: { value: " grafana.home.example " } });
    fireEvent.change(dialog().getByLabelText("Local service"), { target: { value: "http://198.18.1.204:3000" } });
    fireEvent.click(dialog().getByRole("button", { name: "Review Plan" }));
    await settle();

    const add = { action: { add: { fields: { hostname: "grafana.home.example", service: "http://198.18.1.204:3000" } } } };
    expect(bodies(calls, "PlanAction")).toEqual([add]);
    expect(steps()).toEqual([
      expect.stringContaining("Add the ingress rule for grafana.home.example"),
      expect.stringContaining("Create the CNAME grafana.home.example"),
    ]);
    expect(steps()[1]).toContain("Cloudflare DNS");

    fireEvent.click(dialog().getByRole("button", { name: "Add" }));
    await settle();

    expect(bodies(calls, "RunAction")).toEqual([{ ...add, plan }]);
    expect(steps().map((s) => s?.match(/done|failed|not run/)?.[0])).toEqual(["done", "done"]);
    fireEvent.click(dialog().getByRole("button", { name: "Done" }));
    await settle();
    expect(screen.queryByRole("dialog")).toBeNull();
  });

  it.each([
    ["request.home.example", "Disable…", "Disable", { disable: { name: { hostname: "request.home.example" } } }],
    ["request.home.example", "Remove…", "Remove", { remove: { name: { hostname: "request.home.example" } } }],
    ["video.home.example", "Enable…", "Enable", { enable: { name: { hostname: "video.home.example" } } }],
  ])("%s's %s plans and runs its Action", async (hostname, button, confirm, action) => {
    const calls = await show({ RunAction: ranThrough(2) });
    const strip = await openStrip(hostname);
    fireEvent.click(strip.getByRole("button", { name: button }));
    await settle();
    fireEvent.click(dialog().getByRole("button", { name: confirm }));
    await settle();

    // Getters refetch after an Action: the Plan is asked for again.
    expect(bodies(calls, "PlanAction")[0]).toEqual({ action });
    expect(bodies(calls, "RunAction")).toEqual([{ action, plan }]);
  });

  it("are edited from their row, the form filled with what they have", async () => {
    const calls = await show();
    const strip = await openStrip("ha.home.example");
    fireEvent.click(strip.getByRole("button", { name: "Edit…" }));
    await settle();

    expect((dialog().getByLabelText("Hostname") as HTMLInputElement).value).toBe("ha.home.example");
    fireEvent.change(dialog().getByLabelText("Local service"), { target: { value: "http://198.18.1.101:8124" } });
    fireEvent.click(dialog().getByRole("button", { name: "Review Plan" }));
    await settle();

    expect(bodies(calls, "PlanAction")).toEqual([
      { action: { edit: { name: { hostname: "ha.home.example" }, fields: { hostname: "ha.home.example", service: "http://198.18.1.101:8124" } } } },
    ]);
  });
});

describe("a Plan Dialog", () => {
  const openDisable = async () => {
    const strip = await openStrip("request.home.example");
    fireEvent.click(strip.getByRole("button", { name: "Disable…" }));
    await settle();
  };

  it("says why its Plan can't run, and keeps it from running", async () => {
    const blocked = "vpn.home.example has a DNS record already (A 203.0.113.7): remove it in Cloudflare first.";
    await show({ PlanAction: () => Response.json({ plan: { ...plan, blocked: [blocked] } }) });
    await openDisable();

    expect(dialog().getByText(blocked)).toBeTruthy();
    expect((dialog().getByRole("button", { name: "Disable" }) as HTMLButtonElement).disabled).toBe(true);
  });

  it("shows the new Plan when Cloudflare changed since, and runs it on a second confirm", async () => {
    const changed = { steps: [{ system: System.CLOUDFLARE_TUNNEL, title: "Remove the ingress rule for request.home.example" }] };
    let n = 0;
    const calls = await show({
      RunAction: () => (++n === 1 ? failing("aborted", "changed since you looked: confirm the new Plan", PlanSchema, changed) : ranThrough(1))(),
    });
    await openDisable();
    fireEvent.click(dialog().getByRole("button", { name: "Disable" }));
    await settle();

    expect(dialog().getByText(/Changed since you looked/)).toBeTruthy();
    expect(steps()).toEqual([expect.stringContaining("Remove the ingress rule for request.home.example")]);
    fireEvent.click(dialog().getByRole("button", { name: "Disable" }));
    await settle();
    expect((bodies(calls, "RunAction")[1] as { plan: object }).plan).toEqual({ steps: [{ system: "SYSTEM_CLOUDFLARE_TUNNEL", title: "Remove the ingress rule for request.home.example" }] });
  });

  it("says where a run stopped, the steps after it not run", async () => {
    const stopped = "Stopped at step 2 of 2, Create the CNAME grafana.home.example: Cloudflare answered 500. Done: Add the ingress rule for grafana.home.example. Nothing was rolled back.";
    await show({ RunAction: failing("unavailable", stopped, RunActionResponseSchema, { done: 1 }, [{ steps: [step.done, step.failed] }]) });
    await openDisable();
    fireEvent.click(dialog().getByRole("button", { name: "Disable" }));
    await settle();

    expect(dialog().getByText(stopped)).toBeTruthy();
    expect(steps().map((s) => s?.match(/done|failed|not run/)?.[0])).toEqual(["done", "failed"]);
  });
});

describe("not set up", () => {
  it.each([
    ["an empty config", () => Response.json({})],
    ["no config", notConfigured],
  ])("with %s, links to Plugins to configure cloudflare-api", async (_, answer) => {
    await show({ GetTunnel: answer, ListPublicHostnames: () => Response.json({}) });

    expect(screen.getByText("Set up the Cloudflare Tunnel")).toBeTruthy();
    expect(screen.getByRole("link", { name: "Configure it in Plugins" }).getAttribute("href")).toBe("/plugins");
    expect(screen.queryByRole("button", { name: "Add Public Hostname" })).toBeNull();
  });
});

it("shows a locally managed Tunnel read only", async () => {
  await show({ ListPublicHostnames: () => Response.json({ ...listed, remotelyManaged: false }) });

  expect(screen.getByText(/runs from cloudflared's config.yml/)).toBeTruthy();
  expect((screen.getByRole("button", { name: "Add Public Hostname" }) as HTMLButtonElement).disabled).toBe(true);
  expect(within(row("request.home.example")).queryByRole("button", { name: "Actions for request.home.example" })).toBeNull();
});
