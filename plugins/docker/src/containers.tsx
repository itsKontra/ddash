import { useQuery } from "@ddash/plugin";
import { DockerApiService, type Container, type HostContainers } from "@ddash/proto/docker-api/docker_api_pb";
import { Badge, PageHeader, StatusDot, type Tone } from "@ddash/ui";

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
      <PageHeader title="Docker" subtitle="Containers on every Docker host." />
      {error && !data && <p className="text-sm text-down">{error.message}</p>}
      <div className="flex flex-col gap-6">
        {data?.hosts.map((h) => <Host key={h.host} host={h} />)}
      </div>
    </div>
  );
}

function Host({ host }: { host: HostContainers }) {
  const running = host.containers.filter((c) => c.state === "running").length;
  const id = `docker-host-${host.host}`;
  return (
    <section aria-labelledby={id} className="rounded-ui border border-border bg-card">
      <header className="flex items-center gap-3 border-b border-border px-4 py-3">
        <h2 id={id} className="font-semibold">
          {host.host}
        </h2>
        {!host.error && (
          <Badge>
            {running}/{host.containers.length} running
          </Badge>
        )}
      </header>
      {host.error ? (
        <p className="px-4 py-3 text-sm text-down">{host.error}</p>
      ) : (
        <table className="w-full text-sm">
          <tbody>
            {host.containers.map((c) => <Row key={c.id} container={c} />)}
          </tbody>
        </table>
      )}
    </section>
  );
}

function Row({ container: c }: { container: Container }) {
  return (
    <tr className="border-b border-border last:border-b-0">
      <td className="w-6 py-2 pl-4">
        <StatusDot tone={tones[c.state] ?? "idle"} label={c.state} />
      </td>
      <th scope="row" className="px-2 py-2 text-left font-medium">
        {c.name}
      </th>
      <td className="truncate px-2 py-2 text-muted-foreground">{c.image}</td>
      <td className="px-2 py-2 pr-4 text-right text-muted-foreground">{c.status}</td>
    </tr>
  );
}
