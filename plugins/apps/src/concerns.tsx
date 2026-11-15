import { PlanDialog } from "@ddash/compose-ui";
import { ago } from "@ddash/display";
import { useAction, usePluginLink, type Concern, type ConcernAction } from "@ddash/plugin";
import { ChecksService, type Check } from "@ddash/proto/checks/checks_pb";
import { useState } from "react";
import { useNavigate } from "react-router";
import { useApps } from "./apps";
import type { WorkloadFix } from "./cause";
import { isDown, useDownCauses } from "./causes";
import { useAppChecks } from "./checks";

/** Where an App's detail lives: the Apps Page with it selected. */
export const appHref = (id: string) => `/apps?app=${encodeURIComponent(id)}`;

/**
 * What needs the Operator in apps: each App whose Check is down. Its cause
 * is its Workload when that isn't running ("LXC 206 n8n is stopped"),
 * else the Check's reason; its fix is the owning Plugin's own Action:
 * Start on the guest's Proxmox detail (which asks before it starts it),
 * or the Compose Project's Restart with its Plan. A Protected Workload
 * has no fix. Mute is the Checks Page's, not a fix. One muted or in
 * Maintenance is quiet, its fix kept. The checks Plugin leaves App Checks
 * to this list. Nothing while checks can't be read: checks says so.
 */
export function useConcerns(): Concern[] | undefined {
  const { list, error: appsError } = useApps();
  const { byApp, error: checksError, answeredAt } = useAppChecks();
  const { causes, known } = useDownCauses(list?.apps, byApp);
  const proxmox = usePluginLink("proxmox");
  const navigate = useNavigate();
  const mute = useAction(ChecksService.method.setMuted);
  // The Check Mute last ran for, whose row shows its pending and error.
  const [muting, setMuting] = useState<bigint>();
  if (checksError) return [];
  if (!byApp || (!list && !appsError) || !known) return undefined;

  const fixOf = (fix: WorkloadFix | undefined): ConcernAction[] => {
    if (fix?.kind === "guest") {
      return proxmox ? [{ label: fix.label, icon: "play", run: () => void navigate(`${proxmox}/workloads/${fix.vmid}?start`) }] : [];
    }
    if (fix?.kind === "project") {
      const { host, project } = fix;
      return [
        {
          label: "Restart",
          icon: "rotate-cw",
          sheet: (close) => <PlanDialog title={`Restart ${project}`} action={{ action: { case: "restart", value: { host, project } } }} confirm="Restart" danger onClose={close} />,
        },
      ];
    }
    return [];
  };
  const muteOf = (c: Check): ConcernAction => ({
    label: "Mute",
    icon: "bell-off",
    secondary: true,
    pending: muting === c.id && mute.pending,
    error: muting === c.id ? mute.error?.rawMessage : undefined,
    run: () => {
      setMuting(c.id);
      return mute.run({ id: c.id, muted: true });
    },
  });

  return [...byApp.values()].filter(isDown).map((c): Concern => {
    const name = list?.apps.find((a) => a.id === c.appId)?.name ?? (c.spec?.name || c.appId);
    const cause = causes.get(c.appId);
    const quiet = c.maintenance ? "in Maintenance" : c.muted ? "muted" : undefined;
    const actions = [...fixOf(cause?.fix), ...(quiet ? [] : [muteOf(c)])];
    return {
      id: c.appId,
      tone: "down",
      subject: name,
      title: `${name} is down`,
      cause: [cause?.text ?? (c.reason || "down"), ago(c.since, answeredAt)].join(" · "),
      quiet,
      icon: c.maintenance ? "wrench" : undefined,
      href: appHref(c.appId),
      actions: actions.length ? actions : undefined,
    };
  });
}
