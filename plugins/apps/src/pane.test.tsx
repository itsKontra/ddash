import { cleanup, screen, within } from "@testing-library/react";
import { afterEach, expect, it } from "vitest";
import { settle } from "@ddash/plugin/testing";
import plugin from "./index";
import { renderScoped, split } from "./testing";

afterEach(cleanup);

const Page = () => {
  const P = plugin.pages![0].Component!;
  return <P />;
};

/** Shows the App id names in the pane, with the Plugins named installed. */
const open = async (id: string, installed = ["apps"], answers = {}) => {
  const calls = renderScoped(<Page />, { ListApps: () => Response.json(split), ...answers }, installed, `/?app=${encodeURIComponent(id)}`);
  await settle();
  return calls;
};

/** The selected App's pane. */
const pane = () => screen.getByRole("article");
const panel = (name: string) => within(pane()).getByRole("region", { name });
/** A Panel's facts as label → text. */
const facts = (name: string) => {
  const out: Record<string, string> = {};
  for (const dt of within(panel(name)).queryAllByRole("term")) out[dt.textContent!] = dt.nextElementSibling!.textContent!;
  return out;
};
/** The pane's Panels and Warning boxes, in order. */
const regions = () =>
  within(pane())
    .getAllByRole("region")
    .map((r) => r.getAttribute("aria-label") ?? r.querySelector("h2")?.textContent);

it("shows a proxied App: its header, every Panel, the Proxy Host and its certificate's state linking to the Reverse Proxy", async () => {
  await open("proxy:grafana.home.example", ["apps", "reverse-proxy", "proxmox"]);

  const header = screen.getByRole("heading", { level: 2, name: "Grafana" }).parentElement!;
  expect(within(header).getByRole("link", { name: "https://grafana.home.example" })).toBeTruthy();
  expect(header.textContent).toContain("Proxied");
  expect(header.textContent).toContain("up");
  expect(regions()).toEqual(["Display", "Address and DNS", "Reverse proxy", "Workload and Check", "Network"]);

  expect(facts("Display")).toEqual({ Name: "Grafana (from the URL)", Icon: "Automatic", Group: "Monitoring", Hidden: "No" });
  expect(facts("Address and DNS")).toMatchObject({
    URL: "https://grafana.home.example",
    Aliases: "None",
    "grafana.home.example": "alias of nginx.home.example (the Reverse Proxy)",
  });
  expect(facts("Reverse proxy")).toMatchObject({
    Upstream: "http://198.18.1.204:3000",
    Options: "Block Common Exploits · Force SSL · HTTP/2",
    "Proxy Host": "#3 in Nginx Proxy Manager",
  });
  const cert = within(panel("Reverse proxy")).getByRole("link", { name: "Not renewing: see the Reverse Proxy" });
  expect(cert.getAttribute("href")).toBe("/reverse-proxy");
  expect(facts("Reverse proxy").Certificate).toContain("*.home.example");
  expect(facts("Reverse proxy").Certificate).toContain("check its Let's Encrypt log");

  const workload = within(panel("Workload and Check"));
  expect(workload.getByRole("link", { name: "LXC grafana · running" }).getAttribute("href")).toBe("/proxmox/workloads/120");
  expect(workload.getByRole("status", { name: "Check" }).textContent).toBe("up");
  const bars = workload.getByRole("img", { name: "Uptime, last 30 days" });
  expect(bars.querySelectorAll('rect[data-tone="down"]')).toHaveLength(1);
  expect(bars.querySelectorAll('rect[data-tone="up"]')).toHaveLength(29);
});

it("labels a disabled Proxy Host, with no Warning for it", async () => {
  await open("proxy:printer.home.example");

  expect(within(panel("Reverse proxy")).getByText("Proxy Host disabled")).toBeTruthy();
  expect(facts("Reverse proxy").Certificate).toBe("None: plain HTTP");
  // Its only Warning, No TLS, is Ignored.
  expect(screen.queryByRole("region", { name: "No TLS" })).toBeNull();
});

it("shows a Not-proxied App: its Host Override and alias, and Add reverse proxy…", async () => {
  await open("app:5a5a5a5a5a5a5a5a");

  expect(screen.getByRole("heading", { level: 2, name: "NAS" }).parentElement!.textContent).toContain("Not proxied");
  expect(facts("Address and DNS")).toMatchObject({
    Aliases: "media.home.example",
    "nas.home.example": "Host Override → 198.18.1.20",
    "media.home.example": "alias of nas.home.example → 198.18.1.20",
  });
  expect(within(panel("Reverse proxy")).getByText("Not proxied: pfSense resolves nas.home.example to 198.18.1.20.")).toBeTruthy();
  expect((within(panel("Reverse proxy")).getByRole("button", { name: "Add reverse proxy…" }) as HTMLButtonElement).disabled).toBe(false);
  expect(within(panel("Workload and Check")).getByText("override")).toBeTruthy();
});

it("shows a Link-only App: no DNS, no Proxy Host", async () => {
  await open("app:00aa11bb22cc33dd");

  expect(screen.getByRole("heading", { level: 2, name: "pfSense" }).parentElement!.textContent).toContain("Link only");
  expect(within(panel("Address and DNS")).getByText("A link only: nothing in pfSense.")).toBeTruthy();
  expect(within(panel("Reverse proxy")).getByText("A link only: no Proxy Host.")).toBeTruthy();
  expect(within(panel("Workload and Check")).getByText("No Workload matched.")).toBeTruthy();
  expect(within(panel("Network")).getByText("No Workload: no address to read.")).toBeTruthy();
});

it("shows orphaned App settings in their own pane: what was kept, and no Panels", async () => {
  await open("proxy:paperless.home.example");

  expect(screen.getByRole("heading", { level: 2, name: "Documents" }).parentElement!.textContent).toContain("Orphaned App settings");
  expect(facts("Kept")).toEqual({
    "Kept for": "paperless.home.example",
    Name: "Documents",
    Icon: "Automatic",
    Group: "Office",
    Hidden: "No",
    Workload: "LXC 130",
    "App id": "proxy:paperless.home.example",
  });
  expect(screen.queryByRole("region", { name: "Display" })).toBeNull();
  expect(screen.queryByRole("button", { name: "Delete…" })).toBeNull();
});

it("shows the Network Panel's path, Ads Allowed and the Device link", async () => {
  await open("app:5a5a5a5a5a5a5a5a", ["apps", "network"]);

  const net = within(panel("Network"));
  expect(net.getByText("No Internet")).toBeTruthy();
  expect(net.getByText("Ads blocked")).toBeTruthy();
  expect(facts("Network").Address).toBe("198.18.1.20");
  expect(net.getByRole("link", { name: "nas" }).getAttribute("href")).toBe("/network/devices/02:1c:d5:5e:3d:e5");
});

it("shows a Docker App its host's network info", async () => {
  await open("proxy:jellyfin.home.example");

  const net = within(panel("Network"));
  expect(net.getByText("Direct")).toBeTruthy();
  expect(net.getByText("Ads Allowed")).toBeTruthy();
  expect(facts("Network")).toEqual({ Address: "198.18.2.10 (the Docker host media)", Device: "None: a fixed address" });
});

it("names the Device without a link while the network Plugin isn't installed", async () => {
  await open("app:5a5a5a5a5a5a5a5a");

  expect(facts("Network").Device).toBe("nas");
  expect(within(panel("Network")).queryByRole("link")).toBeNull();
});

it("says why there's no network info while network can't answer, and hides Ads Allowed while AdGuard can't", async () => {
  await open("proxy:grafana.home.example", ["apps"], { ListApps: () => Response.json({ ...split, apps: split.apps.map((a) => ({ ...a, network: undefined })), networkError: "network not configured" }) });
  expect(within(panel("Network")).getByText("Network info unavailable: network not configured")).toBeTruthy();

  cleanup();
  await open("proxy:grafana.home.example", ["apps"], { ListApps: () => Response.json({ ...split, adguardError: "can't read AdGuard's clients: timeout" }) });
  expect(within(panel("Network")).queryByText(/Ads/)?.textContent).toBe("Ads Allowed unknown: can't read AdGuard's clients: timeout");
});
