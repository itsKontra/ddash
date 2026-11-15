import { useQuery, type Status, type Widget, type WidgetSize } from "@ddash/plugin";
import { Kind, ProxmoxApiService, type Workload } from "@ddash/proto/proxmox-api/proxmox_api_pb";
import { Facts, MetricTile, Skeleton, Summary } from "@ddash/ui";
import { guestsRunning, hostsStatus, nodeMeters, pressureRow, underPressure } from "./hosts";

const running = (ws: readonly Workload[]) => ws.filter((w) => w.status === "running").length;
const plural = (n: number, one: string) => `${n} ${one}${n === 1 ? "" : "s"}`;

/** Every Workload but templates, which never run. */
function useWorkloads() {
  const q = useQuery(ProxmoxApiService.method.listWorkloads, {});
  return { ...q, workloads: q.data?.workloads.filter((w) => !w.template) };
}

/** Up, with the counts; warn while proxmox-api is unavailable. */
function useStatus(): Status | undefined {
  const { workloads, error } = useWorkloads();
  if (error) return { tone: "warn", summary: `proxmox-api unavailable: ${error.rawMessage}` };
  if (!workloads) return undefined;
  const lxcs = workloads.filter((w) => w.kind === Kind.LXC).length;
  const vms = workloads.filter((w) => w.kind === Kind.VM).length;
  return { tone: "up", summary: `${running(workloads)} of ${workloads.length} running · ${plural(lxcs, "LXC")}, ${plural(vms, "VM")}` };
}

/** S: the running and total counts. M: each kind's counts too. */
function Body({ size }: { size: WidgetSize }) {
  const { workloads, error } = useWorkloads();
  // Unavailable: the Widget's status says why.
  if (error && !workloads) return null;
  const kinds = [
    ["LXCs", Kind.LXC],
    ["VMs", Kind.VM],
  ] as const;
  return (
    <div className="flex flex-col gap-3">
      <div className="grid grid-cols-2 gap-3">
        <MetricTile label="running" value={workloads && running(workloads)} tone="up" />
        <MetricTile label="guests" value={workloads?.length} />
      </div>
      {size === "M" && workloads && (
        <Facts
          items={kinds.map(([label, kind]) => {
            const ws = workloads.filter((w) => w.kind === kind);
            return { label, value: `${running(ws)}/${ws.length} running` };
          })}
        />
      )}
    </div>
  );
}

/** The nodes' meters, the two guests under most pressure and what that makes the status; undefined until proxmox-api first answered. */
function useHosts() {
  const { workloads, error } = useWorkloads();
  const nodes = useQuery(ProxmoxApiService.method.listNodes, {});
  if (!workloads || (!nodes.data && !nodes.error)) return { error };
  const meters = nodeMeters(nodes.data?.nodes ?? []);
  const rows = underPressure(workloads).map(pressureRow);
  return { error, workloads, meters, rows, status: hostsStatus(workloads, meters, rows), nodesError: nodes.error };
}

/** Warn while proxmox-api is unavailable; else hostsStatus. */
function useHostsStatus(): Status | undefined {
  const { error, status } = useHosts();
  if (error) return { tone: "warn", summary: `proxmox-api unavailable: ${error.rawMessage}` };
  return status;
}

/** The guests running, the nodes' CPU, memory and storage, and the two guests under most pressure. */
function HostsBody() {
  const { workloads, meters, rows, nodesError } = useHosts();
  if (!workloads) return <Skeleton lines={4} />;
  // No node to read: the token lacks Sys.Audit on /nodes, or the read failed.
  const line = meters.length ? undefined : [{ text: nodesError ? `node unreadable: ${nodesError.rawMessage}` : "the API token can't read the node (Sys.Audit on /nodes)" }];
  return <Summary headline={guestsRunning(workloads)} line={line} meters={meters} rows={rows} />;
}

/**
 * Is the server all right? The node and its guests; the containers are
 * the docker Plugin's Docker hosts Widget.
 */
export const hosts: Widget = {
  id: "hosts",
  title: "Hosts",
  sizes: ["S", "M"],
  defaultSize: "S",
  Body: HostsBody,
  useStatus: useHostsStatus,
};

/** The Overview Widget: how many LXCs and VMs run. */
export const guests: Widget = {
  id: "guests",
  title: "Guests",
  sizes: ["S", "M"],
  defaultSize: "S",
  Body,
  useStatus,
};
