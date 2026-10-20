import { cleanup, fireEvent, screen, within } from "@testing-library/react";
import { afterEach, expect, it } from "vitest";
import plugin from "./index";
import { actions, list, renderScoped, settle } from "./testing";

afterEach(cleanup);

const Page = () => {
  const P = plugin.pages![0].Component!;
  return <P />;
};

const sectionNames = () => screen.getAllByRole("region").map((r) => r.getAttribute("aria-label") ?? r.querySelector("h2")?.textContent);

it("is the apps Nav Entry at order 10 (its icon, layout-grid, is in plugin.yaml)", () => {
  expect(plugin.nav?.order).toBe(10);
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
  expect(within(card("Jellyfin")).getByRole("img", { name: "down: HTTP 502" })).toBeTruthy();
  expect(within(card("Printer")).getByRole("img", { name: "up, certificate expires in 5 days" })).toBeTruthy();
});

it("shows the cards without a Check status while checks is unavailable", async () => {
  renderScoped(<Page />, { ListChecks: () => Response.json({ code: "unavailable", message: "checks is down" }, { status: 503 }) });
  await settle();

  expect(within(screen.getByRole("listitem", { name: "Jellyfin" })).queryByRole("status")).toBeNull();
  expect(screen.getByText("Check status unavailable: checks is down")).toBeTruthy();
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

it("re-attaches unmatched settings to an App without any", async () => {
  const calls = renderScoped(<Page />);
  await settle();

  const row = screen.getByRole("listitem", { name: "paperless.home.example" });
  expect(row.textContent).toContain("Documents");
  const target = within(row).getByRole("combobox", { name: "Re-attach to" });
  // Only Apps without settings of their own.
  expect(within(target).getAllByRole("option").map((o) => o.getAttribute("value"))).toEqual(["", "jellyfin.home.example", "printer.home.example"]);
  fireEvent.change(target, { target: { value: "jellyfin.home.example" } });
  fireEvent.click(within(row).getByRole("button", { name: "Re-attach" }));
  await settle();

  expect(actions(calls)).toEqual([{ method: "AttachOverlay", body: { serverName: "paperless.home.example", to: "jellyfin.home.example" } }]);
});

it("deletes unmatched settings after a confirm", async () => {
  const calls = renderScoped(<Page />);
  await settle();

  fireEvent.click(within(screen.getByRole("listitem", { name: "paperless.home.example" })).getByRole("button", { name: "Delete" }));
  fireEvent.click(within(screen.getByRole("alertdialog")).getByRole("button", { name: "Delete" }));
  await settle();

  expect(actions(calls)).toEqual([{ method: "DeleteOverlay", body: { serverName: "paperless.home.example" } }]);
});

it("says when nginx-config couldn't be read", async () => {
  renderScoped(<Page />, { ListApps: () => Response.json({ apps: [], discoveryError: "nginx-config is not configured" }) });
  await settle();

  expect(screen.getByRole("status").textContent).toMatch(/nginx-config is not configured/);
  expect(screen.getByText(/No Apps yet/)).toBeTruthy();
});

it("shows each card's Workload, linked to the Proxmox or Docker Page when that Plugin is installed", async () => {
  renderScoped(<Page />, {}, ["apps", "proxmox"]);
  await settle();

  const workload = (name: string) => within(screen.getByRole("listitem", { name })).queryByLabelText("Workload");
  expect(workload("Grafana")?.textContent).toBe("LXC grafana · running");
  expect(within(workload("Grafana")!).getByRole("link").getAttribute("href")).toBe("/proxmox");
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
