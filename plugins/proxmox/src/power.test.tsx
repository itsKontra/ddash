import { cleanup, fireEvent, screen, within } from "@testing-library/react";
import { afterEach, expect, it } from "vitest";
import { pending, settle, unavailable } from "@ddash/plugin/testing";
import { bodies, show } from "./testing";

afterEach(cleanup);

const menu = (name: string) => within(screen.getByRole("row", { name: new RegExp(name) })).queryByRole("button", { name: `Power for ${name}` });

async function pick(name: string, item: string) {
  fireEvent.click(menu(name)!);
  fireEvent.click(screen.getByRole("menuitem", { name: item }));
  await settle();
}

it("offers Stop and Restart on a running Workload and Start on a stopped one, from its row's menu", async () => {
  await show("/proxmox");

  fireEvent.click(menu("media-stack")!);
  expect(screen.getAllByRole("menuitem").map((i) => i.textContent)).toEqual(["Restart", "Stop"]);
  fireEvent.keyDown(screen.getByRole("menu"), { key: "Escape" });

  fireEvent.click(menu("dhcp-box")!);
  expect(screen.getAllByRole("menuitem").map((i) => i.textContent)).toEqual(["Start"]);
});

it("shows a protected-in-Proxmox lock in place of the menu where ddash can't power the Workload", async () => {
  await show("/proxmox");

  const nginx = screen.getByRole("row", { name: /nginx/ });
  expect(menu("nginx")).toBeNull();
  expect(within(nginx).getByText("protected in Proxmox")).toBeTruthy();
  // A template has no power Actions.
  expect(menu("debian-template")).toBeNull();
});

it("stops a Workload once the Operator confirms, naming it by kind and VMID", async () => {
  const calls = await show("/proxmox");

  await pick("media-stack", "Stop");
  const dialog = screen.getByRole("alertdialog");
  expect(dialog.textContent).toContain("Stop LXC 102 media-stack?");
  expect(bodies(calls, "StopWorkload")).toEqual([]);
  fireEvent.click(within(dialog).getByRole("button", { name: "Stop" }));
  await settle();

  expect(bodies(calls, "StopWorkload")).toEqual([{ workload: { kind: "KIND_LXC", host: "pve", id: "102" } }]);
  expect(calls.find((c) => c.method === "StopWorkload")?.url).toBe("/api/c/proxmox/ddash.proxmox.ProxmoxService/StopWorkload");
});

it("restarts only after the confirm, and does nothing when cancelled", async () => {
  const calls = await show("/proxmox");

  await pick("media-stack", "Restart");
  fireEvent.click(within(screen.getByRole("alertdialog")).getByRole("button", { name: "Cancel" }));
  await settle();
  expect(bodies(calls, "RestartWorkload")).toEqual([]);

  await pick("media-stack", "Restart");
  fireEvent.click(within(screen.getByRole("alertdialog")).getByRole("button", { name: "Restart" }));
  await settle();
  expect(bodies(calls, "RestartWorkload")).toEqual([{ workload: { kind: "KIND_LXC", host: "pve", id: "102" } }]);
});

it("starts a stopped Workload at once, without a confirm", async () => {
  const calls = await show("/proxmox");

  await pick("win-test", "Start");

  expect(screen.queryByRole("alertdialog")).toBeNull();
  expect(bodies(calls, "StartWorkload")).toEqual([{ workload: { kind: "KIND_VM", host: "pve", id: "111" } }]);
});

it("disables a Workload's power Actions while its task runs", async () => {
  await show("/proxmox", {
    ListPowerTasks: () =>
      Response.json({ tasks: [{ workload: { kind: "KIND_LXC", host: "pve", id: "102" }, action: "POWER_ACTION_STOP", started: "2026-10-20T09:00:00Z" }] }),
  });

  expect((menu("media-stack") as HTMLButtonElement).disabled).toBe(true);
  expect(within(screen.getByRole("row", { name: /media-stack/ })).getByText("stopping")).toBeTruthy();
  expect((menu("dhcp-box") as HTMLButtonElement).disabled).toBe(false);
});

it("shows why a power Action failed in its row", async () => {
  await show("/proxmox", {
    StartWorkload: () => Response.json({ code: "permission_denied", message: "VM 111 win-test is protected in Proxmox" }, { status: 403 }),
  });

  await pick("win-test", "Start");

  expect(within(screen.getByRole("row", { name: /win-test/ })).getByRole("alert").textContent).toContain("VM 111 win-test is protected in Proxmox");
});

it("has the power Actions as buttons on a Workload's detail", async () => {
  const calls = await show("/proxmox/workloads/102");

  expect(screen.getByRole("heading", { level: 1, name: "media-stack" })).toBeTruthy();
  // The kicker sits right above the title.
  expect(screen.getByRole("heading", { level: 1, name: "media-stack" }).previousElementSibling?.textContent).toBe("Workload");
  const power = screen.getByRole("region", { name: "Power" });
  fireEvent.click(within(power).getByRole("button", { name: "Restart" }));
  fireEvent.click(within(screen.getByRole("alertdialog")).getByRole("button", { name: "Restart" }));
  await settle();
  expect(bodies(calls, "RestartWorkload")).toEqual([{ workload: { kind: "KIND_LXC", host: "pve", id: "102" } }]);
  expect(within(power).getByRole("button", { name: "Stop" })).toBeTruthy();
  expect(within(power).queryByRole("button", { name: "Start" })).toBeNull();
  const details = screen.getByRole("region", { name: "Details" });
  expect(within(details).getByText("198.18.2.10")).toBeTruthy();
  expect(within(details).getByText("Node").nextElementSibling?.textContent).toBe("pve");
  expect(within(details).getByText("Memory").nextElementSibling?.textContent).toBe("3.0 GiB / 8.0 GiB");
});

it("links a Workload's detail back to the Workloads", async () => {
  await show("/proxmox/workloads/102");

  expect(screen.getByRole("link", { name: "Proxmox" }).getAttribute("href")).toBe("/proxmox");
});

it("draws a skeleton for the power Actions while ddash's permissions are read", async () => {
  await show("/proxmox/workloads/102", { ListPermissions: pending });

  const power = screen.getByRole("region", { name: "Power" });
  expect(within(power).getByRole("status", { name: "Loading" })).toBeTruthy();
  expect(within(power).queryByRole("button")).toBeNull();
  expect(screen.queryByText(/reading/i)).toBeNull();
});

it("draws a skeleton for a Workload's detail until proxmox-api first answers", async () => {
  await show("/proxmox/workloads/102", { ListWorkloads: pending });

  expect(screen.getByRole("status", { name: "Loading" })).toBeTruthy();
  expect(screen.getByRole("link", { name: "Proxmox" })).toBeTruthy();
});

it("shows why proxmox-api can't list the Workloads on a Workload's detail", async () => {
  await show("/proxmox/workloads/102", {
    ListWorkloads: () => unavailable("Proxmox refused the API token"),
  });

  expect(screen.getByRole("alert").textContent).toContain("Proxmox refused the API token");
  expect(screen.getByRole("link", { name: "Proxmox" }).getAttribute("href")).toBe("/proxmox");
});

it("shows the lock on a Protected Workload's detail, and why a Workload isn't listed", async () => {
  await show("/proxmox/workloads/101");

  const power = screen.getByRole("region", { name: "Power" });
  expect(within(power).getByText("protected in Proxmox")).toBeTruthy();
  expect(within(power).queryByRole("button")).toBeNull();

  cleanup();
  await show("/proxmox/workloads/999");
  expect(screen.getByText("Proxmox lists no Workload 999.")).toBeTruthy();
  expect(screen.queryByRole("alert")).toBeNull();
  expect(screen.getByRole("link", { name: "Proxmox" }).getAttribute("href")).toBe("/proxmox");
});
