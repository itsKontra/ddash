// What the Hosts Widget shows of the nodes and guests: plain functions over
// proxmox-api's answers.
import { bytes } from "@ddash/display";
import type { Concern, Status } from "@ddash/plugin";
import type { Node, Workload } from "@ddash/proto/proxmox-api/proxmox_api_pb";
import { meterTone, type SummaryMeter, type SummaryRow, type Tone } from "@ddash/ui";

/** How full a guest is: the fuller of its memory and its disk. */
export interface Pressure {
  workload: Workload;
  resource: "memory" | "disk";
  /** 0 to 1 (or over, should Proxmox report it). */
  ratio: number;
}

const ratio = (used: bigint, max: bigint) => (max > 0n ? Number(used) / Number(max) : 0);

/** A running guest's pressure: its memory or its disk, whichever is fuller. */
export function pressure(w: Workload): Pressure {
  const memory = ratio(w.mem, w.maxMem);
  const disk = ratio(w.disk, w.maxDisk);
  return disk > memory ? { workload: w, resource: "disk", ratio: disk } : { workload: w, resource: "memory", ratio: memory };
}

/** The running guests (templates never run), fullest first. */
function byPressure(workloads: readonly Workload[]): Pressure[] {
  return workloads
    .filter((w) => w.status === "running" && !w.template)
    .map(pressure)
    .sort((a, b) => b.ratio - a.ratio);
}

/** The n running guests under the most pressure, fullest first. */
export function underPressure(workloads: readonly Workload[], n = 2): Pressure[] {
  return byPressure(workloads).slice(0, n);
}

/**
 * The running guests whose memory or disk is at threshold (0 to 1) or
 * fuller, fullest first: the ones getting tight.
 */
export function tightGuests(workloads: readonly Workload[], threshold = 0.75): Pressure[] {
  return byPressure(workloads).filter((p) => p.ratio >= threshold);
}

/** A ratio as a whole percentage: "82 %". */
export const percent = (r: number) => `${Math.round(r * 100)} %`;

/** A Pressure as a Summary's row: the guest, which resource and how full, in its tone. */
export const pressureRow = (p: Pressure): SummaryRow => ({
  tone: meterTone(p.ratio),
  label: p.workload.name || `${p.workload.vmid}`,
  value: `${p.resource} ${percent(p.ratio)}`,
});

/** A node's meter, with the resource it measures. */
export interface NodeMeter extends SummaryMeter {
  resource: "cpu" | "memory" | "storage";
}

/** True for a storage that holds guest disks: an LXC's root or a VM's images. */
const holdsGuests = (content: readonly string[]) => content.includes("images") || content.includes("rootdir");

const onlineNodes = (nodes: readonly Node[]) => nodes.filter((n) => n.status === "online" && n.maxCpu > 0);

/**
 * The nodes' CPU, memory and storage as meters, summed over every online
 * node. Storage is the active storages that hold guest disks, a shared one
 * counted once. A node with no storage the token may audit has no storage
 * meter; no node, no meters.
 */
export function nodeMeters(nodes: readonly Node[]): NodeMeter[] {
  const online = onlineNodes(nodes);
  if (online.length === 0) return [];
  const cores = online.reduce((s, n) => s + n.maxCpu, 0);
  const cpu = online.reduce((s, n) => s + n.cpu * n.maxCpu, 0) / cores;
  const mem = online.reduce((s, n) => s + n.mem, 0n);
  const maxMem = online.reduce((s, n) => s + n.maxMem, 0n);
  const storages = new Map<string, { used: bigint; total: bigint }>();
  for (const n of online) {
    for (const s of n.storages) {
      if (!s.active || !holdsGuests(s.content) || s.total === 0n) continue;
      storages.set(s.shared ? s.name : `${n.name}/${s.name}`, { used: s.total - s.avail, total: s.total });
    }
  }
  const used = [...storages.values()].reduce((s, x) => s + x.used, 0n);
  const total = [...storages.values()].reduce((s, x) => s + x.total, 0n);
  const meters: NodeMeter[] = [
    { resource: "cpu", label: "CPU", ratio: cpu, value: `${(cpu * 100).toFixed(1)} % of ${cores}` },
    { resource: "memory", label: "Memory", ratio: ratio(mem, maxMem), value: `${bytes(mem)} of ${bytes(maxMem)}` },
  ];
  if (total > 0n) meters.push({ resource: "storage", label: "Storage", ratio: ratio(used, total), value: `${bytes(used)} of ${bytes(total)}` });
  return meters;
}

/**
 * What a node's meter does to the Hosts Widget's status; the meter's own
 * colour stays meterTone. Storage only fills up: warn from 75 %, down from
 * 90 %. A hypervisor's memory sits at 75 to 90 % as a matter of course
 * (ZFS's ARC takes what is free, the guests keep what they were given)
 * and its CPU runs in bursts, so those two warn only from 90 % and are
 * never down: a guest short of memory is named on its own.
 */
export function nodeTone(meter: NodeMeter): Tone {
  if (meter.resource === "storage") return meterTone(meter.ratio);
  return meter.ratio >= 0.9 ? "warn" : "up";
}

/**
 * The nodes' meters that nodeTone makes warn or down, as Concerns in that
 * tone, with the figures, to the Proxmox Page: "pve is running out of
 * storage", "pve's memory is 93 % full", "pve's CPU is 95 % busy". With
 * more than one node online the meters are summed, and so is the subject:
 * "the nodes".
 */
export function nodeConcerns(nodes: readonly Node[]): Concern[] {
  const online = onlineNodes(nodes);
  const one = online.length === 1;
  const subject = one ? online[0].name : "the nodes";
  const own = one ? `${subject}'s` : "the nodes'";
  const cores = online.reduce((s, n) => s + n.maxCpu, 0);
  return nodeMeters(nodes).flatMap((m): Concern[] => {
    const tone = nodeTone(m);
    if (tone !== "warn" && tone !== "down") return [];
    const full = percent(m.ratio);
    const [title, cause, icon] = {
      storage: [`${subject} ${one ? "is" : "are"} running out of storage`, `storage ${full} · ${m.value}`, "hard-drive"],
      memory: [`${own} memory is ${full} full`, `memory ${full} · ${m.value}`, "memory-stick"],
      cpu: [`${own} CPU is ${full} busy`, `CPU ${full} of ${cores} ${cores === 1 ? "core" : "cores"}`, "cpu"],
    }[m.resource] as [string, string, "hard-drive" | "memory-stick" | "cpu"];
    return [{ id: `node-${m.resource}`, tone, subject, title, cause, icon, href: "/proxmox" }];
  });
}

/**
 * Each running guest whose memory or disk is 75 % full or more as a
 * Concern, in its pressure row's tone (down from 90 %), with the fuller
 * one's figures and a link to the guest.
 */
export function guestConcerns(workloads: readonly Workload[]): Concern[] {
  return tightGuests(workloads).map(({ workload: w, resource, ratio }): Concern => {
    const name = w.name || String(w.vmid);
    const [used, max] = resource === "disk" ? [w.disk, w.maxDisk] : [w.mem, w.maxMem];
    return {
      id: String(w.vmid),
      tone: ratio >= 0.9 ? "down" : "warn",
      subject: name,
      title: `${name} is running out of ${resource}`,
      cause: `${resource} ${percent(ratio)} · ${bytes(used)} of ${bytes(max)}`,
      icon: resource === "disk" ? "hard-drive" : "memory-stick",
      href: `/proxmox/workloads/${w.vmid}`,
    };
  });
}

const rank: Record<Tone, number> = { down: 3, warn: 2, up: 1, maint: 0, info: 0, idle: 0 };

/**
 * The Hosts Widget's status: the worst tone it shows, a node's meter by
 * nodeTone or a named guest at 75 % or more (warn) or 90 % or more (down),
 * each named in the summary; up with the running count otherwise. A
 * stopped guest is no problem: guests are off on purpose. Its fact: the
 * guests running.
 */
export function hostsStatus(workloads: readonly Workload[], meters: readonly NodeMeter[], rows: readonly SummaryRow[]): Status {
  const tight = [
    ...meters.filter((m) => nodeTone(m) !== "up").map((m) => ({ tone: nodeTone(m), text: `${m.label} ${percent(m.ratio)}` })),
    ...rows.filter((r) => r.tone !== "up").map((r) => ({ tone: r.tone, text: `${r.label} ${r.value}` })),
  ];
  const fact = { value: workloads.filter((w) => !w.template && w.status === "running").length, label: "guests running" };
  if (tight.length === 0) return { tone: "up", summary: guestsRunning(workloads), fact };
  const tone = tight.reduce<Tone>((t, x) => (rank[x.tone] > rank[t] ? x.tone : t), "up");
  return { tone, summary: tight.map((x) => x.text).join(" · "), fact };
}

/** "12 of 17 guests running", templates left out. */
export function guestsRunning(workloads: readonly Workload[]): string {
  const guests = workloads.filter((w) => !w.template);
  const running = guests.filter((w) => w.status === "running").length;
  return `${running} of ${guests.length} ${guests.length === 1 ? "guest" : "guests"} running`;
}
