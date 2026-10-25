import { useQuery } from "@ddash/plugin";
import { ProxmoxApiService, type Workload } from "@ddash/proto/proxmox-api/proxmox_api_pb";
import { EmptyState, Facts, PageTitle, Panel, Skeleton } from "@ddash/ui";
import { useParams } from "react-router";
import { kindLabel } from "@ddash/display";
import { PowerButtons } from "./power";
import { Addresses, resources, StatusPill, Tags, Unlisted } from "./workloads";

// Relative to the route, so ".." is the Plugin's root, the Workloads.
const back = { href: "..", label: "Proxmox" };

/** A Workload's detail (/proxmox/workloads/:vmid): its power Actions (or the lock) and its facts. Apps link their Workload here. */
export function WorkloadDetail() {
  const { vmid = "" } = useParams();
  const { data, error } = useQuery(ProxmoxApiService.method.listWorkloads, {});
  const w = data?.workloads.find((x) => String(x.vmid) === vmid);
  if (!w) {
    return (
      <div>
        <PageTitle kicker="Workload" title={vmid} back={back} />
        {error && !data ? (
          <Unlisted error={error} />
        ) : !data ? (
          <Skeleton height="lg" />
        ) : (
          <Panel>
            <EmptyState icon="server" title={`Proxmox lists no Workload ${vmid}.`} />
          </Panel>
        )}
      </div>
    );
  }
  return (
    <div>
      <PageTitle kicker="Workload" title={w.name} subtitle={`${kindLabel(w.kind)} ${w.vmid} on ${w.node}`} back={back} />
      <div className="flex flex-col gap-6">
        <Panel title="Power">
          <PowerButtons workload={w} />
        </Panel>
        <Panel title="Details">
          <Facts items={facts(w)} />
        </Panel>
      </div>
    </div>
  );
}

function facts(w: Workload) {
  const r = resources(w);
  return [
    { label: "Status", value: <StatusPill workload={w} /> },
    { label: "Node", value: w.node },
    { label: "CPU", value: r.cpu },
    { label: "Memory", value: r.memory },
    { label: "Disk", value: r.disk },
    { label: "Network", value: r.network },
    { label: "Uptime", value: r.uptime },
    { label: "IPs", value: <Addresses workload={w} />, mono: true },
    { label: "Tags", value: <Tags workload={w} /> },
  ];
}
