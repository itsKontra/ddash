import { useQuery } from "@ddash/plugin";
import { ProxmoxApiService, type Workload } from "@ddash/proto/proxmox-api/proxmox_api_pb";
import { Badge, PageHeader, StatusDot } from "@ddash/ui";
import { useId, type ReactNode } from "react";
import { Link, useParams } from "react-router";
import { bytes, kindLabel } from "@ddash/display";
import { cpu, uptime } from "./format";
import { PowerButtons } from "./power";
import { Addresses, tones } from "./workloads";

/** A Workload's detail (/proxmox/workloads/:vmid): its power Actions (or the lock) and its facts. Apps link their Workload here. */
export function WorkloadDetail() {
  const { vmid = "" } = useParams();
  const { data, error } = useQuery(ProxmoxApiService.method.listWorkloads, {});
  const back = (
    <Link to="../.." relative="path" className="text-sm text-muted-foreground hover:underline">
      ← Proxmox
    </Link>
  );
  if (error && !data) {
    return (
      <div>
        <PageHeader title={vmid} actions={back} />
        <p className="text-sm text-down-text">{error.rawMessage}</p>
      </div>
    );
  }
  if (!data) return null;
  const w = data.workloads.find((x) => String(x.vmid) === vmid);
  if (!w) {
    return (
      <div>
        <PageHeader title={vmid} actions={back} />
        <p className="text-sm text-muted-foreground">Proxmox lists no Workload {vmid}.</p>
      </div>
    );
  }
  return (
    <div>
      <PageHeader title={w.name} subtitle={`${kindLabel(w.kind)} ${w.vmid} on ${w.node}`} actions={back} />
      <div className="flex flex-col gap-6">
        <Panel title="Power">
          <PowerButtons workload={w} />
        </Panel>
        <Details workload={w} />
      </div>
    </div>
  );
}

function Panel({ title, children }: { title: string; children: ReactNode }) {
  const id = useId();
  return (
    <section aria-labelledby={id} className="rounded-ui border border-border bg-card">
      <div className="border-b border-border px-4 py-3">
        <h2 id={id} className="font-semibold">
          {title}
        </h2>
      </div>
      <div className="px-4 py-3 text-sm">{children}</div>
    </section>
  );
}

function Details({ workload: w }: { workload: Workload }) {
  const running = w.status === "running";
  const rows: [string, ReactNode][] = [
    [
      "Status",
      <span key="status" className="inline-flex items-center gap-2">
        <StatusDot tone={tones[w.status] ?? "idle"} />
        {w.status}
        {w.template && <Badge>template</Badge>}
      </span>,
    ],
    ["Node", w.node],
    ["CPU", running ? cpu(w.cpu, w.maxCpu) : `${w.maxCpu} ${w.maxCpu === 1 ? "CPU" : "CPUs"}`],
    ["Memory", running ? `${bytes(w.mem)} / ${bytes(w.maxMem)}` : bytes(w.maxMem)],
    ["Disk", w.disk ? `${bytes(w.disk)} / ${bytes(w.maxDisk)}` : bytes(w.maxDisk)],
    ["Network", running ? `${bytes(w.netIn)} in · ${bytes(w.netOut)} out` : "–"],
    ["Uptime", running ? uptime(w.uptimeSeconds) : "–"],
    ["IPs", <Addresses key="ips" workload={w} />],
    ["Tags", w.tags.join(", ") || "–"],
  ];
  return (
    <Panel title="Details">
      <dl className="grid grid-cols-3 gap-x-4 gap-y-2">
        {rows.map(([label, value]) => (
          <div key={label} className="contents">
            <dt className="text-muted-foreground">{label}</dt>
            <dd className="col-span-2 tabular-nums">{value}</dd>
          </div>
        ))}
      </dl>
    </Panel>
  );
}
