import { workloadName, workloadPage, workloadText } from "@ddash/display";
import { usePluginLink, useQuery } from "@ddash/plugin";
import { AppsService, type Workload } from "@ddash/proto/apps/apps_pb";
import { Kind, type WorkloadRef } from "@ddash/proto/ddash/workload/workload_pb";

/** A card's Workload line, linked to its Proxmox detail or the Docker Page when that Plugin is installed. */
export function WorkloadLine({ workload }: { workload: Workload }) {
  const page = workloadPage(workload.ref);
  // Hooks run unconditionally: "" is never an installed Plugin.
  const href = usePluginLink(page?.plugin ?? "", page?.path);
  const text = workloadText(workload);
  return (
    <p aria-label="Workload" className="truncate text-xs text-muted-foreground">
      {href && page ? (
        <a href={href} className="hover:text-foreground hover:underline">
          {text}
        </a>
      ) : (
        text
      )}
    </p>
  );
}

/** A WorkloadRef as an option value: kind|host|id. */
export const refKey = (r?: WorkloadRef) => (r ? `${r.kind}|${r.host}|${r.id}` : "");

/** Where a Workload lives: "media" for a container, "pve/102" for a guest. */
const where = (r?: WorkloadRef) => (r?.kind === Kind.DOCKER ? r.host : `${r?.host}/${r?.id}`);

/** ListWorkloads: what an override can pick. */
export function useWorkloads() {
  const { data, error } = useQuery(AppsService.method.listWorkloads, {});
  return { workloads: data?.workloads, error };
}

/** The value of the "External host…" option. */
export const external = "external";

/** The WorkloadRef an option value names; undefined for the match. */
export function refOf(key: string): WorkloadRef | undefined {
  if (!key) return undefined;
  const [kind, host, id] = key.split("|");
  return { kind: Number(kind), host, id } as WorkloadRef;
}

/**
 * The options of the Workload override select: the match (labelled
 * matchLabel), every Workload listed now, the current override when none
 * lists it, and an external host.
 */
export function WorkloadOptions({ matchLabel, current, listed }: { matchLabel: string; current?: WorkloadRef; listed: readonly Workload[] }) {
  const lost = current && current.kind !== Kind.EXTERNAL && !listed.some((w) => refKey(w.ref) === refKey(current));
  return (
    <>
      <option value="">{matchLabel}</option>
      {listed.map((w) => (
        <option key={refKey(w.ref)} value={refKey(w.ref)}>
          {`${workloadName(w)} (${where(w.ref)})`}
        </option>
      ))}
      {lost && <option value={refKey(current)}>{`${workloadName({ ref: current } as Workload)} (${where(current)}, not listed now)`}</option>}
      <option value={external}>External host…</option>
    </>
  );
}
