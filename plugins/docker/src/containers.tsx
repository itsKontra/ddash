import { useQuery } from "@ddash/plugin";
import { DockerApiService, type Container, type HostContainers } from "@ddash/proto/docker-api/docker_api_pb";
import { DataTable, Notice, PageTitle, Panel, Pill, Skeleton, StatusDot, type Column, type Tone } from "@ddash/ui";

/** How each of Docker's container states reads at a glance. */
const tones: Record<string, Tone> = {
  running: "up",
  restarting: "warn",
  paused: "warn",
  created: "idle",
  removing: "idle",
  exited: "down",
  dead: "down",
};

/** The docker Page: every container on every Docker host, with its state. */
export function Containers() {
  const { data, error } = useQuery(DockerApiService.method.listContainers, {});
  return (
    <div>
      <PageTitle title="Docker" subtitle="Containers on every Docker host." />
      {error && !data && <Notice tone="down">{error.message}</Notice>}
      {!data && !error && <Skeleton lines={3} />}
      <div className="flex flex-col gap-6">
        {data?.hosts.map((h) => <Host key={h.host} host={h} />)}
      </div>
    </div>
  );
}

const columns: readonly Column<Container>[] = [
  { id: "name", header: "Container", rowHeader: true, cell: (c) => c.name },
  {
    id: "state",
    header: "State",
    cell: (c) => (
      <span className="inline-flex items-center gap-2">
        <StatusDot tone={tones[c.state] ?? "idle"} label={c.state} />
        {c.state}
      </span>
    ),
  },
  { id: "image", header: "Image", mono: true, hideBelow: "sm", cell: (c) => c.image },
  { id: "status", header: "Status", cell: (c) => c.status },
];

function Host({ host }: { host: HostContainers }) {
  const running = host.containers.filter((c) => c.state === "running").length;
  return (
    <Panel
      title={host.host}
      flush
      actions={
        !host.error && (
          <Pill tone={running === host.containers.length ? "up" : "idle"}>
            {running}/{host.containers.length} running
          </Pill>
        )
      }
    >
      {host.error ? (
        <Notice tone="down">{host.error}</Notice>
      ) : (
        <DataTable rows={host.containers} rowKey={(c) => c.id} columns={columns} empty="No containers on this host" />
      )}
    </Panel>
  );
}
