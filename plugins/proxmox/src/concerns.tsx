import { useQuery, type Concern } from "@ddash/plugin";
import { ProxmoxApiService } from "@ddash/proto/proxmox-api/proxmox_api_pb";
import { guestConcerns, nodeConcerns } from "./hosts";

/**
 * What needs the Operator in proxmox: whatever makes the Hosts Widget warn
 * or down. The node's meters as nodeTone has them (to the Proxmox Page),
 * then each running guest whose memory or disk is 75 % full or more (to
 * the guest). Nothing to run: freeing space is the guest's own business.
 * The nodes left unread (no Sys.Audit) report nothing of the node.
 */
export function useConcerns(): Concern[] | undefined {
  const { data, error } = useQuery(ProxmoxApiService.method.listWorkloads, {});
  const nodes = useQuery(ProxmoxApiService.method.listNodes, {});
  if ((!data && !error) || (!nodes.data && !nodes.error)) return undefined;
  return [...nodeConcerns(nodes.data?.nodes ?? []), ...guestConcerns(data?.workloads ?? [])];
}
