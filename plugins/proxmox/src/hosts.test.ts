import { create } from "@bufbuild/protobuf";
import { NodeSchema, StorageSchema, WorkloadSchema } from "@ddash/proto/proxmox-api/proxmox_api_pb";
import { expect, it } from "vitest";
import type { Concern } from "@ddash/plugin";
import {
  guestConcerns,
  guestsRunning,
  hostsStatus,
  nodeConcerns,
  nodeMeters,
  nodeTone,
  pressure,
  pressureRow,
  tightGuests,
  underPressure,
  type NodeMeter,
} from "./hosts";

const GiB = 1024n ** 3n;

const guest = (name: string, status: string, mem: number, disk: number, extra: { template?: boolean } = {}) =>
  create(WorkloadSchema, {
    name,
    status,
    mem: (BigInt(mem) * GiB) / 100n,
    maxMem: GiB,
    disk: (BigInt(disk) * 10n * GiB) / 100n,
    maxDisk: 10n * GiB,
    ...extra,
  });

const workloads = [
  guest("quiet", "running", 20, 30),
  guest("full-disk", "running", 40, 91),
  guest("busy", "running", 82, 10),
  guest("off", "stopped", 99, 99),
  guest("template", "running", 99, 99, { template: true }),
];

it("a guest's pressure is the fuller of its memory and its disk", () => {
  expect(pressure(workloads[1])).toMatchObject({ resource: "disk", ratio: expect.closeTo(0.91) });
  expect(pressure(workloads[2])).toMatchObject({ resource: "memory", ratio: expect.closeTo(0.82) });
  // No maximum known: no pressure, rather than a division by zero.
  expect(pressure(create(WorkloadSchema, { mem: 5n })).ratio).toBe(0);
});

it("the guests under most pressure are the running ones, fullest first", () => {
  expect(underPressure(workloads).map((p) => p.workload.name)).toEqual(["full-disk", "busy"]);
  expect(underPressure(workloads, 5).map((p) => p.workload.name)).toEqual(["full-disk", "busy", "quiet"]);
});

it("the tight guests are the running ones at the threshold or fuller", () => {
  expect(tightGuests(workloads).map((p) => [p.workload.name, p.resource])).toEqual([
    ["full-disk", "disk"],
    ["busy", "memory"],
  ]);
  expect(tightGuests(workloads, 0.85).map((p) => p.workload.name)).toEqual(["full-disk"]);
});

it("a pressure row names the guest, the resource and how full, in its tone", () => {
  expect(underPressure(workloads, 3).map(pressureRow)).toEqual([
    { tone: "down", label: "full-disk", value: "disk 91 %" },
    { tone: "warn", label: "busy", value: "memory 82 %" },
    { tone: "up", label: "quiet", value: "disk 30 %" },
  ]);
});

it("counts the guests running, templates left out", () => {
  expect(guestsRunning(workloads)).toBe("3 of 4 guests running");
  expect(guestsRunning([workloads[0]])).toBe("1 of 1 guest running");
});

const storage = (name: string, content: string[], usedGiB: number, totalGiB: number, extra = {}) =>
  create(StorageSchema, { name, content, active: true, avail: BigInt(totalGiB - usedGiB) * GiB, total: BigInt(totalGiB) * GiB, ...extra });

const node = (name: string, extra = {}) =>
  create(NodeSchema, {
    name,
    status: "online",
    cpu: 0.022,
    maxCpu: 16,
    mem: 11n * GiB,
    maxMem: 64n * GiB,
    storages: [
      storage("local", ["iso", "vztmpl", "backup"], 40, 100),
      storage("local-lvm", ["images", "rootdir"], 145, 476),
      storage("pbs", ["backup"], 2000, 4000, { shared: true }),
    ],
    ...extra,
  });

it("the node's meters are CPU, memory and the storage holding guest disks", () => {
  expect(nodeMeters([node("pve")])).toEqual([
    { resource: "cpu", label: "CPU", ratio: 0.022, value: "2.2 % of 16" },
    { resource: "memory", label: "Memory", ratio: 11 / 64, value: "11.0 GiB of 64.0 GiB" },
    { resource: "storage", label: "Storage", ratio: 145 / 476, value: "145 GiB of 476 GiB" },
  ]);
});

it("sums the online nodes, a shared storage once", () => {
  const shared = storage("ceph", ["images"], 100, 1000, { shared: true });
  const a = node("a", { storages: [shared, storage("local-lvm", ["images"], 10, 100)] });
  const b = node("b", { cpu: 0.5, storages: [shared, storage("local-lvm", ["rootdir"], 30, 100)] });
  const off = node("c", { status: "offline" });

  const [cpu, memory, disk] = nodeMeters([a, b, off]);
  expect(cpu.ratio).toBeCloseTo((0.022 + 0.5) / 2);
  expect(cpu.value).toBe("26.1 % of 32");
  expect(memory.value).toBe("22.0 GiB of 128 GiB");
  expect(disk.value).toBe("140 GiB of 1.2 TiB");
});

it("has no storage meter without a storage it may read, and no meters without a node", () => {
  expect(nodeMeters([node("pve", { storages: [] })]).map((m) => m.label)).toEqual(["CPU", "Memory"]);
  expect(nodeMeters([])).toEqual([]);
});

it("is up with the running count while nothing is tight; a stopped guest is no problem", () => {
  const meters = nodeMeters([node("pve")]);
  const rows = underPressure([workloads[0], workloads[3]]).map(pressureRow);

  expect(hostsStatus([workloads[0], workloads[3]], meters, rows)).toEqual({ tone: "up", summary: "1 of 2 guests running", fact: { value: 1, label: "guests running" } });
});

const meter = (resource: NodeMeter["resource"], r: number): NodeMeter => ({ resource, label: resource, ratio: r, value: "" });

it("a node's storage warns from 75 % and is down from 90 %", () => {
  expect([0.7499, 0.75, 0.8999, 0.9, 1].map((r) => nodeTone(meter("storage", r)))).toEqual(["up", "warn", "warn", "down", "down"]);
});

it("a node's memory and CPU warn only from 90 % and are never down", () => {
  for (const resource of ["memory", "cpu"] as const) {
    expect([0.75, 0.8999, 0.9, 1].map((r) => nodeTone(meter(resource, r)))).toEqual(["up", "up", "warn", "warn"]);
  }
});

it("guests warn from 75 % and are down from 90 %, naming what is tight; the node's memory at 78 % is no problem", () => {
  const meters = nodeMeters([node("pve", { mem: 50n * GiB })]);
  const rows = underPressure(workloads).map(pressureRow);

  expect(hostsStatus(workloads, meters, [])).toEqual({ tone: "up", summary: "3 of 4 guests running", fact: { value: 3, label: "guests running" } });
  expect(hostsStatus(workloads, meters, rows.slice(1))).toEqual({ tone: "warn", summary: "busy memory 82 %", fact: { value: 3, label: "guests running" } });
  expect(hostsStatus(workloads, meters, rows)).toEqual({
    tone: "down",
    summary: "full-disk disk 91 % · busy memory 82 %",
    fact: { value: 3, label: "guests running" },
  });
});

it("the node's memory warns from 90 % and its storage is down from 90 %, named in the summary", () => {
  const full = storage("local-lvm", ["images"], 440, 476);
  expect(hostsStatus([], nodeMeters([node("pve", { mem: 60n * GiB })]), [])).toMatchObject({ tone: "warn", summary: "Memory 94 %" });
  expect(hostsStatus([], nodeMeters([node("pve", { mem: 60n * GiB, storages: [full] })]), [])).toMatchObject({ tone: "down", summary: "Memory 94 % · Storage 92 %" });
});

it("names the node's tight meters as Concerns in nodeTone's tone, to the Proxmox Page", () => {
  const meters = { cpu: 0.95, mem: 60n * GiB, storages: [storage("local-lvm", ["images"], 380, 476)] };
  expect(nodeConcerns([node("pve", meters), node("off", { status: "offline" })])).toEqual([
    { id: "node-cpu", tone: "warn", subject: "pve", title: "pve's CPU is 95 % busy", cause: "CPU 95 % of 16 cores", icon: "cpu", href: "/proxmox" },
    { id: "node-memory", tone: "warn", subject: "pve", title: "pve's memory is 94 % full", cause: "memory 94 % · 60.0 GiB of 64.0 GiB", icon: "memory-stick", href: "/proxmox" },
    { id: "node-storage", tone: "warn", subject: "pve", title: "pve is running out of storage", cause: "storage 80 % · 380 GiB of 476 GiB", icon: "hard-drive", href: "/proxmox" },
  ]);
  expect(nodeConcerns([node("pve", { mem: 50n * GiB })])).toEqual([]);
  const ceph = storage("ceph", ["images"], 950, 1000, { shared: true });
  expect(nodeConcerns([node("a", { storages: [ceph] }), node("b", { storages: [ceph] })])).toMatchObject([
    { tone: "down", subject: "the nodes", title: "the nodes are running out of storage" },
  ]);
  expect(nodeConcerns([])).toEqual([]);
});

it("names each tight guest as a Concern, down from 90 %", () => {
  expect(guestConcerns(workloads).map((c) => [c.subject, c.tone, c.title])).toEqual([
    ["full-disk", "down", "full-disk is running out of disk"],
    ["busy", "warn", "busy is running out of memory"],
  ]);
});

const loudest = (cs: readonly Concern[]) => cs.reduce((t, c) => (c.tone === "down" || (c.tone === "warn" && t !== "down") ? c.tone : t), "none" as string);

it("whenever the Hosts Widget warns or is down, a Concern says so in at least that tone", () => {
  const guestSets = [[], [workloads[0]], [workloads[2]], workloads];
  const nodeSets = [
    [],
    [node("pve")],
    ...[0.5, 0.75, 0.89, 0.9, 0.99].flatMap((r) => [
      [node("pve", { cpu: r })],
      [node("pve", { mem: BigInt(Math.round(r * 64)) * GiB })],
      [node("pve", { storages: [storage("local-lvm", ["images"], Math.round(r * 476), 476)] })],
      [node("a", { cpu: r }), node("b", { storages: [storage("local-lvm", ["rootdir"], Math.round(r * 100), 100)] })],
    ]),
  ];
  const seen = new Set<string>();
  for (const ws of guestSets) {
    for (const nodes of nodeSets) {
      const status = hostsStatus(ws, nodeMeters(nodes), underPressure(ws).map(pressureRow));
      const loud = loudest([...nodeConcerns(nodes), ...guestConcerns(ws)]);
      seen.add(status.tone);
      if (status.tone === "down") expect(loud).toBe("down");
      if (status.tone === "warn") expect(["warn", "down"]).toContain(loud);
    }
  }
  expect([...seen].sort()).toEqual(["down", "up", "warn"]);
});
