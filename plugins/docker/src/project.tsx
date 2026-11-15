import { CancelRun, ComposeFiles, ending, JobLog, PlanDialog, PlanSteps, type ActionInit } from "@ddash/compose-ui";
import { useQuery, useShownRuns } from "@ddash/plugin";
import { State } from "@ddash/proto/ddash/container/container_pb";
import { ComposeService, ImageState, type Project, type ProjectContainer, type Run } from "@ddash/proto/compose/compose_pb";
import { ago } from "@ddash/display";
import { ActionStrip, Button, DataTable, EmptyState, Facts, Notice, PageTitle, Panel, Pill, Skeleton, type Column } from "@ddash/ui";
import { useState, type ReactNode } from "react";
import { useParams } from "react-router";
import { ContainerState, isRunning, protocolWord } from "./containers";
import { ImageBadge, ProjectStatePill, shortDigest } from "./images";
import { useNow } from "./now";
import { projectActions } from "./projects";

// Relative to the route, so ".." is the Plugin's root.
const back = { href: "..", label: "Docker" };

interface Row {
  service: string;
  image: string;
  ports: string[];
  container?: ProjectContainer;
}

/** "8096→8096/tcp", once per port: a port published on every IPv4 and IPv6 address reads as one. */
const ports = (c: ProjectContainer) => [...new Set((c.container?.ports ?? []).map((p) => `${p.ip && p.ip !== "0.0.0.0" && p.ip !== "::" ? `${p.ip}:` : ""}${p.publicPort}→${p.privatePort}/${protocolWord(p.protocol)}`))].join(", ");

const columns: readonly Column<Row>[] = [
  { id: "service", header: "Compose service", rowHeader: true, cell: (r) => r.service },
  {
    id: "state",
    header: "Container",
    cell: (r) =>
      r.container ? (
        <span className="inline-flex items-center gap-2">
          <ContainerState state={r.container.container?.state ?? State.UNSPECIFIED} word={false} />
          <span>
            {r.container.container?.name}
            <span className="block text-xs text-muted-foreground">{r.container.container?.status}</span>
          </span>
        </span>
      ) : (
        <span className="text-muted-foreground">no container</span>
      ),
  },
  {
    id: "image",
    header: "Image",
    cell: (r) => (
      <span className="flex flex-col gap-1">
        <span className="font-mono text-xs break-all">{r.container?.container?.image ?? r.image}</span>
        <ImageBadge status={r.container?.imageStatus} />
      </span>
    ),
  },
  {
    id: "digest",
    header: "Digest",
    hideBelow: "sm",
    mono: true,
    cell: (r) => {
      const s = r.container?.imageStatus;
      if (!s?.currentDigest) return <span className="text-muted-foreground">–</span>;
      return (
        <span className="flex flex-col text-xs">
          <span>{shortDigest(s.currentDigest)}</span>
          {s.latestDigest && s.latestDigest !== s.currentDigest && <span className="text-warn-text">→ {shortDigest(s.latestDigest)}</span>}
        </span>
      );
    },
  },
  { id: "ports", header: "Ports", hideBelow: "sm", mono: true, cell: (r) => (r.container ? ports(r.container) || "–" : r.ports.join(", ") || "–") },
];

function rows(p: Project): Row[] {
  return p.services.flatMap((s) =>
    s.containers.length ? s.containers.map((c) => ({ service: s.name, image: s.image, ports: s.ports, container: c })) : [{ service: s.name, image: s.image, ports: s.ports }],
  );
}

/** The Project's images at a glance: an update, not checked yet, or none known. */
function ImagesFact({ project: p }: { project: Project }) {
  const statuses = p.services.flatMap((s) => s.containers.map((c) => c.imageStatus));
  if (p.updateAvailable) return <Pill tone="warn">update available</Pill>;
  if (statuses.some((s) => s?.state === ImageState.UNKNOWN && s.reason === "not checked yet")) return <Pill>not checked yet</Pill>;
  if (statuses.length > 0 && statuses.every((s) => s?.state === ImageState.NOT_CHECKED)) return <Pill>not checked: no registry</Pill>;
  return <Pill tone="up">no update known</Pill>;
}

/** One Compose service's Actions: Start, or Restart and Stop, through the Project's Plan for just that service. */
function ServiceStrip({ project: p, row: r, open, close }: { project: Project; row: Row; open: (d: ReactNode) => void; close: () => void }) {
  const target = { host: p.host, project: p.name, services: [r.service] };
  const running = isRunning(r.container?.container);
  const dialog = (verb: "Start" | "Restart" | "Stop", action: ActionInit) =>
    open(<PlanDialog title={`${verb} ${p.name} (${r.service})`} action={action} confirm={verb} danger={verb !== "Start"} onClose={close} />);
  return (
    <ActionStrip summary={r.container ? `${r.container.container?.name}: ${r.container.container?.status}` : "no container yet"}>
      {running ? (
        <>
          <Button size="sm" variant="danger" disabled={!!p.runId} onClick={() => dialog("Restart", { action: { case: "restart", value: target } })}>
            Restart
          </Button>
          <Button size="sm" variant="danger" disabled={!!p.runId} onClick={() => dialog("Stop", { action: { case: "stop", value: target } })}>
            Stop
          </Button>
        </>
      ) : (
        <Button size="sm" disabled={!!p.runId} onClick={() => dialog("Start", { action: { case: "start", value: target } })}>
          Start
        </Button>
      )}
    </ActionStrip>
  );
}

/** The Project's run in flight, or its last one if it ended with the Page open. */
function useProjectRun(p: Project | undefined): Run | undefined {
  const runs = useQuery(ComposeService.method.listRuns, {});
  const mine = p && runs.data?.runs.filter((r) => r.host === p.host && r.project === p.name);
  const shown = useShownRuns(mine, runs.isFetchedAfterMount, (r) => ({ id: r.id, ended: !!r.ended }));
  return shown[shown.length - 1];
}

function RunPanel({ run }: { run: Run }) {
  const now = useNow();
  const end = ending(run);
  return (
    <Panel title={run.title} description={run.ended ? `Ended ${ago(run.ended, now)}${run.actor ? ` · ${run.actor.replace(/^operator:/, "")}` : ""}` : `Started ${ago(run.started, now)} · it goes on if you leave this Page`}>
      <div className="flex flex-col gap-4">
        {end ? <Notice tone={end.tone}>{end.text}</Notice> : null}
        <PlanSteps steps={run.steps} run={run} />
        {!run.ended && <JobLog run={run} />}
        <CancelRun run={run} />
      </div>
    </Panel>
  );
}

/** A Compose Project's Page (/docker/projects/:host/:name): its Actions, its last run, its Compose services and images, its Compose file. */
export function ProjectPage() {
  const { host = "", name = "" } = useParams();
  const { data, error } = useQuery(ComposeService.method.listProjects, {});
  const p = data?.hosts.find((h) => h.name === host)?.projects.find((x) => x.name === name);
  const run = useProjectRun(p);
  const [dialog, setDialog] = useState<ReactNode>();
  const close = () => setDialog(undefined);
  if (!p) {
    return (
      <div>
        <PageTitle kicker="Compose Project" title={name} back={back} />
        {error && !data ? (
          <Notice tone="down">{error.rawMessage}</Notice>
        ) : !data ? (
          <Skeleton height="lg" />
        ) : (
          <Panel>
            <EmptyState icon="container" title={`${host} has no Compose Project ${name}.`} />
          </Panel>
        )}
      </div>
    );
  }
  const items = projectActions(p, setDialog, close);
  return (
    <div>
      <PageTitle
        kicker="Compose Project"
        title={p.name}
        subtitle={`on ${p.host} · ${p.workingDir}`}
        back={back}
        actions={
          <span className="inline-flex flex-wrap gap-2">
            {items.map((i) => (
              <Button key={i.label} variant={i.danger ? "danger" : i.label === "Update" ? "default" : "outline"} disabled={!!p.runId} onClick={i.run}>
                {i.label}
              </Button>
            ))}
          </span>
        }
      />
      <div className="flex flex-col gap-6">
        <Panel title="Details">
        <Facts
          items={[
            { label: "State", value: <ProjectStatePill project={p} /> },
            { label: "Images", value: <ImagesFact project={p} /> },
            { label: "Protected", value: p.protected ? "yes: Stop, Restart and Update need an override" : "no" },
            { label: "Compose file", value: p.configFile || "unknown", mono: true },
          ]}
        />
        </Panel>
        {p.error && <Notice tone="warn">{p.error}</Notice>}
        {run && <RunPanel run={run} />}
        <Panel title="Compose services" flush>
          <DataTable
            rows={rows(p)}
            rowKey={(r) => r.service + (r.container?.container?.id ?? "")}
            columns={columns}
            empty="The file defines no Compose services"
            rowLabel={(r) => r.service}
            strip={(r) => <ServiceStrip project={p} row={r} open={setDialog} close={close} />}
            busy={() => (p.runId ? "running" : undefined)}
          />
        </Panel>
        <Panel title="Compose file" description="Saved only after `docker compose config` accepts it on the host; the previous file is kept as .bak.">
          <ComposeFiles project={p} />
        </Panel>
      </div>
      {dialog}
    </div>
  );
}
