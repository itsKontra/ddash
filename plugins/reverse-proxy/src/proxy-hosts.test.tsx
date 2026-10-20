import { cleanup, screen, within } from "@testing-library/react";
import { afterEach, expect, it } from "vitest";
import { Page, renderScoped, settle, type Answers } from "./testing";

afterEach(cleanup);

const day = 86_400_000;
const in5Days = new Date(Date.now() + 5 * day + 3_600_000).toISOString();
const in60Days = "2027-01-10T12:00:00Z";

// GetSnapshot's answer (Connect JSON): three Proxy Hosts, jellyfin's
// port-80 redirect and nginx's catch-all, which forward nothing.
const snapshot = {
  snapshot: {
    revision: "7",
    readAt: "2026-10-20T07:15:00Z",
    servers: [
      {
        file: "/etc/nginx/conf.d/jellyfin.conf",
        line: "1",
        serverNames: ["jellyfin.home.example"],
        listens: [{ address: "80" }],
        return: "301 https://$host$request_uri",
      },
      {
        file: "/etc/nginx/conf.d/jellyfin.conf",
        line: "6",
        serverNames: ["jellyfin.home.example", "media.home.example"],
        listens: [{ address: "443", ssl: true }, { address: "198.18.2.5:443", ssl: true }],
        sslCertificate: "/etc/letsencrypt/live/jellyfin.home.example/fullchain.pem",
        locations: [
          { path: "/socket", proxyPass: "http://198.18.2.20:8097" },
          { path: "/", proxyPass: "http://198.18.2.20:8096" },
        ],
      },
      {
        file: "/etc/nginx/conf.d/grafana.conf",
        line: "5",
        serverNames: ["grafana.home.example"],
        listens: [{ address: "443", ssl: true }],
        sslCertificate: "/etc/letsencrypt/live/grafana.home.example/fullchain.pem",
        locations: [{ path: "/", proxyPass: "http://grafana" }],
      },
      {
        file: "/etc/nginx/conf.d/printer.conf",
        line: "1",
        serverNames: ["printer.home.example"],
        listens: [{ address: "8080" }],
        locations: [{ path: "/", proxyPass: "http://198.18.2.99" }],
      },
      {
        file: "/etc/nginx/conf.d/default.conf",
        line: "1",
        serverNames: ["_"],
        listens: [{ address: "443", ssl: true, defaultServer: true }],
        return: "444",
      },
    ],
    upstreams: [{ name: "grafana", servers: ["198.18.2.30:3000", "198.18.2.31:3000"] }],
  },
};

// ListApps' answer: Jellyfin on a container, Grafana on an LXC, the
// Printer on nothing, and a manual App that is no Proxy Host.
const apps = {
  apps: [
    {
      id: "proxy:grafana.home.example",
      serverName: "grafana.home.example",
      name: "Grafana",
      url: "https://grafana.home.example",
      workload: { ref: { kind: "KIND_LXC", host: "pve", id: "120" }, name: "grafana", state: "running" },
    },
    {
      id: "proxy:jellyfin.home.example",
      serverName: "jellyfin.home.example",
      name: "Jellyfin",
      url: "https://jellyfin.home.example",
      aliases: ["media.home.example"],
      workload: { ref: { kind: "KIND_DOCKER", host: "media", id: "jellyfin" }, name: "jellyfin", state: "running" },
    },
    { id: "manual:00aa11bb22cc33dd", name: "pfSense", url: "https://198.18.0.1", manual: true },
    { id: "proxy:printer.home.example", serverName: "printer.home.example", name: "Printer", url: "http://printer.home.example:8080" },
  ],
};

const appCheck = (id: number, appId: string, state: string, extra: Record<string, unknown> = {}) => ({
  id: String(id),
  appId,
  spec: { name: appId, type: "CHECK_TYPE_HTTP", target: "https://app.home.example" },
  state,
  ...extra,
});

// ListChecks' answer: Jellyfin up with its certificate expiring in 5 days,
// Grafana up with two months left, the Printer down, and a manual Check.
const checks = {
  checks: [
    appCheck(1, "proxy:jellyfin.home.example", "STATE_UP", { certExpires: in5Days, certExpiring: true }),
    appCheck(2, "proxy:grafana.home.example", "STATE_UP", { certExpires: in60Days }),
    appCheck(3, "proxy:printer.home.example", "STATE_DOWN", { reason: "connection refused" }),
    { id: "4", spec: { name: "nas", type: "CHECK_TYPE_TCP", target: "198.18.1.10:22" }, state: "STATE_DOWN" },
  ],
};

const answers: Answers = {
  GetSnapshot: () => Response.json(snapshot),
  ListApps: () => Response.json(apps),
  ListChecks: () => Response.json(checks),
};

async function show(over: Answers = {}, installed?: string[]) {
  const calls = renderScoped(<Page />, { ...answers, ...over }, installed);
  await settle();
  return calls;
}

const table = () => screen.getByRole("table", { name: "Proxy Hosts" });
const row = (name: string) => within(table()).getByRole("row", { name });
const datetime = (el: HTMLElement) => within(el).getByText((_, e) => e?.tagName === "TIME").getAttribute("datetime");

it("lists every server block that forwards to an upstream, by domain", async () => {
  await show();

  const rows = within(table()).getAllByRole("row").slice(1);
  expect(rows.map((r) => r.getAttribute("aria-label"))).toEqual(["grafana.home.example", "jellyfin.home.example", "printer.home.example"]);
});

it("shows a Proxy Host's domains, upstream, App with its Check status, Workload and certificate expiry", async () => {
  await show();

  const jellyfin = row("jellyfin.home.example");
  expect(jellyfin.textContent).toMatch(/media\.home\.example/);
  expect(within(jellyfin).getByLabelText("Upstream").textContent).toBe("http://198.18.2.20:8096");
  expect(within(jellyfin).getByLabelText("App").textContent).toBe("Jellyfin");
  expect(within(jellyfin).getByRole("img", { name: "Check: up, certificate expires in 5 days" })).toBeTruthy();
  expect(within(jellyfin).getByLabelText("Workload").textContent).toBe("Docker jellyfin · running");
  const cert = within(jellyfin).getByLabelText("Certificate");
  expect(cert.textContent).toMatch(/expires in 5 days/);
  expect(datetime(cert)).toBe(new Date(in5Days).toISOString());

  const grafana = row("grafana.home.example");
  expect(within(grafana).getByRole("img", { name: "Check: up" })).toBeTruthy();
  expect(datetime(within(grafana).getByLabelText("Certificate"))).toBe("2027-01-10T12:00:00.000Z");

  const printer = row("printer.home.example");
  expect(within(printer).getByRole("img", { name: "Check: down: connection refused" })).toBeTruthy();
  expect(within(printer).getByLabelText("Workload").textContent).toBe("none");
  expect(within(printer).getByLabelText("Certificate").textContent).toBe("—");
});

it("shows an upstream block's servers next to its name", async () => {
  await show();

  expect(within(row("grafana.home.example")).getByLabelText("Upstream").textContent).toBe("http://grafana → 198.18.2.30:3000, 198.18.2.31:3000");
});

it("links the App to the apps Page and the Workload to its Plugin's Page when they are installed", async () => {
  await show({}, ["reverse-proxy", "apps", "proxmox"]);

  const grafana = row("grafana.home.example");
  expect(within(within(grafana).getByLabelText("App")).getByRole("link").getAttribute("href")).toBe("/apps");
  expect(within(within(grafana).getByLabelText("Workload")).getByRole("link").getAttribute("href")).toBe("/proxmox");
  // docker isn't installed: Jellyfin's container is plain text.
  expect(within(within(row("jellyfin.home.example")).getByLabelText("Workload")).queryByRole("link")).toBeNull();
});

it("warns about an upstream that matches no Workload and a block without TLS", async () => {
  await show();

  const warnings = screen.getByRole("region", { name: "Warnings" });
  expect(within(warnings).getAllByRole("listitem").map((li) => li.textContent)).toEqual([
    "printer.home.example: upstream http://198.18.2.99 matches no Workload",
    "printer.home.example: no TLS",
  ]);
  expect(within(row("printer.home.example")).getByRole("img", { name: "upstream matches no Workload; no TLS" })).toBeTruthy();
  expect(within(row("grafana.home.example")).queryByRole("img", { name: /no TLS|no Workload/ })).toBeNull();
});

it("shows a Proxy Host apps has no App for, without a Workload warning", async () => {
  await show({
    GetSnapshot: () =>
      Response.json({
        snapshot: {
          revision: "2",
          servers: [{ serverNames: ["~^(?<app>.+)\\.home\\.example$"], listens: [{ address: "443", ssl: true }], locations: [{ path: "/", proxyPass: "http://198.18.2.50:80" }] }],
        },
      }),
  });

  const r = row("~^(?<app>.+)\\.home\\.example$");
  expect(within(r).getByLabelText("App").textContent).toBe("no App");
  expect(screen.queryByRole("region", { name: "Warnings" })).toBeNull();
});

it("still lists Proxy Hosts and TLS warnings while apps and checks are unavailable", async () => {
  const down = (who: string) => () => Response.json({ code: "unavailable", message: `${who} is starting` }, { status: 503 });
  await show({ ListApps: down("apps"), ListChecks: down("checks") });

  const printer = row("printer.home.example");
  expect(within(printer).getByLabelText("Upstream").textContent).toBe("http://198.18.2.99");
  const section = screen.getByRole("region", { name: "Proxy Hosts" });
  expect(section.textContent).toMatch(/apps unavailable: apps is starting/);
  expect(section.textContent).toMatch(/checks unavailable: checks is starting/);
  const warnings = screen.getByRole("region", { name: "Warnings" });
  expect(within(warnings).getAllByRole("listitem").map((li) => li.textContent)).toEqual(["printer.home.example: no TLS"]);
});

it("runs no probes of its own: it only reads nginx-config, apps and checks", async () => {
  const calls = await show();

  expect(new Set(calls)).toEqual(new Set(["GetSnapshot", "ListApps", "ListChecks"]));
});
