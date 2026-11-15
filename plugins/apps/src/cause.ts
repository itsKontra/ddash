import { kindLabel } from "@ddash/display";
import type { Workload } from "@ddash/proto/apps/apps_pb";
import type { Host, Project, ProjectContainer } from "@ddash/proto/compose/compose_pb";
import { State } from "@ddash/proto/ddash/container/container_pb";
import { Kind } from "@ddash/proto/ddash/workload/workload_pb";
import type { Workload as Guest, WorkloadPermissions } from "@ddash/proto/proxmox-api/proxmox_api_pb";

/** The privilege ddash needs on a guest to start it; without it the guest is Protected. */
const powerMgmt = "VM.PowerMgmt";

/**
 * What fixes a Workload that isn't running, as its owning Plugin's Page
 * runs it: Start on the guest's Proxmox detail, or the Compose Project's
 * Restart through its Plan.
 */
export type WorkloadFix = { kind: "guest"; label: string; vmid: string } | { kind: "project"; host: string; project: string };

/** Why an App is down when its Workload isn't running, and what fixes it. */
export interface WorkloadCause {
  /** "LXC 206 n8n is stopped", "Compose Project paperless exited (137)". */
  text: string;
  /** None for a Protected Workload, one busy with an Action, or a state its Page has no fix for. */
  fix?: WorkloadFix;
}

/** What the Workloads' own Components say now, fresher than apps' last sync; each undefined while unknown. */
export interface WorkloadReads {
  /** compose's Docker hosts with a Docker Agent. */
  compose?: readonly Host[];
  /** proxmox-api's guests. */
  guests?: readonly Guest[];
  /** ddash's privileges on each guest. */
  permissions?: readonly WorkloadPermissions[];
}

/** Docker's exit code in a container's status: "137" for "Exited (137) 2 hours ago". */
export const exitCode = (status: string) => /^Exited \((\d+)\)/.exec(status)?.[1];

/** The Compose Project that container name on host belongs to, with the container. */
export function composeOf(hosts: readonly Host[] | undefined, host: string, name: string): { project: Project; container: ProjectContainer } | undefined {
  for (const project of hosts?.find((h) => h.name === host)?.projects ?? []) {
    for (const s of project.services) {
      const container = s.containers.find((c) => c.container?.name === name);
      if (container) return { project, container };
    }
  }
  return undefined;
}

/** Docker's state words, as apps writes a container's state. */
const words: Record<State, string> = {
  [State.UNSPECIFIED]: "unknown",
  [State.CREATED]: "created",
  [State.RUNNING]: "running",
  [State.PAUSED]: "paused",
  [State.RESTARTING]: "restarting",
  [State.REMOVING]: "removing",
  [State.EXITED]: "exited",
  [State.DEAD]: "dead",
};

/**
 * Why w isn't running, as its down App's cause: undefined while it runs,
 * for none, an external host, or a state not known. The guest's or
 * container's own Component is read first, apps' last sync after. A
 * stopped guest is fixed by Start unless Protected (ddash lacks
 * VM.PowerMgmt on it, or that isn't known yet); an exited or dead
 * container of a Compose Project by Restart unless the Project is
 * Protected or runs an Action.
 */
export function workloadCause(w: Workload | undefined, reads: WorkloadReads): WorkloadCause | undefined {
  const ref = w?.ref;
  if (!w || !ref) return undefined;
  switch (ref.kind) {
    case Kind.LXC:
    case Kind.VM: {
      // VMIDs are unique in a cluster; the node changes on a migration.
      const guest = reads.guests?.find((g) => String(g.vmid) === ref.id);
      const state = guest?.status ?? w.state;
      if (!state || state === "running") return undefined;
      const kind = kindLabel(ref.kind);
      const text = `${[kind, ref.id, guest?.name || w.name].filter(Boolean).join(" ")} is ${state}`;
      const privileges = reads.permissions?.find((p) => String(p.vmid) === ref.id)?.privileges;
      const fix = state === "stopped" && !!privileges?.includes(powerMgmt) && !guest?.template;
      return fix ? { text, fix: { kind: "guest", label: `Start ${kind}`, vmid: ref.id } } : { text };
    }
    case Kind.DOCKER: {
      const found = composeOf(reads.compose, ref.host, ref.id);
      const c = found?.container.container;
      const state = c ? words[c.state] : w.state;
      if (!state || state === "running") return undefined;
      const what = found ? `Compose Project ${found.project.name}` : `Container ${w.name || ref.id}`;
      const code = c && exitCode(c.status);
      const text = state === "exited" ? `${what} exited${code ? ` (${code})` : ""}` : `${what} is ${state}`;
      const p = found?.project;
      const fix = !!p && (state === "exited" || state === "dead") && !p.protected && !p.runId;
      return fix ? { text, fix: { kind: "project", host: p.host, project: p.name } } : { text };
    }
    default:
      return undefined;
  }
}
