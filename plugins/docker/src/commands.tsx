import { useQuery, type Command } from "@ddash/plugin";
import { ComposeService, type Host } from "@ddash/proto/compose/compose_pb";
import type { IconName } from "@ddash/ui";
import { planDialog, projectPlans } from "./projects";

// What the palette offers of a Compose Project's Actions, by the Plan's verb.
const offered: Record<string, { icon: IconName; keywords: string[] }> = {
  Start: { icon: "play", keywords: ["up"] },
  Restart: { icon: "rotate-cw", keywords: ["reboot"] },
  Update: { icon: "download", keywords: ["pull", "upgrade", "images"] },
};

/**
 * The docker Commands: Start, Restart and Update of each Compose Project
 * its state allows, as on its Page, each opening the Page's own Plan
 * Dialog. None while the Project runs an Action, and a Protected one
 * offers no Restart or Update (both need the Plan's override): only Start.
 * There are no logs to offer: ddash doesn't read containers' logs.
 */
export function projectCommands(hosts: readonly Host[]): Command[] {
  return hosts.flatMap((h) =>
    h.projects.flatMap((p): Command[] => {
      if (p.runId) return [];
      return projectPlans(p).flatMap((plan): Command[] => {
        const what = offered[plan.confirm];
        if (!what || (p.protected && plan.confirm !== "Start")) return [];
        return [
          {
            id: `${plan.confirm.toLowerCase()}:${p.host}/${p.name}`,
            title: `${plan.confirm} ${p.name}`,
            hint: `Compose Project on ${p.host}`,
            icon: what.icon,
            keywords: [p.host, "compose", ...what.keywords],
            sheet: (close) => planDialog(plan, close),
          },
        ];
      });
    }),
  );
}

/** The docker Plugin's Commands for the command palette, from the Compose Projects its Concerns poll already; undefined until listed. */
export function useCommands(): Command[] | undefined {
  const { data } = useQuery(ComposeService.method.listProjects, {});
  return data && projectCommands(data.hosts);
}
