import { kindLabel } from "@ddash/display";
import { Refresh, useQuery, type Command } from "@ddash/plugin";
import { AgentState, HostAgentApiService, type Agent } from "@ddash/proto/host-agent-api/host_agent_api_pb";
import { ProxmoxApiService, type ListPermissionsResponse, type Workload } from "@ddash/proto/proxmox-api/proxmox_api_pb";
import type { IconName } from "@ddash/ui";
import { mayPower, powerItems, usePowerActions, type PowerActions, type PowerItem } from "./power";
import { UpdateAllDialog } from "./update-all";
import { UpdatePlanDialog } from "./updates";

const icons: Record<PowerItem["label"], IconName> = { Start: "play", Restart: "rotate-cw", Stop: "power" };
// The words the Operator may type for a power Action besides its own.
const aliases: Record<PowerItem["label"], string[]> = { Start: ["boot", "power on"], Restart: ["reboot"], Stop: ["shut down", "shutdown", "power off"] };

/**
 * The proxmox Commands: per guest the power Actions its status allows
 * (Start, Restart, Stop), each running the Page's own Action with its
 * confirm, but none for a Protected guest or one whose permissions aren't
 * known; Update for a running guest with a paired Host Agent, opening the
 * detail's Plan; and Update all running guests, opening the Page's Plan.
 */
export function guestCommands(
  workloads: readonly Workload[],
  { perms, agents, power }: { perms?: ListPermissionsResponse; agents?: readonly Agent[]; power: PowerActions },
): Command[] {
  const paired = new Set(agents?.filter((a) => a.state === AgentState.PAIRED).map((a) => a.vmid));
  const out: Command[] = [];
  for (const w of workloads) {
    if (w.template) continue;
    const name = w.name || String(w.vmid);
    const about = { hint: `${kindLabel(w.kind)} ${w.vmid}`, keywords: [kindLabel(w.kind), w.node, "guest", ...w.tags] };
    if (mayPower(perms, w)) {
      for (const i of powerItems(w, power)) {
        out.push({ id: `${i.label.toLowerCase()}:${w.vmid}`, title: `${i.label} ${name}`, icon: icons[i.label], ...about, keywords: [...about.keywords, ...aliases[i.label]], run: i.run });
      }
    }
    if (w.status === "running" && paired.has(w.vmid)) {
      out.push({
        id: `update:${w.vmid}`,
        title: `Update ${name}`,
        icon: "download",
        ...about,
        keywords: [...about.keywords, "packages", "upgrade", "apt"],
        sheet: (close) => <UpdatePlanDialog workload={w} onClose={close} />,
      });
    }
  }
  out.push({
    id: "update-all",
    title: "Update all running guests",
    hint: "Proxmox",
    icon: "download",
    keywords: ["packages", "upgrade", "apt", "LXC", "VM"],
    sheet: (close) => <UpdateAllDialog onClose={close} />,
  });
  return out;
}

/**
 * The proxmox Plugin's Commands for the command palette; undefined until
 * proxmox-api listed the Workloads. They run on every Page, so beyond the
 * Workloads (polled there for the Concerns already) the permissions and
 * Host Agents are read once, not polled: the Page polls them, and each
 * Action's success reads them again.
 */
export function useCommands(): Command[] | undefined {
  const { data } = useQuery(ProxmoxApiService.method.listWorkloads, {});
  const perms = useQuery(ProxmoxApiService.method.listPermissions, {}, { refresh: Refresh.NONE });
  const agents = useQuery(HostAgentApiService.method.listAgents, {}, { refresh: Refresh.NONE });
  const power = usePowerActions();
  return data && guestCommands(data.workloads, { perms: perms.data, agents: agents.data?.agents, power });
}
