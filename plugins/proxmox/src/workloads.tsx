import { useQuery } from "@ddash/plugin";
import { AddressSource, Kind, ProxmoxApiService, type Workload } from "@ddash/proto/proxmox-api/proxmox_api_pb";
import { ProxmoxService } from "@ddash/proto/proxmox/proxmox_pb";
import { DataTable, EmptyState, matches, noMatch, Notice, PageTitle, Panel, Pill, SearchField, Select, Tabs, useQueryState, type Column, type Tone } from "@ddash/ui";
import { Link } from "react-router";
import { bytes, kindLabel } from "@ddash/display";
import { cpu, uptime } from "./format";
import { PowerStrip, taskOf } from "./power";

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
  { id: "kind", header: "Kind", sortable: true, sortValue: (w) => kindLabel(w.kind), cell: (w) => kindLabel(w.kind) },
  { id: "tags", header: "Tags", hideBelow: "sm", cell: (w) => <Tags workload={w} /> },
  // The resources sort by what's in use (CPU, memory and uptime: none while stopped), not by how the cell reads.
  { id: "cpu", header: "CPU", sortable: true, sortValue: (w) => live(w, w.cpu), cell: (w) => <span className="tabular-nums">{resources(w).cpu}</span> },
  {
    id: "memory",
    header: "Memory",
    sortable: true,
    sortValue: (w) => live(w, Number(w.mem)),
    cell: (w) => <span className="tabular-nums">{resources(w).memory}</span>,
  },
  {
    id: "disk",
    header: "Disk",
    hideBelow: "sm",
    sortable: true,
    sortValue: (w) => Number(w.disk),
    cell: (w) => <span className="tabular-nums">{resources(w).disk}</span>,
  },
  { id: "network", header: "Network", hideBelow: "sm", cell: (w) => <span className="tabular-nums">{resources(w).network}</span> },
  {
    id: "uptime",
    header: "Uptime",
    hideBelow: "sm",
    sortable: true,
    sortValue: (w) => live(w, Number(w.uptimeSeconds)),
    cell: (w) => <span className="tabular-nums">{resources(w).uptime}</span>,
  },
  { id: "ips", header: "IPs", mono: true, cell: (w) => <Addresses workload={w} /> },
];

/** A running Workload's live value, else 0. */
const live = (w: Workload, value: number) => (w.status === "running" ? value : 0);

/** Whether a Workload matches the search: its name, ID, tags or IPs. */
const found = (q: string, w: Workload) => matches(q, w.name, String(w.vmid), ...w.tags, ...w.nics.flatMap((n) => n.ips));

type TabId = "all" | "running" | "stopped";

/** The Tabs, each with the Workloads it shows. */
const tabNames: readonly { id: TabId; label: string; has: (w: Workload) => boolean }[] = [
  { id: "all", label: "All", has: () => true },
  { id: "running", label: "Running", has: (w) => w.status === "running" },
  { id: "stopped", label: "Stopped", has: (w) => w.status === "stopped" },
];

/** The Kind Select's options past Any, as the URL keeps them. */
const kinds = new Map([
  ["lxc", Kind.LXC],
  ["vm", Kind.VM],
]);

/**
 * The proxmox Page: every LXC and VM with its status, resources and IPs,
 * searched, filtered by status and kind and sorted (kept in the URL), and
 * each row's strip with its power Actions.
 */
export function Workloads() {
  const { data, error } = useQuery(ProxmoxApiService.method.listWorkloads, {});
  const tasks = useQuery(ProxmoxService.method.listPowerTasks, {});
  // Read up front, so a strip opens on its power Actions, not on a skeleton.
  useQuery(ProxmoxApiService.method.listPermissions, {});
  const [q, setQ] = useQueryState("q", "");
  const [tab, setTab] = useQueryState("tab", "all");
  const [kind, setKind] = useQueryState("kind", "");
  const all = data?.workloads ?? [];
  const only = kinds.get(kind);
  const searched = all.filter((w) => found(q, w) && (only === undefined || w.kind === only));
  const shown = tabNames.find((t) => t.id === tab) ?? tabNames[0];
  const tabs = tabNames.map((t) => ({ id: t.id, label: t.label, count: searched.filter(t.has).length }));
  return (
    <div>
      <PageTitle kicker="Infrastructure" title="Proxmox" subtitle="Every LXC and VM, with its status, resources and IPs." />
      {error && !data ? (
        <Unlisted error={error} />
      ) : (
        <Panel
          flush
          toolbar={
            <>
              <SearchField label="Search Workloads" value={q} onChange={setQ} placeholder="Search by name, ID, tag or IP" />
              <Select label="Kind" value={only === undefined ? "" : kind} onChange={(e) => setKind(e.target.value)}>
                <option value="">Any kind</option>
                <option value="lxc">LXC</option>
                <option value="vm">VM</option>
              </Select>
              <Tabs label="Show" value={shown.id} options={tabs} onChange={setTab} />
            </>
          }
        >
          <DataTable
            rows={searched.filter(shown.has)}
            rowKey={(w) => String(w.vmid)}
            columns={columns}
            sortKey="sort"
            defaultSort="id"
            loading={!data}
            empty={q.trim() ? noMatch("Workloads", q) : all.length ? "No Workloads match the filters" : "Proxmox lists no LXCs or VMs"}
            rowLabel={(w) => w.name}
            strip={(w) => (w.template ? null : <PowerStrip workload={w} />)}
            busy={(w) => taskOf(tasks.data?.tasks, w)}
          />
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
