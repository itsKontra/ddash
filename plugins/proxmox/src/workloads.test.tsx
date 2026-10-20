import { cleanup, screen, within } from "@testing-library/react";
import { afterEach, expect, it } from "vitest";
import plugin from "./index";
import { show } from "./testing";

afterEach(cleanup);

it("is the proxmox Nav Entry at order 40 (its icon, server, is in plugin.yaml)", () => {
  expect(plugin.nav?.order).toBe(40);
});

it("lists every LXC and VM with its status, resources, tags and uptime", async () => {
  const calls = await show("/proxmox");

  expect(calls.find((c) => c.method === "ListWorkloads")?.url).toBe("/api/c/proxmox-api/ddash.proxmoxapi.ProxmoxApiService/ListWorkloads");
  const nginx = screen.getByRole("row", { name: /nginx/ });
  expect(within(nginx).getByRole("img", { name: "running" })).toBeTruthy();
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
  expect(within(win).getByRole("img", { name: "stopped" })).toBeTruthy();
  expect(within(win).getByText("VM")).toBeTruthy();
});

it("links each Workload's name to its detail", async () => {
  await show("/proxmox");

  const media = screen.getByRole("row", { name: /media-stack/ });
  expect(within(media).getByRole("link", { name: "media-stack" }).getAttribute("href")).toBe("/proxmox/workloads/102");
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
    ListWorkloads: () => Response.json({ code: "unavailable", message: "Proxmox refused the API token" }, { status: 503 }),
  });

  expect(screen.getByText("Proxmox refused the API token")).toBeTruthy();
});
