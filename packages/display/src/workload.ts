import type { Workload } from "@ddash/proto/apps/apps_pb";
import { Kind, type WorkloadRef } from "@ddash/proto/ddash/workload/workload_pb";
import type { Kind as GuestKind } from "@ddash/proto/proxmox-api/proxmox_api_pb";

const labels: Record<Kind, string> = {
  [Kind.UNSPECIFIED]: "Workload",
  [Kind.LXC]: "LXC",
  [Kind.VM]: "VM",
  [Kind.DOCKER]: "Docker",
  [Kind.EXTERNAL]: "External",
};

/**
 * A Workload kind as the Operator reads it: "LXC", "VM", "Docker",
 * "External". proxmox-api's guest Kind numbers LXC and VM as WorkloadRef's
 * Kind does, so it reads the same.
 */
export const kindLabel = (kind: Kind | GuestKind): string => labels[kind as Kind] ?? labels[Kind.UNSPECIFIED];

/** "LXC jellyfin": kind and name, else the id or host the reference holds. */
export function workloadName(w: Workload): string {
  const ref = w.ref;
  return `${kindLabel(ref?.kind ?? Kind.UNSPECIFIED)} ${w.name || ref?.id || ref?.host || ""}`.trim();
}

/** "LXC jellyfin · running". */
export function workloadText(w: Workload): string {
  return w.state ? `${workloadName(w)} · ${w.state}` : workloadName(w);
}

/**
 * The Plugin Page that shows a Workload, for usePluginLink: an LXC's or
 * VM's detail on the proxmox Page, the docker Page's index; undefined for
 * an external host.
 */
export function workloadPage(ref: WorkloadRef | undefined): { plugin: string; path: string } | undefined {
  switch (ref?.kind) {
    case Kind.LXC:
    case Kind.VM:
      return { plugin: "proxmox", path: `workloads/${ref.id}` };
    case Kind.DOCKER:
      return { plugin: "docker", path: "" };
    default:
      return undefined;
  }
}
