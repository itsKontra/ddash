import { ComposeFiles } from "@ddash/compose-ui";
import { usePluginLink, useQuery } from "@ddash/plugin";
import type { App } from "@ddash/proto/apps/apps_pb";
import { ComposeService } from "@ddash/proto/compose/compose_pb";
import { Kind, type WorkloadRef } from "@ddash/proto/ddash/workload/workload_pb";
import { Panel } from "@ddash/ui";
import { Link } from "react-router";

/**
 * Compose Project: the Compose file and .env of the Compose Project whose
 * container is the App's Workload, to view and edit as on the Docker
 * Page. Nothing while the Workload isn't a container of a Compose Project
 * ddash lists.
 */
export function ComposePanel({ app }: { app: App }) {
  const ref = app.workload?.ref;
  return ref?.kind === Kind.DOCKER ? <Project workload={ref} /> : null;
}

function Project({ workload: ref }: { workload: WorkloadRef }) {
  const { data } = useQuery(ComposeService.method.listProjects, {});
  const p = data?.hosts
    .find((h) => h.name === ref.host)
    ?.projects.find((x) => x.services.some((s) => s.containers.some((c) => c.name === ref.id)));
  const docker = usePluginLink("docker", p ? `projects/${encodeURIComponent(p.host)}/${encodeURIComponent(p.name)}` : "");
  if (!p) return null;
  return (
    <Panel
      title="Compose Project"
      description={`${p.name} on ${p.host}: a Compose file is saved only after docker compose config accepts it there; the previous content is kept as .bak.`}
      actions={docker ? <Link to={docker}>Open on the Docker Page</Link> : undefined}
    >
      <ComposeFiles project={p} />
    </Panel>
  );
}
