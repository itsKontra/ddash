import { cleanup, fireEvent, screen, within } from "@testing-library/react";
import { useLocation } from "react-router";
import { afterEach, expect, it } from "vitest";
import { pending, settle, unavailable, type Answers } from "@ddash/plugin/testing";
import { Page, renderScoped } from "./testing";

afterEach(cleanup);

const day = 86_400_000;
const in5Days = new Date(Date.now() + 5 * day + 3_600_000).toISOString();
const in60Days = "2027-01-10T12:00:00Z";

// ListProxyHosts' answer (Connect JSON): three Proxy Hosts, by domain, as
// apps joins them with their Apps: Grafana on an LXC behind an upstream
// block, Jellyfin on a container, the Office printer on nothing and
// without TLS.
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
      app: { id: "proxy:printer.home.example", serverName: "printer.home.example", name: "Office printer", url: "http://printer.home.example:8080" },
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

// The Page's URL search, as the table last wrote it.
let search = "";

function Where() {
  search = useLocation().search;
  return null;
}

async function show(over: Answers = {}, installed?: string[], path?: string) {
  const calls = renderScoped(
    <>
      <Page />
      <Where />
    </>,
    { ...answers, ...over },
    installed,
    path,
  );
  await settle();
  return calls;
}

const panel = () => screen.getByRole("region", { name: "Proxy Hosts" });
const table = () => within(panel()).getByRole("table");
const rows = () => within(table()).getAllByRole("row").slice(1);
/** A row's first domain, the first line of its row header. */
const domainOf = (r: HTMLElement) => within(r).getByRole("rowheader").firstElementChild?.textContent;
const row = (domain: string) => rows().find((r) => domainOf(r) === domain)!;
/** A row's cell under the column headed header. */
const cell = (r: HTMLElement, header: string) =>
  r.children[within(table()).getAllByRole("columnheader").findIndex((h) => h.textContent === header)] as HTMLElement;
const datetime = (el: HTMLElement) => within(el).getByText((_, e) => e?.tagName === "TIME").getAttribute("datetime");

/** The rows' first domains, in order. */
const domains = () => rows().map(domainOf);
const sortBy = (header: string) => fireEvent.click(within(table()).getByRole("button", { name: header }));
const searchbox = () => within(panel()).getByRole("searchbox", { name: "Search Proxy Hosts" });

it("lists the Proxy Hosts apps finds by their first domain until another sort is chosen", async () => {
  await show({ ListProxyHosts: () => Response.json({ proxyHosts: [...proxyHosts.proxyHosts].reverse() }) });

  expect(domains()).toEqual(["grafana.home.example", "jellyfin.home.example", "printer.home.example"]);
  expect(within(table()).getByRole("columnheader", { name: "Domains" }).getAttribute("aria-sort")).toBe("ascending");
  expect(search).toBe("");
});

it("sorts by Check, Down first, kept in the URL", async () => {
  await show();

  sortBy("Check");

  // The Printer is down, Jellyfin's certificate warns, Grafana is up.
  expect(domains()).toEqual(["printer.home.example", "jellyfin.home.example", "grafana.home.example"]);
  expect(search).toBe("?sort=check");
});

it("sorts by Certificate, soonest expiry first and none last", async () => {
  await show();

  sortBy("Certificate");

  expect(domains()).toEqual(["jellyfin.home.example", "grafana.home.example", "printer.home.example"]);
  expect(search).toBe("?sort=certificate");
});

it("sorts by Upstream, App and Workload, a host without one last", async () => {
  await show({
    ListProxyHosts: () =>
      Response.json({
        proxyHosts: [
          ...proxyHosts.proxyHosts,
          { file: "/etc/nginx/conf.d/wild.conf", line: "1", serverNames: ["wild.home.example"], upstream: "http://198.18.2.1" },
        ],
      }),
  });

  sortBy("Upstream");
  // By the address's value: 198.18.2.1, .20, .99, then a name.
  expect(domains()).toEqual(["wild.home.example", "jellyfin.home.example", "printer.home.example", "grafana.home.example"]);
  sortBy("App");
  expect(domains()).toEqual(["grafana.home.example", "jellyfin.home.example", "printer.home.example", "wild.home.example"]);
  sortBy("Workload");
  expect(domains()).toEqual(["jellyfin.home.example", "grafana.home.example", "printer.home.example", "wild.home.example"]);
});

it("searches the Proxy Hosts by any domain, the upstream, a server, the App or the Workload, kept in the URL", async () => {
  await show();

  const find = (q: string) => {
    fireEvent.change(searchbox(), { target: { value: q } });
    return domains();
  };
  expect(find("media")).toEqual(["jellyfin.home.example"]);
  expect(find("http://198.18.2.99")).toEqual(["printer.home.example"]);
  expect(find("198.18.2.31")).toEqual(["grafana.home.example"]);
  expect(find("office")).toEqual(["printer.home.example"]);
  expect(find("docker")).toEqual(["jellyfin.home.example"]);
  expect(search).toBe("?q=docker");
  // Warnings has its own Panel, which the search leaves alone.
  expect(screen.getByRole("region", { name: "Warnings" })).toBeTruthy();
});

it("opens on the search and sort a link carries", async () => {
  await show({}, undefined, "/?q=home&sort=-domains");

  expect(searchbox()).toHaveProperty("value", "home");
  expect(domains()).toEqual(["printer.home.example", "jellyfin.home.example", "grafana.home.example"]);
});

it("says so when no Proxy Host matches the search", async () => {
  await show({}, undefined, "/?q=nothing-like-it");

  expect(table().textContent).toMatch(/No Proxy Hosts match "nothing-like-it"/);
});

it("shows a Proxy Host's domains, upstream, App with its Check status, Workload and certificate expiry", async () => {
  await show();

  const jellyfin = row("jellyfin.home.example");
  expect(jellyfin.textContent).toMatch(/media\.home\.example/);
  expect(cell(jellyfin, "Upstream").textContent).toBe("http://198.18.2.20:8096");
  expect(cell(jellyfin, "App").textContent).toBe("Jellyfin");
  expect(within(jellyfin).getByRole("img", { name: "Check: up, certificate expires in 5 days" })).toBeTruthy();
  expect(cell(jellyfin, "Workload").textContent).toBe("Docker jellyfin · running");
  const cert = cell(jellyfin, "Certificate");
  expect(cert.textContent).toMatch(/expires in 5 days/);
  expect(datetime(cert)).toBe(new Date(in5Days).toISOString());

  const grafana = row("grafana.home.example");
  expect(within(grafana).getByRole("img", { name: "Check: up" })).toBeTruthy();
  expect(datetime(cell(grafana, "Certificate"))).toBe("2027-01-10T12:00:00.000Z");

  const printer = row("printer.home.example");
  expect(within(printer).getByRole("img", { name: "Check: down: connection refused" })).toBeTruthy();
  expect(cell(printer, "Workload").textContent).toBe("none");
  expect(cell(printer, "Certificate").textContent).toBe("—");
});

it("shows an upstream block's servers next to its name", async () => {
  await show();

  expect(cell(row("grafana.home.example"), "Upstream").textContent).toBe("http://grafana → 198.18.2.30:3000, 198.18.2.31:3000");
});

it("shows an upstream without its nginx variables, keeping its literal path", async () => {
  await show({
    ListProxyHosts: () =>
      Response.json({
        proxyHosts: [
          { serverNames: ["jellyfin.home.example"], upstream: "http://jellyfin:8096$request_uri" },
          { serverNames: ["app.home.example"], upstream: "http://app$uri$is_args$args" },
          { serverNames: ["api.home.example"], upstream: "http://198.18.2.40:8080/api${uri}", warnings: ["PROXY_HOST_WARNING_NO_WORKLOAD"] },
        ],
      }),
  });

  expect(cell(row("jellyfin.home.example"), "Upstream").textContent).toBe("http://jellyfin:8096");
  expect(cell(row("app.home.example"), "Upstream").textContent).toBe("http://app");
  expect(cell(row("api.home.example"), "Upstream").textContent).toBe("http://198.18.2.40:8080/api");
  // The Warnings list names the upstream as nginx's config writes it.
  const warnings = screen.getByRole("region", { name: "Warnings" });
  expect(within(warnings).getByRole("listitem").textContent).toBe("api.home.exampleupstream http://198.18.2.40:8080/api${uri} matches no Workload");
});

it("links the App to the apps Page and the Workload to its Proxmox detail or Docker Page when they are installed", async () => {
  await show({}, ["reverse-proxy", "apps", "proxmox"]);

  const grafana = row("grafana.home.example");
  expect(within(cell(grafana, "App")).getByRole("link").getAttribute("href")).toBe("/apps");
  expect(within(cell(grafana, "Workload")).getByRole("link").getAttribute("href")).toBe("/proxmox/workloads/120");
  // docker isn't installed: Jellyfin's container is plain text.
  expect(within(cell(row("jellyfin.home.example"), "Workload")).queryByRole("link")).toBeNull();
});

it("warns about an upstream that matches no Workload and a block without TLS", async () => {
  await show();

  const warnings = screen.getByRole("region", { name: "Warnings" });
  const items = within(warnings).getAllByRole("listitem");
  expect(items.map((li) => within(li).getByRole("img").getAttribute("aria-label"))).toEqual(["warn", "warn"]);
  expect(items.map((li) => li.textContent)).toEqual([
    "printer.home.exampleupstream http://198.18.2.99 matches no Workload",
    "printer.home.exampleno TLS",
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
  expect(cell(r, "App").textContent).toBe("no App");
  expect(screen.queryByRole("region", { name: "Warnings" })).toBeNull();
});

it("says why while apps or checks is unavailable, keeping what the other answers", async () => {
  const down = (who: string) => () => unavailable(`${who} is starting`);
  await show({ ListChecks: down("checks") });

  const printer = row("printer.home.example");
  expect(cell(printer, "Upstream").textContent).toBe("http://198.18.2.99");
  // Partial: a warning, with the table still there.
  expect(within(panel()).getByRole("status").textContent).toBe("checks unavailable: checks is starting");
  cleanup();

  await show({ ListProxyHosts: down("apps") });
  expect(within(panel()).getByRole("alert").textContent).toBe("apps unavailable: apps is starting");
  expect(within(panel()).queryByRole("table")).toBeNull();
});

it("shows skeleton rows until apps answers", async () => {
  await show({ ListProxyHosts: pending });

  expect(within(table()).getByRole("status", { name: "Loading" })).toBeTruthy();
});

it("says so when no server block forwards to an upstream", async () => {
  await show({ ListProxyHosts: () => Response.json({ proxyHosts: [] }) });

  expect(table().textContent).toMatch(/No server block forwards to an upstream/);
});

it("runs no probes of its own: it only reads nginx-config, apps and checks", async () => {
  const calls = await show();

  expect(new Set(calls.map((c) => c.method))).toEqual(new Set(["GetSnapshot", "ListProxyHosts", "ListChecks"]));
});
