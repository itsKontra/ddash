import { useQuery } from "@ddash/plugin";
import { AddressSource, ProxmoxApiService, type Workload } from "@ddash/proto/proxmox-api/proxmox_api_pb";
import { DataTable, EmptyState, Notice, PageTitle, Panel, Pill, type Column, type Tone } from "@ddash/ui";
import { Link } from "react-router";
import { bytes, kindLabel } from "@ddash/display";
import { cpu, uptime } from "./format";
import { PowerMenu } from "./power";

/** How each of Proxmox's statuses reads at a glance. */
export const tones: Record<string, Tone> = {
  running: "up",
  paused: "warn",
  stopped: "idle",
};

/** A Workload's status as a Pill in its tone: "running". */
export const StatusPill = ({ workload: w }: { workload: Workload }) => (
  <Pill tone={tones[w.status] ?? "idle"} dot>
    {w.status}
  </Pill>
);

/** Its CPU, memory, disk, network and uptime as the Page shows them: live while it runs, else its size. */
export function resources(w: Workload) {
  const running = w.status === "running";
  return {
    cpu: running ? cpu(w.cpu, w.maxCpu) : `${w.maxCpu} ${w.maxCpu === 1 ? "CPU" : "CPUs"}`,
    memory: running ? `${bytes(w.mem)} / ${bytes(w.maxMem)}` : bytes(w.maxMem),
    disk: w.disk ? `${bytes(w.disk)} / ${bytes(w.maxDisk)}` : bytes(w.maxDisk),
    network: running ? `${bytes(w.netIn)} in · ${bytes(w.netOut)} out` : "–",
    uptime: running ? uptime(w.uptimeSeconds) : "–",
  };
}

/** Its template mark and tags as Pills, or "–" without either. */
export function Tags({ workload: w }: { workload: Workload }) {
  if (!w.template && !w.tags.length) return <span className="text-muted-foreground">–</span>;
  return (
    <span className="inline-flex flex-wrap gap-1.5">
      {w.template && <Pill tone="info">template</Pill>}
      {w.tags.map((t) => (
        <Pill key={t}>{t}</Pill>
      ))}
    </span>
  );
}

/** Why proxmox-api gave no Workloads: not configured yet (with the way to configure it), or the failure. */
export function Unlisted({ error }: { error: { rawMessage: string } }) {
  if (error.rawMessage === "not configured") {
    return (
      <Panel>
        <EmptyState
          icon="server"
          title="proxmox-api isn't configured"
          action={
            <Link to="/plugins" className="text-sm font-semibold text-primary hover:underline">
              Configure it in Plugins
            </Link>
          }
        >
          Give it a Proxmox API token to list the LXCs and VMs.
        </EmptyState>
      </Panel>
    );
  }
  return (
    <Notice tone="down" title="proxmox-api can't list the Workloads">
      {error.rawMessage}
    </Notice>
  );
}

const columns: readonly Column<Workload>[] = [
  {
    id: "name",
    header: "Name",
    rowHeader: true,
    sortable: true,
    sortValue: (w) => w.name,
    cell: (w) => <Link to={`workloads/${w.vmid}`}>{w.name}</Link>,
  },
  { id: "status", header: "Status", sortable: true, sortValue: (w) => w.status, cell: (w) => <StatusPill workload={w} /> },
  { id: "id", header: "ID", sortable: true, sortValue: (w) => w.vmid, hideBelow: "sm", cell: (w) => <span className="tabular-nums">{w.vmid}</span> },
  { id: "kind", header: "Kind", cell: (w) => kindLabel(w.kind) },
  { id: "tags", header: "Tags", hideBelow: "sm", cell: (w) => <Tags workload={w} /> },
  { id: "cpu", header: "CPU", cell: (w) => <span className="tabular-nums">{resources(w).cpu}</span> },
  { id: "memory", header: "Memory", cell: (w) => <span className="tabular-nums">{resources(w).memory}</span> },
  { id: "disk", header: "Disk", hideBelow: "sm", cell: (w) => <span className="tabular-nums">{resources(w).disk}</span> },
  { id: "network", header: "Network", hideBelow: "sm", cell: (w) => <span className="tabular-nums">{resources(w).network}</span> },
  { id: "uptime", header: "Uptime", hideBelow: "sm", cell: (w) => <span className="tabular-nums">{resources(w).uptime}</span> },
  { id: "ips", header: "IPs", mono: true, cell: (w) => <Addresses workload={w} /> },
  { id: "power", header: <span className="sr-only">Power</span>, align: "right", cell: (w) => <PowerMenu workload={w} /> },
];

/** The proxmox Page: every LXC and VM with its status, resources and IPs, and a menu of its power Actions. */
export function Workloads() {
  const { data, error } = useQuery(ProxmoxApiService.method.listWorkloads, {});
  return (
    <div>
      <PageTitle title="Proxmox" subtitle="Every LXC and VM, with its status, resources and IPs." />
      {error && !data ? (
        <Unlisted error={error} />
      ) : (
        <Panel flush>
          <DataTable rows={data?.workloads ?? []} rowKey={(w) => String(w.vmid)} columns={columns} loading={!data} empty="Proxmox lists no LXCs or VMs" />
        </Panel>
      )}
    </div>
  );
}

/** Its IPs, or each NIC's MAC when it has none; marked when from the config. */
export function Addresses({ workload: w }: { workload: Workload }) {
  if (w.addressesError) return <span className="text-warn-text">{w.addressesError}</span>;
  const shown = w.nics.flatMap((n) => (n.ips.length ? n.ips : n.mac ? [n.mac] : []));
  if (!shown.length) return <span className="text-muted-foreground">–</span>;
  return (
    <div className="flex flex-col">
      {shown.map((a) => (
        <span key={a}>{a}</span>
      ))}
      {w.addresses === AddressSource.CONFIG && <span className="font-sans text-xs text-muted-foreground">from config</span>}
    </div>
  );
}
