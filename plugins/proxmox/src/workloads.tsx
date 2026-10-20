import { useQuery } from "@ddash/plugin";
import { AddressSource, ProxmoxApiService, type Workload } from "@ddash/proto/proxmox-api/proxmox_api_pb";
import { Badge, PageHeader, StatusDot, type Tone } from "@ddash/ui";
import { bytes, cpu, kindLabel, uptime } from "./format";

/** How each of Proxmox's statuses reads at a glance. */
const tones: Record<string, Tone> = {
  running: "up",
  paused: "warn",
  stopped: "idle",
};

/** The proxmox Page: every LXC and VM with its status, resources and IPs. */
export function Workloads() {
  const { data, error } = useQuery(ProxmoxApiService.method.listWorkloads, {});
  return (
    <div>
      <PageHeader title="Proxmox" subtitle="Every LXC and VM, with its status, resources and IPs." />
      {error && !data && <p className="text-sm text-down">{error.rawMessage}</p>}
      {data && (
        <div className="overflow-x-auto rounded-ui border border-border bg-card">
          <table className="w-full text-sm">
            <thead className="text-left text-muted-foreground">
              <tr className="border-b border-border">
                <th scope="col" className="w-6 py-2 pl-4">
                  <span className="sr-only">Status</span>
                </th>
                <th scope="col" className="px-2 py-2 font-medium">
                  ID
                </th>
                <th scope="col" className="px-2 py-2 font-medium">
                  Name
                </th>
                <th scope="col" className="px-2 py-2 font-medium">
                  Kind
                </th>
                <th scope="col" className="px-2 py-2 font-medium">
                  CPU
                </th>
                <th scope="col" className="px-2 py-2 font-medium">
                  Memory
                </th>
                <th scope="col" className="px-2 py-2 font-medium">
                  Disk
                </th>
                <th scope="col" className="px-2 py-2 font-medium">
                  Network
                </th>
                <th scope="col" className="px-2 py-2 font-medium">
                  Uptime
                </th>
                <th scope="col" className="px-2 py-2 pr-4 font-medium">
                  IPs
                </th>
              </tr>
            </thead>
            <tbody>
              {data.workloads.map((w) => (
                <Row key={w.vmid} workload={w} />
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}

function Row({ workload: w }: { workload: Workload }) {
  const running = w.status === "running";
  return (
    <tr className="border-b border-border align-top last:border-b-0">
      <td className="py-2 pl-4">
        <StatusDot tone={tones[w.status] ?? "idle"} label={w.status} />
      </td>
      <td className="px-2 py-2 text-muted-foreground tabular-nums">{w.vmid}</td>
      <th scope="row" className="px-2 py-2 text-left font-medium">
        <div className="flex flex-wrap items-center gap-1.5">
          <span>{w.name}</span>
          {w.template && <Badge>template</Badge>}
          {w.tags.map((t) => (
            <Badge key={t}>{t}</Badge>
          ))}
        </div>
      </th>
      <td className="px-2 py-2 text-muted-foreground">{kindLabel(w.kind)}</td>
      <td className="px-2 py-2 text-muted-foreground tabular-nums">{running ? cpu(w.cpu, w.maxCpu) : `${w.maxCpu} ${w.maxCpu === 1 ? "CPU" : "CPUs"}`}</td>
      <td className="px-2 py-2 text-muted-foreground tabular-nums">{running ? `${bytes(w.mem)} / ${bytes(w.maxMem)}` : bytes(w.maxMem)}</td>
      <td className="px-2 py-2 text-muted-foreground tabular-nums">{w.disk ? `${bytes(w.disk)} / ${bytes(w.maxDisk)}` : bytes(w.maxDisk)}</td>
      <td className="px-2 py-2 text-muted-foreground tabular-nums">{running ? `${bytes(w.netIn)} in · ${bytes(w.netOut)} out` : "–"}</td>
      <td className="px-2 py-2 text-muted-foreground tabular-nums">{running ? uptime(w.uptimeSeconds) : "–"}</td>
      <td className="px-2 py-2 pr-4">
        <Addresses workload={w} />
      </td>
    </tr>
  );
}

/** Its IPs, or each NIC's MAC when it has none; marked when from the config. */
function Addresses({ workload: w }: { workload: Workload }) {
  if (w.addressesError) return <span className="text-warn">{w.addressesError}</span>;
  const shown = w.nics.flatMap((n) => (n.ips.length ? n.ips : n.mac ? [n.mac] : []));
  if (!shown.length) return <span className="text-muted-foreground">–</span>;
  return (
    <div className="flex flex-col">
      {shown.map((a) => (
        <span key={a} className="tabular-nums">
          {a}
        </span>
      ))}
      {w.addresses === AddressSource.CONFIG && <span className="text-xs text-muted-foreground">from config</span>}
    </div>
  );
}
