import { ago } from "@ddash/display";
import { useAction, type Concern } from "@ddash/plugin";
import { ChecksService, State } from "@ddash/proto/checks/checks_pb";
import { useState } from "react";
import { shownName, useChecks } from "./checks";
import { MaintenanceSheet } from "./maintenance";

/**
 * What needs the Operator in checks: each enabled Check that is down, with
 * its reason and since when, and the Page's Mute and Maintenance. One that
 * is muted or in Maintenance is quiet, with no Actions. A warn one when
 * checks can't be read.
 */
export function useConcerns(): Concern[] | undefined {
  const { data, error, dataUpdatedAt } = useChecks();
  const mute = useAction(ChecksService.method.setMuted);
  // The Check Mute last ran for, whose row shows its pending and error.
  const [muting, setMuting] = useState<bigint>();
  if (error) return [{ id: "unavailable", tone: "warn", subject: "Checks", title: "Checks can't be read", cause: error.rawMessage, href: "/checks" }];
  if (!data) return undefined;
  const checks = data.checks;
  return checks
    .filter((c) => !c.disabled && c.state === State.DOWN)
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
        href: `/checks/${c.id}`,
        actions: quiet
          ? undefined
          : [
              {
                label: "Mute",
                icon: "bell-off",
                pending: mine && mute.pending,
                error: mine ? mute.error?.rawMessage : undefined,
                run: () => {
                  setMuting(c.id);
                  return mute.run({ id: c.id, muted: true });
                },
              },
              { label: "Maintenance", icon: "wrench", sheet: (close) => <MaintenanceSheet check={c} checks={checks} onClose={close} /> },
            ],
      };
    });
}
