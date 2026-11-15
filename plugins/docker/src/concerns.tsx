import { ago } from "@ddash/display";
import { useAction, useQuery, type Concern } from "@ddash/plugin";
import { ComposeService, ImageState, type Host } from "@ddash/proto/compose/compose_pb";
import { containersOf } from "./images";

/** A Docker host's containers whose image couldn't be checked in the last check. */
const failed = (h: Host) => [...h.projects.flatMap(containersOf), ...h.containers].filter((c) => c.imageStatus?.state === ImageState.ERROR);

/** "a, b" or "a, b and 3 more". */
function some(names: readonly string[]) {
  const shown = names.slice(0, 2).join(", ");
  return names.length > 2 ? `${shown} and ${names.length - 2} more` : shown;
}

/**
 * What needs the Operator in docker: each Docker host whose image check
 * failed, with the images, why and when, and the Page's Check now as
 * Check again. It checks every host's images, as on the Page.
 */
export function useConcerns(): Concern[] | undefined {
  const { data, error, dataUpdatedAt } = useQuery(ComposeService.method.listProjects, {});
  const check = useAction(ComposeService.method.checkImages);
  if (!data) return error ? [] : undefined;
  const checks = data.checks;
  return data.hosts.flatMap((h): Concern[] => {
    const cs = failed(h);
    if (cs.length === 0) return [];
    const images = [...new Set(cs.map((c) => c.container?.image || c.container?.name || ""))].filter(Boolean);
    const reasons = [...new Set(cs.map((c) => c.imageStatus!.reason))].filter(Boolean);
    const checked = cs.find((c) => c.imageStatus!.checked)?.imageStatus!.checked ?? checks?.last;
    return [
      {
        id: h.name,
        tone: "info",
        subject: h.name,
        title: `Image check failed on ${h.name}`,
        cause: [some(images), reasons.join("; ") || "check failed", ago(checked, dataUpdatedAt)].filter(Boolean).join(" · "),
        icon: "package",
        href: "/docker",
        actions: [
          {
            label: "Check again",
            icon: "refresh-cw",
            pending: check.pending || !!checks?.running,
            error: check.error?.rawMessage,
            run: () => check.run({}),
          },
        ],
      },
    ];
  });
}
