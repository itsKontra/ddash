import { useQuery, type Status, type Widget, type WidgetSize } from "@ddash/plugin";
import { DockerApiService, type Container } from "@ddash/proto/docker-api/docker_api_pb";
import { Facts, MetricTile, Pill } from "@ddash/ui";

const unhealthy = (c: Container) => c.status.includes("(unhealthy)");
const restarting = (c: Container) => c.state === "restarting";
const running = (cs: readonly Container[]) => cs.filter((c) => c.state === "running").length;

function useContainers() {
  return useQuery(DockerApiService.method.listContainers, {});
}

/**
 * Up, with the running count, while every container is fine. Warn when a
 * container is unhealthy or restarting, a Docker host can't be read, or
 * docker-api is unavailable.
 */
function useStatus(): Status | undefined {
  const { data, error } = useContainers();
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
  return { tone: "up", summary: `${running(all)} of ${all.length} running` };
}

/** S: the running and total counts. M: each host's counts too. */
function Body({ size }: { size: WidgetSize }) {
  const { data } = useContainers();
  const all = data?.hosts.flatMap((h) => h.containers);
  const troubled = all?.filter((c) => unhealthy(c) || restarting(c)).length ?? 0;
  return (
    <div className="flex flex-col gap-3">
      <div className="grid grid-cols-2 gap-3">
        <MetricTile label="running" value={all && running(all)} tone="up" />
        <MetricTile label="containers" value={all?.length} />
        {troubled > 0 && <MetricTile label="need attention" value={troubled} tone="warn" />}
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

/** The Overview Widget: container counts across every Docker host. */
export const containers: Widget = {
  id: "containers",
  title: "Containers",
  sizes: ["S", "M"],
  defaultSize: "S",
  Body,
  useStatus,
};
