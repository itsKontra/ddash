import { cleanup, fireEvent, screen, within } from "@testing-library/react";
import { useLocation } from "react-router";
import { afterEach, expect, it } from "vitest";
import { dashboardIcon } from "@ddash/ui";
import plugin from "./index";
import { settle, unavailable } from "@ddash/plugin/testing";
import { actions, allUp, list, renderScoped, warned } from "./testing";

afterEach(cleanup);

const Page = () => {
  const P = plugin.pages![0].Component!;
  return <P />;
};

const sectionNames = () => screen.getAllByRole("region").map((r) => r.getAttribute("aria-label") ?? r.querySelector("h2")?.textContent);

it("is the apps Nav Entry at order 10 (its icon, layout-grid, is in plugin.yaml)", () => {
  expect(plugin.nav?.order).toBe(10);
});

it("titles the Page Apps under its kicker", async () => {
  renderScoped(<Page />);

  const title = await screen.findByRole("heading", { level: 1, name: "Apps" });
  expect(title.previousElementSibling?.textContent).toBe("Services");
});

it("shows the visible Apps as cards by group, ungrouped ones in the default section after the groups", async () => {
  renderScoped(<Page />);
  await settle();

  expect(sectionNames()).toEqual(["Monitoring", "Network", "Ungrouped", "Hidden", "Orphaned App settings"]);
  const names = (group: string) =>
    within(screen.getByRole("list", { name: group }))
      .getAllByRole("listitem")
      .map((li) => li.getAttribute("aria-label"));
  expect(names("Monitoring")).toEqual(["Grafana"]);
  expect(names("Network")).toEqual(["pfSense"]);
  expect(names("Ungrouped")).toEqual(["Jellyfin", "Printer"]);
  expect(names("Hidden")).toEqual(["Old"]);
});

// The Page's URL search, as the search box last wrote it.
let search = "";

function Where() {
  search = useLocation().search;
  return null;
}

const searchbox = () => screen.getByRole("searchbox", { name: "Search Apps" });
/** Each card section as its name followed by its cards' names. */
const cards = () =>
  screen
    .queryAllByRole("list")
    .map((l) => [l.getAttribute("aria-label"), ...within(l).getAllByRole("listitem").map((li) => li.getAttribute("aria-label"))]);

it("searches the cards in every group, Hidden included, by name, domain or group, kept in the URL", async () => {
  renderScoped(
    <>
      <Page />
      <Where />
    </>,
  );
  await settle();

  const find = (q: string) => {
    fireEvent.change(searchbox(), { target: { value: q } });
    return cards();
  };
  expect(find("graf")).toEqual([["Monitoring", "Grafana"]]);
  expect(find("tv.home")).toEqual([["Ungrouped", "Jellyfin"]]);
  expect(find("198.18.0.1")).toEqual([["Network", "pfSense"]]);
  expect(find("netw")).toEqual([["Network", "pfSense"]]);
  expect(find("old")).toEqual([["Hidden", "Old"]]);
  expect(find("home.example")).toEqual([
    ["Monitoring", "Grafana"],
    ["Ungrouped", "Jellyfin", "Printer"],
    ["Hidden", "Old"],
  ]);
  expect(search).toBe("?q=home.example");
  // A group with no match is gone; orphaned App settings aren't searched.
  expect(sectionNames()).toEqual(["Monitoring", "Ungrouped", "Hidden", "Orphaned App settings"]);
  expect(screen.getByRole("rowheader", { name: "paperless.home.example" })).toBeTruthy();

  fireEvent.change(searchbox(), { target: { value: "" } });
  expect(search).toBe("");
  expect(sectionNames()).toEqual(["Monitoring", "Network", "Ungrouped", "Hidden", "Orphaned App settings"]);
});

it("opens on the search a link carries", async () => {
  renderScoped(<Page />, {}, undefined, "/?q=jelly");
  await settle();

  expect(searchbox()).toHaveProperty("value", "jelly");
  expect(cards()).toEqual([["Ungrouped", "Jellyfin"]]);
});

it("says so when no App matches the search", async () => {
  renderScoped(<Page />, {}, undefined, "/?q=nothing-like-it");
  await settle();

  expect(screen.getByText('No Apps match "nothing-like-it"')).toBeTruthy();
  expect(cards()).toEqual([]);
  expect(sectionNames()).toEqual(["Orphaned App settings"]);
});

it("shows each card's Check status, and warns of a certificate expiring within 14 days", async () => {
  renderScoped(<Page />);
  await settle();

  const card = (name: string) => screen.getByRole("listitem", { name });
  const status = (name: string) => within(card(name)).getByRole("status").textContent;
  expect(status("Grafana")).toBe("up");
  expect(status("Jellyfin")).toBe("down: HTTP 502");
  expect(status("pfSense")).toBe("pending: timeout");
  expect(status("Old")).toBe("Check disabled");
  expect(status("Printer")).toBe("up, certificate expires in 5 days");
});

it("marks a card whose Check is Flapping", async () => {
  const flapping = {
    checks: allUp.checks.map((c) =>
      c.appId === "proxy:grafana.home.example"
        ? { ...c, flapping: true }
        : c.appId === "proxy:jellyfin.home.example"
          ? { ...c, state: "STATE_DOWN", reason: "HTTP 502", flapping: true }
          : c,
    ),
  };
  renderScoped(<Page />, { ListChecks: () => Response.json(flapping) });
  await settle();

  const card = (name: string) => screen.getByRole("listitem", { name });
  const status = (name: string) => within(card(name)).getByRole("status").textContent;
  expect(status("Grafana")).toBe("up, flapping");
  expect(status("Jellyfin")).toBe("down, flapping: HTTP 502");
});

it("shows the cards without a Check status while checks is unavailable", async () => {
  renderScoped(<Page />, { ListChecks: () => unavailable("checks is down") });
  await settle();

  expect(within(screen.getByRole("listitem", { name: "Jellyfin" })).queryByRole("status")).toBeNull();
  // Partial: the cards show, so it's a warning, not a failure.
  expect(screen.getByRole("status").textContent).toBe("Check status unavailable: checks is down");
  expect(screen.queryByRole("alert")).toBeNull();
});

it("shows each card's icon, name, URL and aliases, and opens the URL on a click", async () => {
  renderScoped(<Page />);
  await settle();

  const card = screen.getByRole("listitem", { name: "Jellyfin" });
  const link = within(card).getByRole("link", { name: /Jellyfin/ });
  expect(link.getAttribute("href")).toBe("https://jellyfin.home.example");
  expect(link.getAttribute("target")).toBe("_blank");
  expect(link.textContent).toContain("jellyfin.home.example");
  expect(card.textContent).toContain("media.home.example");
  expect(card.textContent).toContain("tv.home.example");
  expect(card.querySelector("img")?.getAttribute("src")).toBe(dashboardIcon("jellyfin"));

  const pf = screen.getByRole("listitem", { name: "pfSense" });
  expect(pf.querySelector("img")?.getAttribute("src")).toBe(dashboardIcon("pfsense"));
});

it("sets a discovered App's settings", async () => {
  const calls = renderScoped(<Page />);
  await settle();

  fireEvent.click(screen.getByRole("button", { name: "Edit Jellyfin" }));
  const dialog = screen.getByRole("dialog", { name: "Jellyfin" });
  fireEvent.change(within(dialog).getByLabelText("Display name"), { target: { value: "Media" } });
  fireEvent.change(within(dialog).getByLabelText("Group"), { target: { value: "Media" } });
  fireEvent.click(within(dialog).getByRole("checkbox", { name: "Hidden" }));
  fireEvent.click(within(dialog).getByRole("button", { name: /^Icon/ }));
  fireEvent.change(within(dialog).getByRole("combobox", { name: "Search icons" }), { target: { value: "emby" } });
  fireEvent.click(await within(dialog).findByRole("option", { name: "Use emby" }));
  fireEvent.click(within(dialog).getByRole("button", { name: "Save" }));
  await settle();

  expect(actions(calls)).toEqual([
    { method: "SetSettings", body: { id: "proxy:jellyfin.home.example", settings: { name: "Media", icon: "emby", group: "Media", hidden: true } } },
  ]);
  expect(screen.queryByRole("dialog")).toBeNull();
});

it("resets a touched App's settings, and offers no Reset on an untouched one", async () => {
  const calls = renderScoped(<Page />);
  await settle();

  fireEvent.click(screen.getByRole("button", { name: "Edit Jellyfin" }));
  expect(within(screen.getByRole("dialog", { name: "Jellyfin" })).queryByRole("button", { name: "Reset" })).toBeNull();
  fireEvent.click(within(screen.getByRole("dialog", { name: "Jellyfin" })).getByRole("button", { name: "Cancel" }));
  await settle();

  fireEvent.click(screen.getByRole("button", { name: "Edit Grafana" }));
  fireEvent.click(within(screen.getByRole("dialog", { name: "Grafana" })).getByRole("button", { name: "Reset" }));
  await settle();

  expect(actions(calls)).toEqual([{ method: "SetSettings", body: { id: "proxy:grafana.home.example", settings: {} } }]);
});

it("adds a Link-only App", async () => {
  const calls = renderScoped(<Page />);
  await settle();

  fireEvent.click(screen.getByRole("button", { name: "Add App" }));
  const dialog = screen.getByRole("dialog", { name: "Add App" });
  fireEvent.change(within(dialog).getByLabelText("Name"), { target: { value: "Proxmox" } });
  fireEvent.change(within(dialog).getByLabelText("URL"), { target: { value: "https://198.18.0.2:8006" } });
  fireEvent.click(within(dialog).getByRole("button", { name: /^Icon/ }));
  fireEvent.change(within(dialog).getByRole("combobox", { name: "Search icons" }), { target: { value: "proxmox" } });
  fireEvent.click(await within(dialog).findByRole("option", { name: "Use proxmox" }));
  fireEvent.click(within(dialog).getByRole("button", { name: "Add" }));
  await settle();

  expect(actions(calls)).toEqual([{ method: "CreateApp", body: { app: { name: "Proxmox", url: "https://198.18.0.2:8006", icon: "proxmox" } } }]);
  expect(screen.queryByRole("dialog")).toBeNull();
});

it("describes the Icon field by its hint, and Cancel closes the sheet unsaved", async () => {
  const calls = renderScoped(<Page />);
  await settle();

  fireEvent.click(screen.getByRole("button", { name: "Add App" }));
  const dialog = screen.getByRole("dialog", { name: "Add App" });
  expect(within(dialog).getByRole("button", { name: /^Icon/, description: /homarr-labs\/dashboard-icons/ })).toBeTruthy();
  fireEvent.change(within(dialog).getByLabelText("Name"), { target: { value: "Proxmox" } });
  fireEvent.click(within(dialog).getByRole("button", { name: "Cancel" }));
  await settle();

  expect(screen.queryByRole("dialog")).toBeNull();
  expect(actions(calls)).toEqual([]);
});

it("shows why a save was refused and keeps the form open", async () => {
  renderScoped(<Page />, {
    CreateApp: () => Response.json({ code: "invalid_argument", message: "url must be an http or https URL, e.g. https://198.18.0.1" }, { status: 400 }),
  });
  await settle();

  fireEvent.click(screen.getByRole("button", { name: "Add App" }));
  const dialog = screen.getByRole("dialog", { name: "Add App" });
  fireEvent.change(within(dialog).getByLabelText("Name"), { target: { value: "Proxmox" } });
  fireEvent.change(within(dialog).getByLabelText("URL"), { target: { value: "pve" } });
  fireEvent.click(within(dialog).getByRole("button", { name: "Add" }));
  await settle();

  expect(within(screen.getByRole("dialog", { name: "Add App" })).getByRole("alert").textContent).toMatch(/url must be an http or https URL/);
});

it("edits and deletes a Link-only App, its URL fixed", async () => {
  const calls = renderScoped(<Page />);
  await settle();

  fireEvent.click(screen.getByRole("button", { name: "Edit pfSense" }));
  const dialog = screen.getByRole("dialog", { name: "pfSense" });
  const url = within(dialog).getByLabelText("URL") as HTMLInputElement;
  expect(url.value).toBe("https://198.18.0.1");
  expect(url.readOnly).toBe(true);
  fireEvent.change(within(dialog).getByLabelText("Name"), { target: { value: "Firewall" } });
  fireEvent.click(within(dialog).getByRole("button", { name: "Save" }));
  await settle();

  fireEvent.click(screen.getByRole("button", { name: "Edit pfSense" }));
  fireEvent.click(within(screen.getByRole("dialog", { name: "pfSense" })).getByRole("button", { name: "Delete" }));
  fireEvent.click(within(screen.getByRole("alertdialog")).getByRole("button", { name: "Delete" }));
  await settle();

  expect(actions(calls)).toEqual([
    { method: "SetSettings", body: { id: "app:00aa11bb22cc33dd", settings: { name: "Firewall", icon: "pfsense", group: "Network" } } },
    { method: "DeleteApp", body: { id: "app:00aa11bb22cc33dd" } },
  ]);
});

/** Orphaned App settings' row in their table, by the hostname they were kept for. */
const unmatched = (serverName: string) =>
  within(within(screen.getByRole("region", { name: "Orphaned App settings" })).getByRole("table")).getByRole("rowheader", { name: serverName }).closest("tr")!;

/** Opens the strip of the unmatched setting serverName and returns it. */
function openUnmatched(serverName: string) {
  fireEvent.click(within(unmatched(serverName)).getByRole("button", { name: `Actions for ${serverName}` }));
  return screen.getByRole("group", { name: `Actions for ${serverName}` });
}

/** Presses button in the strip of the unmatched setting serverName. */
const chooseUnmatched = (serverName: string, button: string) => fireEvent.click(within(openUnmatched(serverName)).getByRole("button", { name: button }));

it("tables the orphaned App settings, each with what it keeps", async () => {
  renderScoped(<Page />);
  await settle();

  expect(unmatched("paperless.home.example").textContent).toContain("Documents · group Office");
});

it("puts orphaned App settings' Actions in their strip, with no ⋯ Menu", async () => {
  renderScoped(<Page />);
  await settle();

  const table = within(screen.getByRole("region", { name: "Orphaned App settings" })).getByRole("table");
  expect(table.querySelector('[aria-haspopup="menu"]')).toBeNull();
  const strip = openUnmatched("paperless.home.example");
  expect(within(strip).getByRole("status").textContent).toBe("Kept for paperless.home.example");
  expect(within(strip).getAllByRole("button").map((b) => b.textContent)).toEqual(["Re-attach…", "Delete"]);
});

it("re-attaches orphaned App settings to an App without any, from the row's strip", async () => {
  const calls = renderScoped(<Page />);
  await settle();

  chooseUnmatched("paperless.home.example", "Re-attach…");
  const dialog = screen.getByRole("dialog", { name: "Re-attach paperless.home.example" });
  const target = within(dialog).getByRole("combobox", { name: "Re-attach to" });
  // Only Apps without settings of their own.
  expect(within(target).getAllByRole("option").map((o) => o.getAttribute("value"))).toEqual(["", "proxy:jellyfin.home.example", "proxy:printer.home.example"]);
  expect((within(dialog).getByRole("button", { name: "Re-attach" }) as HTMLButtonElement).disabled).toBe(true);
  fireEvent.change(target, { target: { value: "proxy:jellyfin.home.example" } });
  fireEvent.click(within(dialog).getByRole("button", { name: "Re-attach" }));
  await settle();

  expect(actions(calls)).toEqual([{ method: "Reattach", body: { id: "proxy:paperless.home.example", to: "proxy:jellyfin.home.example" } }]);
  expect(screen.queryByRole("dialog", { name: "Re-attach paperless.home.example" })).toBeNull();
});

it("deletes orphaned App settings from the row's strip after a confirm", async () => {
  const calls = renderScoped(<Page />);
  await settle();

  chooseUnmatched("paperless.home.example", "Delete");
  const confirm = screen.getByRole("alertdialog");
  expect(confirm.textContent).toContain("Delete the settings kept for paperless.home.example?");
  fireEvent.click(within(confirm).getByRole("button", { name: "Delete" }));
  await settle();

  expect(actions(calls)).toEqual([{ method: "DeleteOrphan", body: { id: "proxy:paperless.home.example" } }]);
});

it("shows a skeleton while apps answers, and no loading text", async () => {
  renderScoped(<Page />);

  expect(screen.getByRole("status", { name: "Loading" })).toBeTruthy();
  expect(screen.queryByText(/loading/i)).toBeNull();
  await settle();
  expect(screen.queryByRole("status", { name: "Loading" })).toBeNull();
});

it("shows apps being unavailable as a failure", async () => {
  renderScoped(<Page />, { ListApps: () => unavailable("apps is down") });
  await settle();

  expect(screen.getByRole("alert").textContent).toMatch(/apps unavailable: apps is down/);
  expect(screen.queryByRole("status", { name: "Loading" })).toBeNull();
});

it("asks to set up Nginx Proxy Manager until it is configured", async () => {
  renderScoped(<Page />, { ListApps: () => Response.json({ apps: [], proxyHosts: { error: "not configured", notConfigured: true } }) });
  await settle();

  const notice = screen.getByRole("status");
  expect(notice.textContent).toMatch(/^Set up Nginx Proxy Manager/);
  expect(within(notice).getByRole("link", { name: "Configure it in Plugins" }).getAttribute("href")).toBe("/plugins");
  expect(screen.queryByText(/Couldn't read the Proxy Hosts/)).toBeNull();
});

it("says when Nginx Proxy Manager couldn't be read", async () => {
  renderScoped(<Page />, { ListApps: () => Response.json({ apps: [], proxyHosts: { error: "nginx-proxy-manager-api unavailable: sign-in refused" } }) });
  await settle();

  expect(screen.getByRole("status").textContent).toMatch(/Nginx Proxy Manager: nginx-proxy-manager-api unavailable: sign-in refused/);
  expect(screen.getByText("No Apps yet")).toBeTruthy();
  // The empty state offers adding one, as the Page title does.
  expect(screen.getAllByRole("button", { name: "Add App" })).toHaveLength(2);
});

it("shows each card's Workload, linked to its Proxmox detail or the Docker Page when that Plugin is installed", async () => {
  renderScoped(<Page />, {}, ["apps", "proxmox"]);
  await settle();

  const workload = (name: string) => within(screen.getByRole("listitem", { name })).queryByLabelText("Workload");
  expect(workload("Grafana")?.textContent).toBe("LXC grafana · running");
  expect(within(workload("Grafana")!).getByRole("link").getAttribute("href")).toBe("/proxmox/workloads/120");
  // docker isn't installed: plain text.
  expect(workload("Jellyfin")?.textContent).toBe("Docker jellyfin · exited");
  expect(within(workload("Jellyfin")!).queryByRole("link")).toBeNull();
  expect(workload("Printer")).toBeNull();
});

it("links a container to the Docker Page", async () => {
  renderScoped(<Page />, {}, ["apps", "docker"]);
  await settle();

  const workload = within(screen.getByRole("listitem", { name: "Jellyfin" })).getByLabelText("Workload");
  expect(within(workload).getByRole("link", { name: "Docker jellyfin · exited" }).getAttribute("href")).toBe("/docker");
  expect(within(within(screen.getByRole("listitem", { name: "Grafana" })).getByLabelText("Workload")).queryByRole("link")).toBeNull();
});

it("overrides a discovered App's Workload with one the sources list", async () => {
  const calls = renderScoped(<Page />);
  await settle();

  fireEvent.click(screen.getByRole("button", { name: "Edit Jellyfin" }));
  await settle();
  const dialog = screen.getByRole("dialog", { name: "Jellyfin" });
  const select = within(dialog).getByRole("combobox", { name: "Workload" });
  expect(within(select).getAllByRole("option").map((o) => o.textContent)).toEqual([
    "Matched: Docker jellyfin",
    "Docker jellyfin (media)",
    "LXC media-stack (pve/102)",
    "LXC grafana (pve/120)",
    "VM home-assistant (pve/110)",
    "External host…",
  ]);
  const mediaStack = within(select).getByRole("option", { name: "LXC media-stack (pve/102)" }).getAttribute("value");
  fireEvent.change(select, { target: { value: mediaStack } });
  fireEvent.click(within(dialog).getByRole("button", { name: "Save" }));
  await settle();

  expect(actions(calls)).toEqual([
    { method: "SetSettings", body: { id: "proxy:jellyfin.home.example", settings: { workload: { kind: "KIND_LXC", host: "pve", id: "102" } } } },
  ]);
});

it("sets an external host as a discovered App's Workload", async () => {
  const calls = renderScoped(<Page />);
  await settle();

  fireEvent.click(screen.getByRole("button", { name: "Edit Printer" }));
  await settle();
  const dialog = screen.getByRole("dialog", { name: "Printer" });
  expect(within(dialog).getByRole("option", { name: "Matched: none" })).toBeTruthy();
  fireEvent.change(within(dialog).getByRole("combobox", { name: "Workload" }), { target: { value: "external" } });
  fireEvent.change(within(dialog).getByLabelText("External host"), { target: { value: "198.18.1.40" } });
  fireEvent.click(within(dialog).getByRole("button", { name: "Save" }));
  await settle();

  expect(actions(calls)).toEqual([
    { method: "SetSettings", body: { id: "proxy:printer.home.example", settings: { workload: { kind: "KIND_EXTERNAL", host: "198.18.1.40" } } } },
  ]);
});

it("keeps an override no source lists on offer", async () => {
  const lost = { ref: { kind: "KIND_LXC", host: "pve", id: "130" } };
  const overridden = {
    ...list,
    apps: list.apps.map((a) =>
      a.name === "Printer" ? { ...a, settings: { workload: lost.ref }, workload: { ...lost, override: true } } : a,
    ),
  };
  const calls = renderScoped(<Page />, { ListApps: () => Response.json(overridden) });
  await settle();

  expect(within(screen.getByRole("listitem", { name: "Printer" })).getByLabelText("Workload").textContent).toBe("LXC 130");
  fireEvent.click(screen.getByRole("button", { name: "Edit Printer" }));
  await settle();
  const dialog = screen.getByRole("dialog", { name: "Printer" });
  const select = within(dialog).getByRole("combobox", { name: "Workload" }) as HTMLSelectElement;
  expect(select.selectedOptions[0].textContent).toBe("LXC 130 (pve/130, not listed now)");
  fireEvent.click(within(dialog).getByRole("button", { name: "Save" }));
  await settle();

  expect(actions(calls)).toEqual([
    { method: "SetSettings", body: { id: "proxy:printer.home.example", settings: { workload: { kind: "KIND_LXC", host: "pve", id: "130" } } } },
  ]);
});

it("shows each card's Warnings that count, and a disabled Proxy Host", async () => {
  const disabled = {
    ...warned,
    apps: warned.apps.map((a) => (a.name === "Grafana" ? { ...a, proxyHost: { id: "3", enabled: false } } : a)),
  };
  renderScoped(<Page />, { ListApps: () => Response.json(disabled) });
  await settle();

  const warnings = (name: string) => within(screen.getByRole("listitem", { name })).queryByLabelText("Warnings")?.textContent;
  expect(warnings("Jellyfin")).toBe("No Workload · Proxy Host offline");
  // Printer's No TLS is Ignored; Old is Hidden.
  expect(warnings("Printer")).toBe("No Workload");
  expect(warnings("Old")).toBeUndefined();
  expect(warnings("Grafana")).toBeUndefined();
  expect(screen.getByRole("listitem", { name: "Grafana" }).textContent).toContain("Proxy Host disabled");
});
