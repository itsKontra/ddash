import { useQuery, type Status, type Widget, type WidgetSize } from "@ddash/plugin";
import { ComposeService, ImageState } from "@ddash/proto/compose/compose_pb";
import { Health, State, type Container } from "@ddash/proto/ddash/container/container_pb";
import { DockerApiService } from "@ddash/proto/docker-api/docker_api_pb";
import { Facts, MetricTile, Pill, Skeleton, Summary } from "@ddash/ui";
import { containersLine, containersRunning, exitedRows, hostsStatus, updateState } from "./hosts";
import { useNow } from "./now";

const unhealthy = (c: Container) => c.health === Health.UNHEALTHY;
const restarting = (c: Container) => c.state === State.RESTARTING;
const running = (cs: readonly Container[]) => cs.filter((c) => c.state === State.RUNNING).length;

function useContainers() {
  return useQuery(DockerApiService.method.listContainers, {});
}

/** How many running containers on Agent hosts have a newer image waiting; undefined until known. */
function useUpdates(): number | undefined {
  const { data } = useQuery(ComposeService.method.listProjects, {});
  if (!data) return undefined;
  return data.hosts
    .flatMap((h) => [...h.projects.flatMap((p) => p.services.flatMap((s) => s.containers)), ...h.containers])
    .filter((c) => c.imageStatus?.state === ImageState.STALE).length;
}

/**
 * Up, with the running count (and the image updates waiting on hosts with
 * a Docker Agent), while every container is fine. Warn when a
 * container is unhealthy or restarting, a Docker host can't be read, or
 * docker-api is unavailable.
 */
function useStatus(): Status | undefined {
  const { data, error } = useContainers();
  const updates = useUpdates();
  if (error) return { tone: "warn", summary: `docker-api unavailable: ${error.rawMessage}` };
  if (!data) return undefined;
  const all = data.hosts.flatMap((h) => h.containers);
  const problems = [
    [all.filter(unhealthy).length, "unhealthy"],
    [all.filter(restarting).length, "restarting"],
  ]
    .filter(([n]) => n)
    .map(([n, what]) => `${n} ${what}`);
  const unreadable = data.hosts.filter((h) => h.error).map((h) => `${h.host} unreadable`);
  if (problems.length || unreadable.length) return { tone: "warn", summary: [...problems, ...unreadable].join(" · ") };
  const waiting = updates ? ` · ${updates} ${updates === 1 ? "update" : "updates"} waiting` : "";
  return { tone: "up", summary: `${running(all)} of ${all.length} running${waiting}` };
}

/** S: the running and total counts. M: each host's counts too. */
function Body({ size }: { size: WidgetSize }) {
  const { data } = useContainers();
  const all = data?.hosts.flatMap((h) => h.containers);
  const troubled = all?.filter((c) => unhealthy(c) || restarting(c)).length ?? 0;
  const updates = useUpdates();
  return (
    <div className="flex flex-col gap-3">
      <div className="grid grid-cols-2 gap-3">
        <MetricTile label="running" value={all && running(all)} tone="up" />
        <MetricTile label="containers" value={all?.length} />
        {troubled > 0 && <MetricTile label="need attention" value={troubled} tone="warn" />}
        {updates !== undefined && updates > 0 && <MetricTile label={updates === 1 ? "update waiting" : "updates waiting"} value={updates} tone="warn" />}
      </div>
      {size === "M" && data && (
        <Facts
          items={data.hosts.map((h) => ({
            label: h.host,
            value: h.error ? <Pill tone="warn">unreadable</Pill> : `${running(h.containers)}/${h.containers.length} running`,
          }))}
        />
      )}
    </div>
  );
}

/** Warn while docker-api is unavailable; else the hosts' status. */
function useHostsStatus(): Status | undefined {
  const { data, error } = useContainers();
  if (error) return { tone: "warn", summary: `docker-api unavailable: ${error.rawMessage}` };
  return data && hostsStatus(data.hosts);
}

/** The containers running, the exited ones in the down tone and the image updates with the age of their check. */
function HostsBody() {
  const { data } = useContainers();
  const projects = useQuery(ComposeService.method.listProjects, {});
  const now = useNow();
  if (!data) return <Skeleton lines={2} />;
  const line = containersLine(data.hosts, updateState(projects.data, now));
  return <Summary headline={containersRunning(data.hosts)} line={line} rows={exitedRows(data.hosts)} />;
}

/**
 * Are the containers all right? The Docker half of Hosts; the proxmox
 * Plugin offers the node's half under the same name.
 */
export const hosts: Widget = {
  id: "hosts",
  title: "Hosts",
  sizes: ["S", "M"],
  defaultSize: "S",
  Body: HostsBody,
  useStatus: useHostsStatus,
};

/** The Overview Widget: container counts across every Docker host. */
export const containers: Widget = {
  id: "containers",
  title: "Containers",
  sizes: ["S", "M"],
  defaultSize: "S",
  Body,
  useStatus,
};
