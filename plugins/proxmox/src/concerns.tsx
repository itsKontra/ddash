import { bytes } from "@ddash/display";
import { useQuery, type Concern } from "@ddash/plugin";
import { ProxmoxApiService } from "@ddash/proto/proxmox-api/proxmox_api_pb";
import { percent, tightGuests } from "./hosts";

/**
 * What needs the Operator in proxmox: each running guest whose memory or
 * disk is 75 % full or more, with the fuller one's figures and a link to
 * the guest. Nothing to run: freeing space is the guest's own business.
 */
export function useConcerns(): Concern[] | undefined {
  const { data, error } = useQuery(ProxmoxApiService.method.listWorkloads, {});
  if (!data) return error ? [] : undefined;
  return tightGuests(data.workloads).map(({ workload: w, resource, ratio }): Concern => {
    const name = w.name || String(w.vmid);
    const [used, max] = resource === "disk" ? [w.disk, w.maxDisk] : [w.mem, w.maxMem];
    return {
      id: String(w.vmid),
      tone: "warn",
      subject: name,
      title: `${name} is running out of ${resource}`,
      cause: `${resource} ${percent(ratio)} · ${bytes(used)} of ${bytes(max)}`,
      icon: resource === "disk" ? "hard-drive" : "memory-stick",
      href: `/proxmox/workloads/${w.vmid}`,
    };
  });
}
