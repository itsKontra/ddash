import { cleanup, fireEvent, screen, within } from "@testing-library/react";
import { afterEach, expect, it } from "vitest";
import { settle } from "@ddash/plugin/testing";
import { agents, bodies, daysAgo, show } from "./testing";

afterEach(cleanup);

const panel = (name: string) => screen.getByRole("region", { name });

const packages = [
  { name: "libc6", current: "2.36-9+deb12u7", new: "2.36-9+deb12u9" },
  { name: "openssl", current: "3.0.13-1~deb12u1", new: "3.0.15-1~deb12u1" },
];

const pendingOn = (vmid: number, extra: Record<string, unknown> = {}) => ({ hosts: [{ vmid, packages, read: daysAgo(0), ...extra }] });

const plan = (vmid: number, label: string, extra: Record<string, unknown> = {}) => ({
  plan: {
    workload: { kind: "KIND_LXC", host: "pve", id: String(vmid) },
    label,
    packages,
    snapshot: true,
    snapshotSupported: true,
    replacesSnapshot: "ddash-20261101-020000",
    ...extra,
  },
});

/** Opens the Update Plan Dialog from the Updates Panel. */
async function openPlan() {
  fireEvent.click(within(panel("Updates")).getByRole("button", { name: "Update" }));
  await settle();
  return screen.getByRole("dialog");
}

/** Confirms the Plan: its Dialog is the only confirmation. */
async function confirm(dialog: HTMLElement) {
  fireEvent.click(within(dialog).getByRole("button", { name: "Update" }));
  await settle();
  expect(screen.queryByRole("alertdialog")).toBeNull();
}

it("lists the pending packages the Host Agent read, and plans an update with a ddash Snapshot that replaces the old one", async () => {
  const calls = await show("/proxmox/workloads/102", {
    ListAgents: () => Response.json(agents(102, "AGENT_STATE_PAIRED")),
    ListPackages: () => Response.json(pendingOn(102)),
    PlanUpdate: () => Response.json(plan(102, "LXC 102 media-stack")),
  });

  const updates = panel("Updates");
  expect(updates.textContent).toContain("2 updates pending");
  expect(within(updates).getByRole("list", { name: "Pending packages" }).textContent).toContain("libc6 2.36-9+deb12u7 → 2.36-9+deb12u9");

  const dialog = await openPlan();
  expect(bodies(calls, "PlanUpdate")).toEqual([{ workload: { kind: "KIND_LXC", host: "pve", id: "102" } }]);
  expect(dialog.textContent).toContain("2 updates, as the Host Agent last read them");
  expect(dialog.textContent).toContain("deletes ddash-20261101-020000 once it succeeded");
  expect(dialog.textContent).toContain("in Maintenance while it updates");
  expect(dialog.textContent).toContain("ddash never reboots it");
  expect(within(dialog).getByRole("switch", { name: "Take a ddash Snapshot first" }).getAttribute("aria-checked")).toBe("true");

  await confirm(dialog);
  expect(bodies(calls, "RunUpdate")).toEqual([{ workload: { kind: "KIND_LXC", host: "pve", id: "102" }, snapshot: true }]);
});

it("sends the snapshot turned off for this run when the Operator switches it off", async () => {
  const calls = await show("/proxmox/workloads/102", {
    ListAgents: () => Response.json(agents(102, "AGENT_STATE_PAIRED")),
    ListPackages: () => Response.json(pendingOn(102)),
    PlanUpdate: () => Response.json(plan(102, "LXC 102 media-stack")),
  });

  const dialog = await openPlan();
  fireEvent.click(within(dialog).getByRole("switch", { name: "Take a ddash Snapshot first" }));
  expect(dialog.textContent).toContain("No snapshot this time");
  await confirm(dialog);
  expect(bodies(calls, "RunUpdate")).toEqual([{ workload: { kind: "KIND_LXC", host: "pve", id: "102" }, snapshot: false }]);
});

it("updates without a snapshot where the storage can't take one, saying so", async () => {
  await show("/proxmox/workloads/102", {
    ListAgents: () => Response.json(agents(102, "AGENT_STATE_PAIRED")),
    ListPackages: () => Response.json(pendingOn(102)),
    PlanUpdate: () => Response.json(plan(102, "LXC 102 media-stack", { snapshotSupported: false, replacesSnapshot: "" })),
  });

  const dialog = await openPlan();
  const toggle = within(dialog).getByRole("switch", { name: "Take a ddash Snapshot first" }) as HTMLButtonElement;
  expect(toggle.getAttribute("aria-checked")).toBe("false");
  expect(toggle.disabled).toBe(true);
  expect(dialog.textContent).toContain("Snapshots not supported");
});

it("needs the explicit override to update a Protected Workload", async () => {
  const calls = await show("/proxmox/workloads/101", {
    ListAgents: () => Response.json(agents(101, "AGENT_STATE_PAIRED")),
    ListPackages: () => Response.json(pendingOn(101)),
    PlanUpdate: () => Response.json(plan(101, "LXC 101 nginx", { protected: true })),
  });

  const dialog = await openPlan();
  const update = within(dialog).getByRole("button", { name: "Update" }) as HTMLButtonElement;
  expect(update.disabled).toBe(true);
  fireEvent.click(within(dialog).getByRole("checkbox", { name: "Update anyway: it's Protected" }));
  expect(update.disabled).toBe(false);
  await confirm(dialog);
  expect(bodies(calls, "RunUpdate")).toEqual([{ workload: { kind: "KIND_LXC", host: "pve", id: "101" }, snapshot: true, overrideProtected: true }]);
});

it("can't confirm a Plan that is blocked, and says why", async () => {
  await show("/proxmox/workloads/102", {
    ListAgents: () => Response.json(agents(102, "AGENT_STATE_PAIRED")),
    ListPackages: () => Response.json(pendingOn(102)),
    PlanUpdate: () => Response.json(plan(102, "LXC 102 media-stack", { blocked: "An update already runs on LXC 102 media-stack" })),
  });

  const dialog = await openPlan();
  expect(dialog.textContent).toContain("An update already runs");
  expect((within(dialog).getByRole("button", { name: "Update" }) as HTMLButtonElement).disabled).toBe(true);
});

it("shows a running update's steps on the Workload, wherever it was started, and no second Update meanwhile", async () => {
  await show("/proxmox/workloads/102", {
    ListAgents: () => Response.json(agents(102, "AGENT_STATE_PAIRED")),
    ListPackages: () => Response.json(pendingOn(102)),
    ListUpdates: () =>
      Response.json({
        runs: [
          {
            id: "r1",
            workload: { kind: "KIND_LXC", host: "pve", id: "102" },
            label: "LXC 102 media-stack",
            state: "STATE_RUNNING",
            started: daysAgo(0),
            snapshot: "ddash-20261114-101500",
            steps: [
              { title: "Take a ddash Snapshot", state: "STATE_DONE", detail: "ddash-20261114-101500" },
              { title: "Delete older ddash Snapshots", state: "STATE_DONE", detail: "ddash-20261101-020000" },
              { title: "Install updates", state: "STATE_RUNNING", log: ["Unpacking libc6 (2.36-9+deb12u9) ..."] },
            ],
          },
        ],
      }),
  });

  const progress = within(panel("Updates")).getByRole("region", { name: "Update of LXC 102 media-stack" });
  expect(progress.textContent).toContain("updating");
  expect(progress.textContent).toContain("Take a ddash Snapshot");
  expect(within(progress).getByRole("region", { name: "Output of Install updates" }).textContent).toContain("Unpacking libc6");
  expect((within(panel("Updates")).getByRole("button", { name: "Update" }) as HTMLButtonElement).disabled).toBe(true);
});

it("shows reboot required after an update, with the Restart beside it", async () => {
  await show("/proxmox/workloads/102", {
    ListAgents: () => Response.json(agents(102, "AGENT_STATE_PAIRED")),
    ListPackages: () => Response.json({ hosts: [{ vmid: 102, read: daysAgo(0), rebootRequired: true }] }),
    ListUpdates: () =>
      Response.json({
        runs: [
          {
            id: "r1",
            workload: { kind: "KIND_LXC", host: "pve", id: "102" },
            label: "LXC 102 media-stack",
            state: "STATE_DONE",
            started: daysAgo(0),
            ended: daysAgo(0),
            updated: 2,
            rebootRequired: true,
            steps: [{ title: "Install updates", state: "STATE_DONE" }],
          },
        ],
      }),
  });

  const updates = panel("Updates");
  expect(updates.textContent).toContain("Up to date");
  expect(updates.textContent).toContain("Updated 2 packages");
  expect(within(updates).getByText("reboot required")).toBeTruthy();
  expect(within(updates).getByRole("button", { name: "Restart" })).toBeTruthy();
});

it("turns the Workload's snapshot before each update off", async () => {
  const calls = await show("/proxmox/workloads/102", {
    ListAgents: () => Response.json(agents(102, "AGENT_STATE_PAIRED")),
    ListPackages: () => Response.json(pendingOn(102)),
  });

  const setting = within(panel("Updates")).getByRole("switch", { name: "Snapshot before each update" });
  expect(setting.getAttribute("aria-checked")).toBe("true");
  fireEvent.click(setting);
  await settle();
  expect(bodies(calls, "SetUpdateSettings")).toEqual([{ settings: { vmid: 102 } }]);
});

it("says why the snapshot setting couldn't be saved", async () => {
  await show("/proxmox/workloads/102", {
    ListAgents: () => Response.json(agents(102, "AGENT_STATE_PAIRED")),
    ListPackages: () => Response.json(pendingOn(102)),
    SetUpdateSettings: () => Response.json({ code: "unavailable", message: "the settings can't be written" }, { status: 503 }),
  });

  fireEvent.click(within(panel("Updates")).getByRole("switch", { name: "Snapshot before each update" }));
  await settle();

  expect(within(panel("Updates")).getByText("the settings can't be written")).toBeTruthy();
});
