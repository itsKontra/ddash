import { checkStatus, type CheckStatus } from "@ddash/display";
import { useQuery } from "@ddash/plugin";
import type { App } from "@ddash/proto/apps/apps_pb";
import { State, type Check } from "@ddash/proto/checks/checks_pb";
import { ComposeService } from "@ddash/proto/compose/compose_pb";
import { Kind } from "@ddash/proto/ddash/workload/workload_pb";
import { ProxmoxApiService } from "@ddash/proto/proxmox-api/proxmox_api_pb";
import { workloadCause, type WorkloadCause } from "./cause";

/** Whether c is an App's Check that is probed and down. */
export const isDown = (c: Check | undefined) => !!c && !c.disabled && c.state === State.DOWN;

/** Whether ref names a Proxmox guest. */
const isGuest = (kind: Kind | undefined) => kind === Kind.LXC || kind === Kind.VM;

/**
 * The cause of each down App whose Workload isn't running, by App id, and
 * whether every read it takes was answered. compose and proxmox-api are
 * read only while a down App runs on a container or a guest.
 */
export function useDownCauses(apps: readonly App[] | undefined, byApp: ReadonlyMap<string, Check> | undefined) {
  const down = (apps ?? []).filter((a) => isDown(byApp?.get(a.id)));
  const containers = down.some((a) => a.workload?.ref?.kind === Kind.DOCKER);
  const guests = down.some((a) => isGuest(a.workload?.ref?.kind));
  const compose = useQuery(ComposeService.method.listProjects, {}, { enabled: containers });
  const workloads = useQuery(ProxmoxApiService.method.listWorkloads, {}, { enabled: guests });
  const permissions = useQuery(ProxmoxApiService.method.listPermissions, {}, { enabled: guests });
  const answered = (q: { data?: unknown; error: unknown }) => q.data !== undefined || !!q.error;
  const known = (!containers || answered(compose)) && (!guests || (answered(workloads) && answered(permissions)));
  const reads = { compose: compose.data?.hosts, guests: workloads.data?.workloads, permissions: permissions.data?.workloads };
  const causes = new Map<string, WorkloadCause>();
  for (const a of down) {
    const cause = workloadCause(a.workload, reads);
    if (cause) causes.set(a.id, cause);
  }
  return { causes, known };
}

/** An App's Check status with its Workload's cause, when down because of it, in place of the Check's reason. */
export function causedStatus(c: Check | undefined, cause: WorkloadCause | undefined): CheckStatus {
  const s = checkStatus(c);
  return s.tone === "down" && cause ? { ...s, label: `${c?.flapping ? "down, flapping" : "down"}: ${cause.text}` } : s;
}
