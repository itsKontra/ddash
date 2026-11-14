import { cleanup, fireEvent, screen, within } from "@testing-library/react";
import { afterEach, expect, it } from "vitest";
import { settle } from "@ddash/plugin/testing";
import { agents, bodies, daysAgo, show } from "./testing";

afterEach(cleanup);

const panel = (name: string) => screen.getByRole("region", { name });
const media = { kind: "KIND_LXC", host: "pve", id: "102" };

it("shows the ddash Snapshot with its age, the snapshots taken by hand, and takes a new one", async () => {
  const calls = await show("/proxmox/workloads/102", {
    GetSnapshots: () =>
      Response.json({
        supported: true,
        ddashSnapshot: { name: "ddash-20261111-020000", description: "Created by ddash before an update", created: daysAgo(3.5) },
        others: [{ name: "before-upgrade", created: daysAgo(40.5) }],
      }),
    TakeSnapshot: () => Response.json({ snapshot: { name: "ddash-20261114-101500" }, deleted: ["ddash-20261111-020000"] }),
  });

  const snaps = panel("Snapshots & backups");
  expect(snaps.textContent).toContain("ddash-20261111-020000 · 3 d ago");
  expect(snaps.textContent).toContain("before-upgrade · 40 d ago");

  fireEvent.click(within(snaps).getByRole("button", { name: "Take snapshot" }));
  await settle();
  expect(bodies(calls, "TakeSnapshot")).toEqual([{ workload: media }]);
  expect(panel("Snapshots & backups").textContent).toContain("Deleted the older ddash Snapshot ddash-20261111-020000");
});

it("says snapshots aren't supported where the storage can't take one", async () => {
  await show("/proxmox/workloads/102", { GetSnapshots: () => Response.json({}) });

  const snaps = panel("Snapshots & backups");
  expect(within(snaps).getByText("snapshots not supported")).toBeTruthy();
  expect((within(snaps).getByRole("button", { name: "Take snapshot" }) as HTMLButtonElement).disabled).toBe(true);
});

it("backs up to the storage picked, and follows the backup", async () => {
  const calls = await show("/proxmox/workloads/102", {
    ListBackupStorages: () =>
      Response.json({
        storages: [
          { name: "local", type: "dir", active: true },
          { name: "pbs", type: "pbs", active: true },
        ],
      }),
    ListBackups: () => Response.json({ backups: [{ workload: media, storage: "pbs", state: "STATE_DONE", started: daysAgo(1), ended: daysAgo(1) }] }),
  });

  const snaps = panel("Snapshots & backups");
  expect(bodies(calls, "ListBackupStorages")).toEqual([{ node: "pve" }]);
  fireEvent.change(within(snaps).getByRole("combobox", { name: "Backup storage" }), { target: { value: "pbs" } });
  fireEvent.click(within(snaps).getByRole("button", { name: "Back up" }));
  await settle();
  expect(bodies(calls, "Backup")).toEqual([{ workload: media, storage: "pbs" }]);
  expect(calls.find((c) => c.method === "Backup")?.url).toBe("/api/c/proxmox/ddash.proxmox.ProxmoxService/Backup");
  expect(snaps.textContent).toContain("Backup to pbs");
});

const ssh = {
  installed: true,
  running: true,
  enabled: true,
  passwordAuthentication: true,
  permitRootLogin: "prohibit-password",
  users: ["root", "ops"],
  keys: [
    { user: "root", name: "laptop", type: "ssh-ed25519", fingerprint: "SHA256:lap" },
    { user: "ops", type: "ssh-rsa", fingerprint: "SHA256:old", comment: "ops@old-pc" },
  ],
};

it("lists every installed key, ddash's and others, and removes a ddash one once confirmed", async () => {
  const calls = await show("/proxmox/workloads/102", {
    ListAgents: () => Response.json(agents(102, "AGENT_STATE_PAIRED")),
    GetSSH: () => Response.json({ ssh }),
  });

  const sshPanel = panel("SSH");
  expect(within(sshPanel).getByRole("row", { name: /ops/ }).textContent).toContain("not from ddash");
  // Only ddash's keys can be removed from here.
  expect(within(within(sshPanel).getByRole("row", { name: /ops/ })).queryByRole("button")).toBeNull();

  fireEvent.click(within(within(sshPanel).getByRole("row", { name: /laptop/ })).getByRole("button", { name: "Remove" }));
  await settle();
  fireEvent.click(within(screen.getByRole("alertdialog")).getByRole("button", { name: "Remove" }));
  await settle();
  expect(bodies(calls, "RemoveKey")).toEqual([{ vmid: 102, user: "root", name: "laptop" }]);
});

it("installs stored SSH Keys for a user", async () => {
  const calls = await show("/proxmox/workloads/102", {
    ListAgents: () => Response.json(agents(102, "AGENT_STATE_PAIRED")),
    GetSSH: () => Response.json({ ssh }),
    ListSSHKeys: () => Response.json({ keys: [{ name: "laptop" }, { name: "desktop" }] }),
  });

  const sshPanel = panel("SSH");
  fireEvent.click(within(sshPanel).getByRole("checkbox", { name: "desktop" }));
  fireEvent.change(within(sshPanel).getByRole("combobox", { name: "For user" }), { target: { value: "ops" } });
  fireEvent.click(within(sshPanel).getByRole("button", { name: "Install" }));
  await settle();
  expect(bodies(calls, "InstallSSHKey")).toEqual([{ workload: media, names: ["desktop"], user: "ops" }]);
});

it("turns password login off through the Host Agent, and shows its refusal", async () => {
  const calls = await show("/proxmox/workloads/102", {
    ListAgents: () => Response.json(agents(102, "AGENT_STATE_PAIRED")),
    GetSSH: () => Response.json({ ssh }),
    SetSSHSettings: () => Response.json({ code: "failed_precondition", message: "no SSH Key is installed for a user who could still log in" }, { status: 400 }),
  });

  fireEvent.click(within(panel("SSH")).getByRole("switch", { name: "Password login" }));
  await settle();
  fireEvent.click(within(screen.getByRole("alertdialog")).getByRole("button", { name: "Apply" }));
  await settle();
  expect(bodies(calls, "SetSSHSettings")).toEqual([{ vmid: 102, permitRootLogin: "prohibit-password" }]);
  expect(panel("SSH").textContent).toContain("no SSH Key is installed for a user who could still log in");
});

it("enables sshd where it isn't installed", async () => {
  const calls = await show("/proxmox/workloads/102", {
    ListAgents: () => Response.json(agents(102, "AGENT_STATE_PAIRED")),
    GetSSH: () => Response.json({ ssh: { users: ["root"] } }),
    EnableSSH: () => Response.json({ job: { id: "j1", state: "STATE_RUNNING" } }),
    ListJobs: () => Response.json({ hosts: [{ vmid: 102, jobs: [{ id: "j1", state: "STATE_RUNNING", steps: [{ title: "Install OpenSSH", state: "STATE_RUNNING" }] }] }] }),
  });

  const sshPanel = panel("SSH");
  expect(within(sshPanel).getByText("not installed")).toBeTruthy();
  fireEvent.click(within(sshPanel).getByRole("button", { name: "Enable sshd" }));
  await settle();
  expect(bodies(calls, "EnableSSH")).toEqual([{ workload: media }]);
  expect(calls.find((c) => c.method === "EnableSSH")?.url).toBe("/api/c/proxmox/ddash.proxmox.ProxmoxService/EnableSSH");
  expect(panel("SSH").textContent).toContain("Install OpenSSH");
});
