import { cleanup, screen, within } from "@testing-library/react";
import { afterEach, expect, it } from "vitest";
import { Page, renderScoped, settle, type Answers } from "./testing";

afterEach(cleanup);

const day = 86_400_000;
const in5Days = new Date(Date.now() + 5 * day + 3_600_000).toISOString();
const in60Days = "2027-01-10T12:00:00Z";

// ListProxyHosts' answer (Connect JSON): three Proxy Hosts, by domain, as
// apps joins them with their Apps: Grafana on an LXC behind an upstream
// block, Jellyfin on a container, the Printer on nothing and without TLS.
const proxyHosts = {
  proxyHosts: [
    {
      file: "/etc/nginx/conf.d/grafana.conf",
      line: "5",
      serverNames: ["grafana.home.example"],
      upstream: "http://grafana",
      upstreamServers: ["198.18.2.30:3000", "198.18.2.31:3000"],
      app: {
        id: "proxy:grafana.home.example",
        serverName: "grafana.home.example",
        name: "Grafana",
        url: "https://grafana.home.example",
        workload: { ref: { kind: "KIND_LXC", host: "pve", id: "120" }, name: "grafana", state: "running" },
      },
    },
    {
      file: "/etc/nginx/conf.d/jellyfin.conf",
      line: "6",
      serverNames: ["jellyfin.home.example", "media.home.example"],
      upstream: "http://198.18.2.20:8096",
      app: {
        id: "proxy:jellyfin.home.example",
        serverName: "jellyfin.home.example",
        name: "Jellyfin",
        url: "https://jellyfin.home.example",
        aliases: ["media.home.example"],
        workload: { ref: { kind: "KIND_DOCKER", host: "media", id: "jellyfin" }, name: "jellyfin", state: "running" },
      },
    },
    {
      file: "/etc/nginx/conf.d/printer.conf",
      line: "1",
      serverNames: ["printer.home.example"],
      upstream: "http://198.18.2.99",
      app: { id: "proxy:printer.home.example", serverName: "printer.home.example", name: "Printer", url: "http://printer.home.example:8080" },
      warnings: ["PROXY_HOST_WARNING_NO_WORKLOAD", "PROXY_HOST_WARNING_NO_TLS"],
    },
  ],
};

// GetSnapshot's answer: the config loads.
const snapshot = { snapshot: { revision: "7", readAt: "2026-10-20T07:15:00Z" } };

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
  ListProxyHosts: () => Response.json(proxyHosts),
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

it("lists the Proxy Hosts apps finds, by domain", async () => {
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

it("links the App to the apps Page and the Workload to its Proxmox detail or Docker Page when they are installed", async () => {
  await show({}, ["reverse-proxy", "apps", "proxmox"]);

  const grafana = row("grafana.home.example");
  expect(within(within(grafana).getByLabelText("App")).getByRole("link").getAttribute("href")).toBe("/apps");
  expect(within(within(grafana).getByLabelText("Workload")).getByRole("link").getAttribute("href")).toBe("/proxmox/workloads/120");
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

it("shows a Proxy Host that yields no App", async () => {
  await show({
    ListProxyHosts: () =>
      Response.json({
        proxyHosts: [{ serverNames: ["~^(?<app>.+)\\.home\\.example$"], upstream: "http://198.18.2.50:80" }],
      }),
  });

  const r = row("~^(?<app>.+)\\.home\\.example$");
  expect(within(r).getByLabelText("App").textContent).toBe("no App");
  expect(screen.queryByRole("region", { name: "Warnings" })).toBeNull();
});

it("says why while apps or checks is unavailable, keeping what the other answers", async () => {
  const down = (who: string) => () => Response.json({ code: "unavailable", message: `${who} is starting` }, { status: 503 });
  await show({ ListChecks: down("checks") });

  const printer = row("printer.home.example");
  expect(within(printer).getByLabelText("Upstream").textContent).toBe("http://198.18.2.99");
  expect(screen.getByRole("region", { name: "Proxy Hosts" }).textContent).toMatch(/checks unavailable: checks is starting/);
  cleanup();

  await show({ ListProxyHosts: down("apps") });
  expect(screen.getByRole("region", { name: "Proxy Hosts" }).textContent).toMatch(/apps unavailable: apps is starting/);
  expect(screen.queryByRole("table", { name: "Proxy Hosts" })).toBeNull();
});

it("runs no probes of its own: it only reads nginx-config, apps and checks", async () => {
  const calls = await show();

  expect(new Set(calls)).toEqual(new Set(["GetSnapshot", "ListProxyHosts", "ListChecks"]));
});
