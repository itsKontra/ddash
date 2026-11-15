import { ago } from "@ddash/display";
import { useAction, usePluginLink, type Concern } from "@ddash/plugin";
import { ChecksService, State } from "@ddash/proto/checks/checks_pb";
import { useState } from "react";
import { shownName, useChecks } from "./checks";
import { MaintenanceSheet } from "./maintenance";

/**
 * What needs the Operator in checks: each enabled Check that is down, with
 * its reason and since when, and the Page's Mute and Maintenance, neither
 * a fix. One that is muted or in Maintenance (with the wrench) is quiet,
 * with no Actions. A warn one when checks can't be read. An App's Check
 * is left to the apps Plugin while it is installed: it knows the App's
 * Workload, and so the cause and the fix.
 */
export function useConcerns(): Concern[] | undefined {
  const { data, error, dataUpdatedAt } = useChecks();
  const apps = usePluginLink("apps");
  const mute = useAction(ChecksService.method.setMuted);
  // The Check Mute last ran for, whose row shows its pending and error.
  const [muting, setMuting] = useState<bigint>();
  if (error) return [{ id: "unavailable", tone: "warn", subject: "Checks", title: "Checks can't be read", cause: error.rawMessage, href: "/checks" }];
  if (!data) return undefined;
  const checks = data.checks;
  return checks
    .filter((c) => !c.disabled && c.state === State.DOWN && !(apps && c.appId))
    .map((c): Concern => {
      const name = shownName(c);
      const quiet = c.maintenance ? "in Maintenance" : c.muted ? "muted" : undefined;
      const mine = muting === c.id;
      return {
        id: String(c.id),
        tone: "down",
        subject: name,
        title: `${name} is down`,
        cause: [c.reason || "down", ago(c.since, dataUpdatedAt)].join(" · "),
        quiet,
        // Told apart at a glance once unfolded.
        icon: c.maintenance ? "wrench" : undefined,
        href: `/checks/${c.id}`,
        // Neither fixes a Check that is down, so neither is drawn as a fix.
        actions: quiet
          ? undefined
          : [
              {
                label: "Mute",
                icon: "bell-off",
                secondary: true,
                pending: mine && mute.pending,
                error: mine ? mute.error?.rawMessage : undefined,
                run: () => {
                  setMuting(c.id);
                  return mute.run({ id: c.id, muted: true });
                },
              },
              { label: "Maintenance", icon: "wrench", secondary: true, sheet: (close) => <MaintenanceSheet check={c} checks={checks} onClose={close} /> },
            ],
      };
    });
}
