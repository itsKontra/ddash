import { useQuery, type Status, type Widget, type WidgetSize } from "@ddash/plugin";
import { Kind, ProxmoxApiService, type Workload } from "@ddash/proto/proxmox-api/proxmox_api_pb";
import { Stat } from "@ddash/ui";

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
  const { workloads } = useWorkloads();
  if (!workloads) return null;
  const kinds = [
    ["LXCs", workloads.filter((w) => w.kind === Kind.LXC)],
    ["VMs", workloads.filter((w) => w.kind === Kind.VM)],
  ] as const;
  return (
    <div className="flex flex-col gap-3">
      <div className="flex gap-6">
        <Stat label="running" value={running(workloads)} tone="up" />
        <Stat label="guests" value={workloads.length} />
      </div>
      {size === "M" && (
        <table className="w-full text-sm">
          <tbody>
            {kinds.map(([label, ws]) => (
              <tr key={label}>
                <th scope="row" className="py-0.5 text-left font-medium">
                  {label}
                </th>
                <td className="py-0.5 text-right text-muted-foreground tabular-nums">
                  {running(ws)}/{ws.length} running
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </div>
  );
}

/** The Overview Widget: how many LXCs and VMs run. */
export const guests: Widget = {
  id: "guests",
  title: "Guests",
  sizes: ["S", "M"],
  defaultSize: "S",
  Body,
  useStatus,
};
