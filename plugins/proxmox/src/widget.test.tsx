import { cleanup, screen, within } from "@testing-library/react";
import { metricValue, pending, showWidget, unavailable, type Answer } from "@ddash/plugin/testing";
import { afterEach, expect, it } from "vitest";
import plugin from "./index";
import { proxmox } from "./testing";

afterEach(cleanup);

const workload = (vmid: number, kind: "KIND_LXC" | "KIND_VM", status: string, extra: object = {}) => ({
  kind,
  vmid,
  node: "pve",
  name: `guest-${vmid}`,
  status,
  ...extra,
});

const listed = {
  workloads: [
    workload(101, "KIND_LXC", "running"),
    workload(102, "KIND_LXC", "running"),
    workload(103, "KIND_LXC", "stopped"),
    workload(110, "KIND_VM", "running"),
    workload(111, "KIND_VM", "stopped"),
    // A template never runs, so it isn't counted.
    workload(9000, "KIND_VM", "stopped", { template: true }),
  ],
};

const guests = () => plugin.widgets!.find((w) => w.id === "guests")!;

// Renders the Widget's body at size, against proxmox-api answering with
// answer, and returns what its useStatus reported last.
async function show(answer: Answer, size?: "S" | "M") {
  const { reported } = await showWidget(proxmox, guests(), { size, answers: { ListWorkloads: answer } });
  return reported;
}

it("is the guests Widget, S or M, S by default", () => {
  const w = guests();
  expect([w.title, w.sizes, w.defaultSize]).toEqual(["Guests", ["S", "M"], "S"]);
});

it("reports up with the counts in its summary", async () => {
  const reported = await show(() => Response.json(listed));

  expect(reported.status).toEqual({ tone: "up", summary: "3 of 5 running · 3 LXCs, 2 VMs" });
});

it("shows the counts at S, and each kind's at M", async () => {
  await show(() => Response.json(listed));
  const s = screen.getByRole("region", { name: "widget" });
  expect(metricValue("running", s)).toBe("3");
  expect(metricValue("guests", s)).toBe("5");
  expect(within(s).queryByText("LXCs")).toBeNull();
  cleanup();

  await show(() => Response.json(listed), "M");
  const m = screen.getByRole("region", { name: "widget" });
  expect(within(m).getByText("LXCs").nextElementSibling?.textContent).toBe("2/3 running");
  expect(within(m).getByText("VMs").nextElementSibling?.textContent).toBe("1/2 running");
});

it("draws its counts as skeletons until proxmox-api first answers", async () => {
  await show(pending);
  const s = screen.getByRole("region", { name: "widget" });

  expect(metricValue("running", s)).toBeUndefined();
  expect(metricValue("guests", s)).toBeUndefined();
});

it("reports warn when proxmox-api is unavailable", async () => {
  const reported = await show(() => unavailable("Proxmox refused the API token"));

  expect(reported.status).toEqual({ tone: "warn", summary: "proxmox-api unavailable: Proxmox refused the API token" });
});

it("reports nothing until proxmox-api first answers", async () => {
  const reported = await show(pending);

  expect(reported.status).toBeUndefined();
});

// Hosts: the node and its guests.

const GiB = 1024 ** 3;
const hostsWidget = () => plugin.widgets!.find((w) => w.id === "hosts")!;

const pveNode = {
  name: "pve",
  status: "online",
  cpu: 0.022,
  maxCpu: 16,
  mem: String(11 * GiB),
  maxMem: String(64 * GiB),
  storages: [
    { name: "local", content: ["iso", "backup"], active: true, avail: String(60 * GiB), total: String(100 * GiB) },
    { name: "local-lvm", content: ["images", "rootdir"], active: true, avail: String(331 * GiB), total: String(476 * GiB) },
  ],
};

const loaded = (mem: number, disk: number) => ({ mem: String(Math.round(mem * GiB)), maxMem: String(GiB * 10), disk: String(Math.round(disk * GiB)), maxDisk: String(GiB * 10) });

const hostsListed = {
  workloads: [
    workload(101, "KIND_LXC", "running", { name: "quiet", ...loaded(2, 3) }),
    workload(102, "KIND_LXC", "running", { name: "busy", ...loaded(8.2, 1) }),
    workload(103, "KIND_LXC", "stopped", { name: "off" }),
  ],
};

async function showHosts(answers: Record<string, Answer>) {
  const { reported } = await showWidget(proxmox, hostsWidget(), { answers });
  return { reported, widget: screen.getByRole("region", { name: "widget" }) };
}

it("is the Hosts Widget, S or M, S by default", () => {
  const w = hostsWidget();
  expect([w.title, w.sizes, w.defaultSize]).toEqual(["Hosts", ["S", "M"], "S"]);
});

it("Hosts shows the guests running, the node's three meters and the guest under most pressure", async () => {
  const { reported, widget } = await showHosts({ ListWorkloads: () => Response.json(hostsListed), ListNodes: () => Response.json({ nodes: [pveNode] }) });

  expect(within(widget).getByText("2 of 3 guests running")).toBeTruthy();
  expect(within(widget).getAllByRole("meter").map((m) => [m.getAttribute("aria-label"), m.getAttribute("aria-valuenow")])).toEqual([
    ["CPU", "2"],
    ["Memory", "17"],
    ["Storage", "30"],
  ]);
  expect(within(widget).getByText("11.0 GiB of 64.0 GiB")).toBeTruthy();
  // A guest above 75 % is named, with the resource and how full.
  expect(within(widget).getAllByRole("listitem").map((li) => li.textContent)).toEqual(["busymemory 82 %", "quietdisk 30 %"]);
  expect(reported.status).toEqual({ tone: "warn", summary: "busy memory 82 %" });
  // No containers line: they're the docker Plugin's Docker hosts Widget.
  expect(within(widget).queryByText(/container/)).toBeNull();
});

it("Hosts is up with the running count while nothing is tight", async () => {
  const calm = { workloads: hostsListed.workloads.filter((w) => w.vmid !== 102) };
  const { reported } = await showHosts({ ListWorkloads: () => Response.json(calm), ListNodes: () => Response.json({ nodes: [pveNode] }) });

  expect(reported.status).toEqual({ tone: "up", summary: "1 of 2 guests running" });
});

it("Hosts is down when a node's meter is at 90 % or more", async () => {
  const full = { ...pveNode, mem: String(60 * GiB) };
  const { reported } = await showHosts({ ListWorkloads: () => Response.json(hostsListed), ListNodes: () => Response.json({ nodes: [full] }) });

  expect(reported.status).toEqual({ tone: "down", summary: "Memory 94 % · busy memory 82 %" });
});

it("Hosts says why it has no meters when the token can't read the node", async () => {
  const { widget } = await showHosts({ ListWorkloads: () => Response.json(hostsListed), ListNodes: () => Response.json({}) });

  expect(within(widget).queryByRole("meter")).toBeNull();
  expect(within(widget).getByText("the API token can't read the node (Sys.Audit on /nodes)")).toBeTruthy();
});

it("Hosts reports warn when proxmox-api is unavailable", async () => {
  const { reported } = await showHosts({ ListWorkloads: () => unavailable("Proxmox refused the API token"), ListNodes: () => unavailable("Proxmox refused the API token") });

  expect(reported.status).toEqual({ tone: "warn", summary: "proxmox-api unavailable: Proxmox refused the API token" });
});
