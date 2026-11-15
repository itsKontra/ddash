import { useQuery } from "@ddash/plugin";
import { Protocol, State, type Container } from "@ddash/proto/ddash/container/container_pb";
import { DockerApiService, type HostContainers } from "@ddash/proto/docker-api/docker_api_pb";
import { DataTable, Notice, PageTitle, Panel, Pill, Skeleton, StatusDot, type Column, type Tone } from "@ddash/ui";

/** How each of Docker's container states reads at a glance, and its word as Docker writes it. */
const states: Record<State, { tone: Tone; word: string }> = {
  [State.UNSPECIFIED]: { tone: "idle", word: "unknown" },
  [State.CREATED]: { tone: "idle", word: "created" },
  [State.RUNNING]: { tone: "up", word: "running" },
  [State.PAUSED]: { tone: "warn", word: "paused" },
  [State.RESTARTING]: { tone: "warn", word: "restarting" },
  [State.REMOVING]: { tone: "idle", word: "removing" },
  [State.EXITED]: { tone: "down", word: "exited" },
  [State.DEAD]: { tone: "down", word: "dead" },
};

const protocols: Record<Protocol, string> = {
  [Protocol.UNSPECIFIED]: "tcp",
  [Protocol.TCP]: "tcp",
  [Protocol.UDP]: "udp",
  [Protocol.SCTP]: "sctp",
};

/** A published port's protocol as Docker writes it. */
export const protocolWord = (p: Protocol) => protocols[p];

/** Whether a container runs. */
export const isRunning = (c: Container | undefined) => c?.state === State.RUNNING;

/** A container's state: its dot and its word. */
export function ContainerState({ state, word = true }: { state: State; word?: boolean }) {
  const s = states[state] ?? states[State.UNSPECIFIED];
  return (
    <span className="inline-flex items-center gap-2">
      <StatusDot tone={s.tone} label={s.word} />
      {word && s.word}
    </span>
  );
}

/** The docker Page: every container on every Docker host, with its state. */
export function Containers() {
  const { data, error } = useQuery(DockerApiService.method.listContainers, {});
  return (
    <div>
      <PageTitle kicker="Infrastructure" title="Docker" subtitle="Containers on every Docker host." />
      {error && !data && <Notice tone="down">{error.rawMessage}</Notice>}
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
    cell: (c) => <ContainerState state={c.state} />,
  },
  { id: "image", header: "Image", mono: true, hideBelow: "sm", cell: (c) => c.image },
  { id: "status", header: "Status", cell: (c) => c.status },
];

/** A host read through a socket-proxy: its containers, read-only. readOnly says so, beside hosts with a Docker Agent. */
export function Host({ host, readOnly }: { host: HostContainers; readOnly?: boolean }) {
  const running = host.containers.filter(isRunning).length;
  return (
    <Panel
      title={host.host}
      description={readOnly ? "Read-only, through a socket-proxy: a Docker Agent on it lets ddash manage its Compose Projects." : undefined}
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
