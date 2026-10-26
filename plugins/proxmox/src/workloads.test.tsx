import { act, cleanup, fireEvent, screen, within } from "@testing-library/react";
import { afterEach, expect, it } from "vitest";
import plugin from "./index";
import { pending, unavailable } from "@ddash/plugin/testing";
import { back, search, show } from "./testing";

afterEach(cleanup);

it("is the proxmox Nav Entry at order 40 (its icon, server, is in plugin.yaml)", () => {
  expect(plugin.nav?.order).toBe(40);
});

it("lists every LXC and VM with its status, resources, tags and uptime", async () => {
  const calls = await show("/proxmox");

  expect(screen.getByRole("heading", { level: 1, name: "Proxmox" }).previousElementSibling?.textContent).toBe("Infrastructure");

  expect(calls.find((c) => c.method === "ListWorkloads")?.url).toBe("/api/c/proxmox-api/ddash.proxmoxapi.ProxmoxApiService/ListWorkloads");
  const nginx = screen.getByRole("row", { name: /nginx/ });
  expect(within(nginx).getByText("running")).toBeTruthy();
  expect(within(nginx).getByText("101")).toBeTruthy();
  expect(within(nginx).getByText("LXC")).toBeTruthy();
  expect(within(nginx).getByText("infra")).toBeTruthy();
  expect(within(nginx).getByText("web")).toBeTruthy();
  expect(within(nginx).getByText("1.3 % of 2 CPUs")).toBeTruthy();
  expect(within(nginx).getByText("256 MiB / 1.0 GiB")).toBeTruthy();
  expect(within(nginx).getByText("2.0 GiB / 8.0 GiB")).toBeTruthy();
  expect(within(nginx).getByText("1.0 MiB in · 2.0 MiB out")).toBeTruthy();
  expect(within(nginx).getByText("1 d 1 h")).toBeTruthy();
  const win = screen.getByRole("row", { name: /win-test/ });
  expect(within(win).getByText("stopped")).toBeTruthy();
  expect(within(win).getByText("VM")).toBeTruthy();
});

it("leads each row with the Workload's name, linked to its detail, so it stays put while the row scrolls sideways", async () => {
  await show("/proxmox");

  const media = screen.getByRole("row", { name: /media-stack/ }) as HTMLTableRowElement;
  const first = media.cells[0];
  expect(first.tagName).toBe("TH");
  expect(within(first).getByRole("link", { name: "media-stack" }).getAttribute("href")).toBe("/proxmox/workloads/102");
  // A template is marked as one.
  expect(within(screen.getByRole("row", { name: /debian-template/ })).getByText("template")).toBeTruthy();
});

it("draws skeleton rows until proxmox-api first answers", async () => {
  await show("/proxmox", { ListWorkloads: pending });

  expect(screen.getByRole("status", { name: "Loading" })).toBeTruthy();
  expect(screen.queryByText(/loading/i)).toBeNull();
});

it("shows IPs read from the running guest, and marks those from a stopped one's config", async () => {
  await show("/proxmox");

  const nginx = screen.getByRole("row", { name: /nginx/ });
  expect(within(nginx).getByText("198.18.1.10")).toBeTruthy();
  expect(within(nginx).getByText("198.18.1.11")).toBeTruthy();
  expect(within(nginx).queryByText("from config")).toBeNull();
  const win = screen.getByRole("row", { name: /win-test/ });
  expect(within(win).getByText("198.18.1.111")).toBeTruthy();
  expect(within(win).getByText("from config")).toBeTruthy();
  // On DHCP and stopped: only its MAC is known.
  const dhcp = screen.getByRole("row", { name: /dhcp-box/ });
  expect(within(dhcp).getByText("02:5e:1a:00:01:04")).toBeTruthy();
  const locked = screen.getByRole("row", { name: /locked-out/ });
  expect(within(locked).getByText("Proxmox refused the API token")).toBeTruthy();
});

it("shows why proxmox-api can't list the Workloads", async () => {
  await show("/proxmox", {
    ListWorkloads: () => unavailable("Proxmox refused the API token"),
  });

  expect(screen.getByRole("alert").textContent).toContain("Proxmox refused the API token");
  expect(screen.queryByRole("table")).toBeNull();
});

it("says proxmox-api isn't configured yet, with a way to configure it", async () => {
  await show("/proxmox", {
    ListWorkloads: () => Response.json({ code: "failed_precondition", message: "not configured" }, { status: 400 }),
  });

  expect(screen.getByText("proxmox-api isn't configured")).toBeTruthy();
  expect(screen.getByRole("link", { name: "Configure it in Plugins" }).getAttribute("href")).toBe("/plugins");
  expect(screen.queryByRole("alert")).toBeNull();
});

const names = () => screen.getAllByRole("rowheader").map((h) => h.textContent);
const sortBy = (header: string) => fireEvent.click(within(screen.getByRole("table")).getByRole("button", { name: header }));
const searchbox = () => screen.getByRole("searchbox", { name: "Search Workloads" });
const kind = () => screen.getByRole("combobox", { name: "Kind" });
const tab = (name: RegExp) => screen.getByRole("tab", { name });

it("has a column for each detail and none for power, which sits in each row's strip", async () => {
  await show("/proxmox");

  const headers = screen.getAllByRole("columnheader").map((h) => h.textContent);
  expect(headers).toEqual(["Name", "Status", "ID", "Kind", "Tags", "CPU", "Memory", "Disk", "Network", "Uptime", "IPs", "Actions"]);
});

it("sorts by ID until another sort is chosen; Tags, Network and IPs don't sort", async () => {
  await show("/proxmox");

  expect(names()).toEqual(["nginx", "media-stack", "dhcp-box", "win-test", "locked-out", "debian-template"]);
  expect(screen.getByRole("columnheader", { name: /^ID/ }).getAttribute("aria-sort")).toBe("ascending");
  const table = within(screen.getByRole("table"));
  expect(table.getAllByRole("button", { name: /^(Name|Status|ID|Kind|CPU|Memory|Disk|Uptime)$/ })).toHaveLength(8);
  for (const header of ["Tags", "Network", "IPs"]) expect(table.queryByRole("button", { name: header })).toBeNull();

  sortBy("Name");
  expect(names()).toEqual(["debian-template", "dhcp-box", "locked-out", "media-stack", "nginx", "win-test"]);
  expect(search).toBe("?sort=name");
  sortBy("Kind");
  expect(names().slice(0, 3)).toEqual(["nginx", "media-stack", "dhcp-box"]);
});

it("sorts CPU, Memory, Disk and Uptime by value, a stopped Workload using none", async () => {
  await show("/proxmox");

  sortBy("Memory");
  sortBy("Memory");
  // 3.0 GiB in use before 256 MiB, though 256 reads as the larger number.
  expect(names().slice(0, 2)).toEqual(["media-stack", "nginx"]);
  expect(search).toBe("?sort=-memory");

  sortBy("CPU");
  expect(names().slice(-2)).toEqual(["nginx", "media-stack"]);
  sortBy("Disk");
  sortBy("Disk");
  expect(names().slice(0, 2)).toEqual(["media-stack", "nginx"]);
  sortBy("Uptime");
  expect(names().slice(-2)).toEqual(["nginx", "media-stack"]);
});

it("searches the Workloads by name, ID, tag or IP, kept in the URL", async () => {
  await show("/proxmox");

  const find = (q: string) => {
    fireEvent.change(searchbox(), { target: { value: q } });
    return names();
  };
  expect(find("media")).toEqual(["media-stack"]);
  expect(find("111")).toEqual(["win-test"]);
  expect(find("lab")).toEqual(["win-test"]);
  expect(find("198.18.1.1")).toEqual(["nginx", "win-test"]);
  expect(find("web 198.18.1.11")).toEqual(["nginx"]);
  expect(search).toBe("?q=web+198.18.1.11");
});

it("says so when no Workload matches the search", async () => {
  await show("/proxmox?q=nothing-like-it");

  expect(screen.getByRole("table").textContent).toMatch(/No Workloads match "nothing-like-it"/);
});

it("shows All, Running or Stopped Workloads, counted, and filters them by kind", async () => {
  await show("/proxmox");

  expect(tab(/All/).textContent).toBe("All6");
  expect(tab(/Running/).textContent).toBe("Running2");
  expect(tab(/Stopped/).textContent).toBe("Stopped4");

  fireEvent.click(tab(/Stopped/));
  expect(names()).toEqual(["dhcp-box", "win-test", "locked-out", "debian-template"]);
  expect(search).toBe("?tab=stopped");

  fireEvent.change(kind(), { target: { value: "vm" } });
  expect(names()).toEqual(["win-test", "locked-out", "debian-template"]);
  // The counts follow the kind.
  expect(tab(/Running/).textContent).toBe("Running0");
  expect(search).toBe("?tab=stopped&kind=vm");

  fireEvent.click(tab(/Running/));
  expect(screen.getByRole("table").textContent).toMatch(/No Workloads match the filters/);
});

it("opens on the search, sort, tab and kind a link carries, and keeps them through a Workload's detail and Back", async () => {
  await show("/proxmox?q=198.18&sort=-name&tab=running&kind=lxc");

  expect(searchbox()).toHaveProperty("value", "198.18");
  expect(kind()).toHaveProperty("value", "lxc");
  expect(tab(/Running/).getAttribute("aria-selected")).toBe("true");
  expect(names()).toEqual(["nginx", "media-stack"]);

  fireEvent.click(screen.getByRole("link", { name: "media-stack" }));
  expect(screen.getByRole("heading", { level: 1, name: "media-stack" })).toBeTruthy();
  act(() => back());

  expect(searchbox()).toHaveProperty("value", "198.18");
  expect(names()).toEqual(["nginx", "media-stack"]);
  expect(search).toBe("?q=198.18&sort=-name&tab=running&kind=lxc");
});
