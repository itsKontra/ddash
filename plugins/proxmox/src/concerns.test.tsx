import { cleanup } from "@testing-library/react";
import type { Concern } from "@ddash/plugin";
import { pending, renderScoped, settle, unavailable, type Answers } from "@ddash/plugin/testing";
import { afterEach, expect, it } from "vitest";
import plugin from "./index";
import { proxmox } from "./testing";

afterEach(cleanup);

let reported: Concern[] | undefined;

// Runs the proxmox Plugin's useConcerns as the Overview does.
function Probe() {
  reported = plugin.useConcerns!();
  return null;
}

async function show(answers: Answers) {
  renderScoped(proxmox, <Probe />, { answers });
  await settle();
}

const GiB = 1024 ** 3;
const guest = (vmid: number, name: string, mem: number, disk: number, extra: object = {}) => ({
  kind: "KIND_LXC",
  vmid,
  node: "pve",
  name,
  status: "running",
  mem: String(Math.round(mem * GiB)),
  maxMem: String(4 * GiB),
  disk: String(disk * GiB),
  maxDisk: String(32 * GiB),
  ...extra,
});
const listed = (...workloads: object[]) => () => Response.json({ workloads });

it("names each running guest whose memory or disk is 75 % full or more, with the fuller one's figures, down from 90 %", async () => {
  await show({
    ListWorkloads: listed(
      guest(101, "nginx", 1, 4),
      guest(102, "grafana", 1, 29),
      guest(103, "influxdb", 3.3, 4),
      guest(104, "stopped-full", 4, 32, { status: "stopped" }),
      guest(9000, "template", 4, 32, { template: true }),
      guest(105, "", 3, 4),
    ),
  });

  expect(reported).toEqual([
    {
      id: "102",
      tone: "down",
      subject: "grafana",
      title: "grafana is running out of disk",
      cause: "disk 91 % · 29.0 GiB of 32.0 GiB",
      icon: "hard-drive",
      href: "/proxmox/workloads/102",
    },
    {
      id: "103",
      tone: "warn",
      subject: "influxdb",
      title: "influxdb is running out of memory",
      cause: "memory 82 % · 3.3 GiB of 4.0 GiB",
      icon: "memory-stick",
      href: "/proxmox/workloads/103",
    },
    { id: "105", tone: "warn", subject: "105", title: "105 is running out of memory", cause: "memory 75 % · 3.0 GiB of 4.0 GiB", icon: "memory-stick", href: "/proxmox/workloads/105" },
  ]);
});

it("reports none while no guest is tight, nothing until proxmox-api answered, and none when it can't be read", async () => {
  await show({ ListWorkloads: listed(guest(101, "nginx", 1, 4)) });
  expect(reported).toEqual([]);

  cleanup();
  await show({ ListWorkloads: () => pending() });
  expect(reported).toBeUndefined();

  cleanup();
  await show({ ListWorkloads: () => unavailable("Proxmox is restarting"), ListNodes: () => unavailable("Proxmox is restarting") });
  expect(reported).toEqual([]);
});

const node = (memGiB: number, usedGiB: number) => ({
  name: "pve",
  status: "online",
  cpu: 0.05,
  maxCpu: 16,
  mem: String(memGiB * GiB),
  maxMem: String(64 * GiB),
  storages: [{ name: "local-lvm", content: ["images", "rootdir"], active: true, total: String(476 * GiB), avail: String((476 - usedGiB) * GiB) }],
});
const nodes = (memGiB: number, usedGiB: number) => () => Response.json({ nodes: [node(memGiB, usedGiB)] });

it("names the node when its storage is 75 % full or its memory 90 %, first, without naming its guests twice", async () => {
  await show({ ListWorkloads: listed(guest(102, "grafana", 1, 29)), ListNodes: nodes(60, 440) });

  expect(reported).toEqual([
    { id: "node-memory", tone: "warn", subject: "pve", title: "pve's memory is 94 % full", cause: "memory 94 % · 60.0 GiB of 64.0 GiB", icon: "memory-stick", href: "/proxmox" },
    { id: "node-storage", tone: "down", subject: "pve", title: "pve is running out of storage", cause: "storage 92 % · 440 GiB of 476 GiB", icon: "hard-drive", href: "/proxmox" },
    expect.objectContaining({ id: "102", subject: "grafana" }),
  ]);
});

it("leaves the node's memory at 78 % unnamed, waits for the nodes, and names the guests when the nodes can't be read", async () => {
  await show({ ListWorkloads: listed(guest(101, "nginx", 1, 4)), ListNodes: nodes(50, 100) });
  expect(reported).toEqual([]);

  cleanup();
  await show({ ListWorkloads: listed(guest(102, "grafana", 1, 29)), ListNodes: () => pending() });
  expect(reported).toBeUndefined();

  cleanup();
  await show({ ListWorkloads: listed(guest(102, "grafana", 1, 29)), ListNodes: () => unavailable("no Sys.Audit") });
  expect(reported).toEqual([expect.objectContaining({ id: "102" })]);
});
