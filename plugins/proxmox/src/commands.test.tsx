import { cleanup, fireEvent, screen, within } from "@testing-library/react";
import { pending, settle, showCommands, type Answers } from "@ddash/plugin/testing";
import { afterEach, expect, it } from "vitest";
import plugin from "./index";
import { agents, bodies, listed, permissions, proxmox } from "./testing";

afterEach(cleanup);

// media-stack (102) has a paired Host Agent; nginx (101) is Protected.
const base: Answers = {
  ListWorkloads: () => Response.json(listed),
  ListPermissions: () => Response.json(permissions),
  ListAgents: () => Response.json(agents(102, "AGENT_STATE_PAIRED")),
};

it("offers each guest the power Actions its status allows, Update where a Host Agent is paired, and Update all", async () => {
  const shown = await showCommands(plugin, proxmox, { answers: base });

  expect(shown.commands()?.map((c) => [c.title, c.hint])).toEqual([
    ["Restart media-stack", "LXC 102"],
    ["Stop media-stack", "LXC 102"],
    ["Update media-stack", "LXC 102"],
    ["Start dhcp-box", "LXC 104"],
    ["Start win-test", "VM 111"],
    ["Start locked-out", "VM 112"],
    ["Update all running guests", "Proxmox"],
  ]);
  // Found by the issue's words too: reboot, shut down, and the node.
  const keywords = (title: string) => shown.commands()?.find((c) => c.title === title)?.keywords;
  expect(keywords("Restart media-stack")).toEqual(expect.arrayContaining(["reboot", "pve", "LXC"]));
  expect(keywords("Stop media-stack")).toEqual(expect.arrayContaining(["shut down"]));
});

it("offers a Protected guest no power Command, and none at all before the permissions are known", async () => {
  const shown = await showCommands(plugin, proxmox, { answers: base });
  expect(shown.commands()?.filter((c) => c.title.endsWith(" nginx"))).toEqual([]);

  cleanup();
  const unknown = await showCommands(plugin, proxmox, { answers: { ...base, ListPermissions: () => pending() } });
  expect(unknown.commands()?.map((c) => c.title)).toEqual(["Update media-stack", "Update all running guests"]);
});

it("restarts a guest only once the Operator confirms the Page's own question", async () => {
  const shown = await showCommands(plugin, proxmox, { answers: base });

  await shown.choose("Restart media-stack");
  const dialog = screen.getByRole("alertdialog");
  expect(dialog.textContent).toContain("Restart LXC 102 media-stack?");
  expect(bodies(shown.calls, "RestartWorkload")).toEqual([]);
  fireEvent.click(within(dialog).getByRole("button", { name: "Restart" }));
  await settle();

  expect(bodies(shown.calls, "RestartWorkload")).toEqual([{ workload: { kind: "KIND_LXC", host: "pve", id: "102" } }]);
});

it("starts a stopped guest as the Page's Start does", async () => {
  const shown = await showCommands(plugin, proxmox, { answers: base });

  await shown.choose("Start dhcp-box");
  expect(bodies(shown.calls, "StartWorkload")).toEqual([{ workload: { kind: "KIND_LXC", host: "pve", id: "104" } }]);
});

it("opens the guest's update Plan and Update all's Plan, running nothing before they are confirmed", async () => {
  const shown = await showCommands(plugin, proxmox, {
    answers: {
      ...base,
      PlanUpdate: () => Response.json({ plan: { workload: { kind: "KIND_LXC", host: "pve", id: "102" }, label: "LXC 102 media-stack", snapshotSupported: true } }),
      PlanUpdateAll: () => Response.json({ workloads: [] }),
    },
  });

  await shown.choose("Update media-stack");
  expect(screen.getByRole("dialog", { name: "Update LXC 102 media-stack" })).toBeTruthy();
  expect(bodies(shown.calls, "PlanUpdate")).toEqual([{ workload: { kind: "KIND_LXC", host: "pve", id: "102" } }]);
  fireEvent.click(within(screen.getByRole("dialog")).getByRole("button", { name: "Cancel" }));
  await settle();

  await shown.choose("Update all running guests");
  expect(screen.getByRole("dialog", { name: "Update all running" })).toBeTruthy();
  expect(bodies(shown.calls, "RunUpdate")).toEqual([]);
  expect(bodies(shown.calls, "RunUpdateAll")).toEqual([]);
});

it("reads the permissions and Host Agents once, without polling them on every Page", async () => {
  const shown = await showCommands(plugin, proxmox, { answers: base });
  expect(shown.calls.filter((c) => c.method === "ListPermissions")).toHaveLength(1);
  expect(shown.calls.filter((c) => c.method === "ListAgents")).toHaveLength(1);
  expect(shown.calls.filter((c) => c.method === "ListPowerTasks" || c.method === "ListUpdates" || c.method === "ListPackages")).toEqual([]);
});

it("offers nothing until proxmox-api listed the Workloads", async () => {
  const shown = await showCommands(plugin, proxmox, { answers: { ...base, ListWorkloads: () => pending() } });
  expect(shown.commands()).toBeUndefined();
});
