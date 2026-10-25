import { cleanup, fireEvent, screen, within } from "@testing-library/react";
import { afterEach, expect, it } from "vitest";
import plugin from "./index";
import { settle, unavailable } from "@ddash/plugin/testing";
import { actions, allUp, list, renderScoped } from "./testing";

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

  expect(sectionNames()).toEqual(["Monitoring", "Network", "Ungrouped", "Hidden", "Unmatched settings"]);
  const names = (group: string) =>
    within(screen.getByRole("list", { name: group }))
      .getAllByRole("listitem")
      .map((li) => li.getAttribute("aria-label"));
  expect(names("Monitoring")).toEqual(["Grafana"]);
  expect(names("Network")).toEqual(["pfSense"]);
  expect(names("Ungrouped")).toEqual(["Jellyfin", "Printer"]);
  expect(names("Hidden")).toEqual(["Old"]);
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
  expect(card.querySelector("img")?.getAttribute("src")).toBe("https://cdn.jsdelivr.net/gh/homarr-labs/dashboard-icons/svg/jellyfin.svg");

  const pf = screen.getByRole("listitem", { name: "pfSense" });
  expect(pf.querySelector("img")?.getAttribute("src")).toBe("https://cdn.jsdelivr.net/gh/homarr-labs/dashboard-icons/svg/pfsense.svg");
});

it("sets a discovered App's display settings", async () => {
  const calls = renderScoped(<Page />);
  await settle();

  fireEvent.click(screen.getByRole("button", { name: "Edit Jellyfin" }));
  const dialog = screen.getByRole("dialog", { name: "Jellyfin" });
  fireEvent.change(within(dialog).getByLabelText("Display name"), { target: { value: "Media" } });
  fireEvent.change(within(dialog).getByLabelText("Group"), { target: { value: "Media" } });
  fireEvent.click(within(dialog).getByRole("checkbox", { name: "Hidden" }));
  fireEvent.click(within(dialog).getByRole("button", { name: "Save" }));
  await settle();

  expect(actions(calls)).toEqual([
    { method: "SetOverlay", body: { overlay: { serverName: "jellyfin.home.example", name: "Media", group: "Media", hidden: true } } },
  ]);
  expect(screen.queryByRole("dialog")).toBeNull();
});

it("resets a discovered App's settings after a confirm", async () => {
  const calls = renderScoped(<Page />);
  await settle();

  fireEvent.click(screen.getByRole("button", { name: "Edit Grafana" }));
  fireEvent.click(within(screen.getByRole("dialog", { name: "Grafana" })).getByRole("button", { name: "Reset" }));
  fireEvent.click(within(screen.getByRole("alertdialog")).getByRole("button", { name: "Reset" }));
  await settle();

  expect(actions(calls)).toEqual([{ method: "DeleteOverlay", body: { serverName: "grafana.home.example" } }]);
});

it("adds a manual App", async () => {
  const calls = renderScoped(<Page />);
  await settle();

  fireEvent.click(screen.getByRole("button", { name: "Add App" }));
  const dialog = screen.getByRole("dialog", { name: "Add App" });
  fireEvent.change(within(dialog).getByLabelText("Name"), { target: { value: "Proxmox" } });
  fireEvent.change(within(dialog).getByLabelText("URL"), { target: { value: "https://198.18.0.2:8006" } });
  fireEvent.change(within(dialog).getByLabelText("Icon"), { target: { value: "proxmox" } });
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
  expect(within(dialog).getByRole("textbox", { name: "Icon", description: /homarr-labs\/dashboard-icons/ })).toBeTruthy();
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

it("edits and deletes a manual App", async () => {
  const calls = renderScoped(<Page />);
  await settle();

  fireEvent.click(screen.getByRole("button", { name: "Edit pfSense" }));
  const dialog = screen.getByRole("dialog", { name: "pfSense" });
  expect((within(dialog).getByLabelText("URL") as HTMLInputElement).value).toBe("https://198.18.0.1");
  fireEvent.change(within(dialog).getByLabelText("Name"), { target: { value: "Firewall" } });
  fireEvent.click(within(dialog).getByRole("button", { name: "Save" }));
  await settle();

  fireEvent.click(screen.getByRole("button", { name: "Edit pfSense" }));
  fireEvent.click(within(screen.getByRole("dialog", { name: "pfSense" })).getByRole("button", { name: "Delete" }));
  fireEvent.click(within(screen.getByRole("alertdialog")).getByRole("button", { name: "Delete" }));
  await settle();

  expect(actions(calls)).toEqual([
    { method: "UpdateApp", body: { id: "manual:00aa11bb22cc33dd", app: { name: "Firewall", url: "https://198.18.0.1", icon: "pfsense", group: "Network" } } },
    { method: "DeleteApp", body: { id: "manual:00aa11bb22cc33dd" } },
  ]);
});

/** An unmatched setting's row in the Unmatched settings table, by its server name. */
const unmatched = (serverName: string) =>
  within(within(screen.getByRole("region", { name: "Unmatched settings" })).getByRole("table")).getByRole("rowheader", { name: serverName }).closest("tr")!;

/** Picks item from the ⋯ Menu of the unmatched setting serverName. */
function chooseUnmatched(serverName: string, item: string) {
  fireEvent.click(within(unmatched(serverName)).getByRole("button", { name: `Actions for ${serverName}` }));
  fireEvent.click(screen.getByRole("menuitem", { name: item }));
}

it("tables the unmatched settings, each with what it sets", async () => {
  renderScoped(<Page />);
  await settle();

  expect(unmatched("paperless.home.example").textContent).toContain("Documents · group Office");
});

it("re-attaches unmatched settings to an App without any, from the row's menu", async () => {
  const calls = renderScoped(<Page />);
  await settle();

  chooseUnmatched("paperless.home.example", "Re-attach…");
  const dialog = screen.getByRole("dialog", { name: "Re-attach paperless.home.example" });
  const target = within(dialog).getByRole("combobox", { name: "Re-attach to" });
  // Only Apps without settings of their own.
  expect(within(target).getAllByRole("option").map((o) => o.getAttribute("value"))).toEqual(["", "jellyfin.home.example", "printer.home.example"]);
  expect((within(dialog).getByRole("button", { name: "Re-attach" }) as HTMLButtonElement).disabled).toBe(true);
  fireEvent.change(target, { target: { value: "jellyfin.home.example" } });
  fireEvent.click(within(dialog).getByRole("button", { name: "Re-attach" }));
  await settle();

  expect(actions(calls)).toEqual([{ method: "AttachOverlay", body: { serverName: "paperless.home.example", to: "jellyfin.home.example" } }]);
  expect(screen.queryByRole("dialog", { name: "Re-attach paperless.home.example" })).toBeNull();
});

it("deletes unmatched settings from the row's menu after a confirm", async () => {
  const calls = renderScoped(<Page />);
  await settle();

  chooseUnmatched("paperless.home.example", "Delete");
  fireEvent.click(within(screen.getByRole("alertdialog")).getByRole("button", { name: "Delete" }));
  await settle();

  expect(actions(calls)).toEqual([{ method: "DeleteOverlay", body: { serverName: "paperless.home.example" } }]);
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

it("says when nginx-config couldn't be read", async () => {
  renderScoped(<Page />, { ListApps: () => Response.json({ apps: [], discoveryError: "nginx-config is not configured" }) });
  await settle();

  expect(screen.getByRole("status").textContent).toMatch(/nginx-config is not configured/);
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
    {
      method: "SetOverlay",
      body: { overlay: { serverName: "jellyfin.home.example", workload: { kind: "KIND_LXC", host: "pve", id: "102" } } },
    },
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
    {
      method: "SetOverlay",
      body: { overlay: { serverName: "printer.home.example", workload: { kind: "KIND_EXTERNAL", host: "198.18.1.40" } } },
    },
  ]);
});

it("keeps an override no source lists on offer", async () => {
  const lost = { ref: { kind: "KIND_LXC", host: "pve", id: "130" } };
  const overridden = {
    ...list,
    apps: list.apps.map((a) =>
      a.name === "Printer" ? { ...a, overlay: { serverName: "printer.home.example", workload: lost.ref }, workload: { ...lost, override: true } } : a,
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
    { method: "SetOverlay", body: { overlay: { serverName: "printer.home.example", workload: { kind: "KIND_LXC", host: "pve", id: "130" } } } },
  ]);
});
