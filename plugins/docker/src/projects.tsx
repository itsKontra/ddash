import { useAction, usePluginLink, useQuery } from "@ddash/plugin";
import { ComposeService, ProjectState, type Container, type Host, type ImageChecks, type Project } from "@ddash/proto/compose/compose_pb";
import { DockerAgentApiService } from "@ddash/proto/docker-agent-api/docker_agent_api_pb";
import { DockerApiService } from "@ddash/proto/docker-api/docker_api_pb";
import { ago, until } from "@ddash/display";
import { ActionStrip, Button, DataTable, EmptyState, Notice, PageTitle, Panel, Pill, Skeleton, StatusDot, type Column } from "@ddash/ui";
import { useState, type ReactNode } from "react";
import { Link, useNavigate } from "react-router";
import { Host as ProxyHost, tones } from "./containers";
import { containersOf, ImageBadge, ProjectStatePill, stale } from "./images";
import { PlanDialog, type ActionInit } from "./plan";
import { useNow } from "./now";

/** The link to a Compose Project's Page. */
export const projectPath = (host: string, name: string) => `projects/${encodeURIComponent(host)}/${encodeURIComponent(name)}`;

/** The Compose Project Actions a Project offers, as Plan Dialogs. */
export function projectActions(p: Project, open: (d: ReactNode) => void, close: () => void) {
  const target = { host: p.host, project: p.name };
  const dialog = (title: string, action: ActionInit, confirm: string, danger: boolean) =>
    open(<PlanDialog title={title} action={action} confirm={confirm} danger={danger} onClose={close} />);
  const items: { label: string; danger: boolean; run: () => void }[] = [];
  const running = p.state === ProjectState.RUNNING || p.state === ProjectState.PARTIAL;
  if (p.state !== ProjectState.RUNNING) items.push({ label: "Start", danger: false, run: () => dialog(`Start ${p.name}`, { action: { case: "start", value: target } }, "Start", false) });
  if (running) {
    items.push(
      { label: "Restart", danger: true, run: () => dialog(`Restart ${p.name}`, { action: { case: "restart", value: target } }, "Restart", true) },
      { label: "Stop", danger: true, run: () => dialog(`Stop ${p.name}`, { action: { case: "stop", value: target } }, "Stop", true) },
    );
  }
  if (p.state !== ProjectState.NOT_STARTED) {
    items.push({ label: p.updateAvailable ? "Update" : "Pull & update", danger: false, run: () => dialog(`Update ${p.name}`, { action: { case: "update", value: target } }, "Update", false) });
  }
  return items;
}

/** "jellyfin, sonarr" or "3 Compose services". */
function servicesText(p: Project) {
  const names = p.services.map((s) => s.name);
  return names.length <= 3 ? names.join(", ") : `${names.length} Compose services`;
}

/** The image state of a Project at a glance. */
function ProjectImages({ project: p }: { project: Project }) {
  const n = stale(containersOf(p)).length;
  if (n > 0) return <Pill tone="warn">{n === 1 ? "1 update" : `${n} updates`}</Pill>;
  const statuses = containersOf(p).map((c) => c.imageStatus);
  const first = statuses.find((s) => s);
  return <ImageBadge status={first} />;
}

function ProjectStrip({ project: p, open, close }: { project: Project; open: (d: ReactNode) => void; close: () => void }) {
  const items = projectActions(p, open, close);
  const summary = p.error ? p.error : `${p.workingDir}${p.protected ? " · Protected" : ""}`;
  return (
    <ActionStrip summary={summary}>
      {items.map((i) => (
        <Button key={i.label} size="sm" variant={i.danger ? "danger" : "default"} disabled={!!p.runId} onClick={i.run}>
          {i.label}
        </Button>
      ))}
    </ActionStrip>
  );
}

const containerColumns: readonly Column<Container>[] = [
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
  { id: "update", header: "Image state", cell: (c) => <ImageBadge status={c.imageStatus} /> },
  { id: "status", header: "Status", hideBelow: "sm", cell: (c) => c.status },
];

/** A Docker host with an Agent: its Compose Projects, then the containers no Compose Project started. */
function AgentHost({ host: h, open, close }: { host: Host; open: (d: ReactNode) => void; close: () => void }) {
  const all = h.projects.flatMap(containersOf).concat(h.containers);
  const running = all.filter((c) => c.state === "running").length;
  const updates = stale(all).length;
  const columns: readonly Column<Project>[] = [
    {
      id: "name",
      header: "Compose Project",
      rowHeader: true,
      sortable: true,
      sortValue: (p) => p.name,
      cell: (p) => (
        <span className="inline-flex items-center gap-2">
          <Link to={projectPath(p.host, p.name)}>{p.name}</Link>
          {p.protected && <Pill>Protected</Pill>}
        </span>
      ),
    },
    { id: "state", header: "State", cell: (p) => (p.runId ? <Pill tone="maint">running an Action</Pill> : <ProjectStatePill project={p} />) },
    { id: "services", header: "Compose services", hideBelow: "sm", cell: (p) => <span className="text-muted-foreground">{servicesText(p)}</span> },
    { id: "images", header: "Images", cell: (p) => <ProjectImages project={p} /> },
  ];
  return (
    <Panel
      title={h.name}
      description={h.address ? `Docker Agent at ${h.address}` : undefined}
      flush
      actions={
        !h.error && (
          <span className="inline-flex flex-wrap gap-2">
            {updates > 0 && <Pill tone="warn">{updates === 1 ? "1 update" : `${updates} updates`}</Pill>}
            <Pill tone={running === all.length ? "up" : "idle"}>
              {running}/{all.length} running
            </Pill>
          </span>
        )
      }
    >
      {h.error ? (
        <Notice tone="down">{h.error}</Notice>
      ) : (
        <div className="flex flex-col">
          <DataTable
            rows={h.projects}
            rowKey={(p) => p.name}
            columns={columns}
            defaultSort="name"
            empty="No Compose Projects under this host's Compose roots"
            rowLabel={(p) => p.name}
            strip={(p) => <ProjectStrip project={p} open={open} close={close} />}
            busy={(p) => (p.runId ? "running" : undefined)}
          />
          {h.containers.length > 0 && (
            <div className="border-t border-border">
              <p className="px-6 pt-4 pb-2 text-xs font-semibold text-muted-foreground">Not started by Compose</p>
              <DataTable rows={h.containers} rowKey={(c) => c.id} columns={containerColumns} />
            </div>
          )}
        </div>
      )}
    </Panel>
  );
}

/** When the images were last checked, the next check, and Check now. */
function ImageCheckBar({ checks }: { checks?: ImageChecks }) {
  const now = useNow();
  const check = useAction(ComposeService.method.checkImages);
  if (!checks) return null;
  const busy = checks.running || check.pending;
  return (
    <div className="mb-6 flex flex-col gap-2">
      <div className="flex flex-wrap items-center gap-x-3 gap-y-2 text-sm text-muted-foreground">
        <span>
          {busy ? "Checking images against their registries…" : checks.last ? `Images checked ${ago(checks.last, now)}` : "Images not checked yet"}
          {!busy && checks.next && checks.last ? ` · next check ${until(checks.next, now)}` : ""}
        </span>
        <Button size="sm" variant="outline" icon="refresh-cw" disabled={busy} onClick={() => void check.run({})}>
          Check now
        </Button>
      </div>
      {checks.problems.map((p) => (
        <Notice key={p.registry} tone="warn">
          {p.registry}: {p.message}
        </Notice>
      ))}
    </div>
  );
}

/**
 * The docker Page: every Docker host with an Agent and its Compose
 * Projects (state, images, Actions), then the hosts read through a
 * socket-proxy, read-only as before.
 */
export function DockerPage() {
  const projects = useQuery(ComposeService.method.listProjects, {});
  const containers = useQuery(DockerApiService.method.listContainers, {});
  const agents = useQuery(DockerAgentApiService.method.listAgents, {});
  const ai = usePluginLink("ai-helper");
  const navigate = useNavigate();
  const [dialog, setDialog] = useState<ReactNode>();
  const close = () => setDialog(undefined);

  const agentHosts = projects.data?.hosts ?? [];
  const names = new Set(agentHosts.map((h) => h.name));
  const proxyHosts = containers.data?.hosts.filter((h) => !names.has(h.host)) ?? [];
  const enrolling = agents.data?.agents.filter((a) => !names.has(a.name)) ?? [];
  const loading = (!projects.data && !projects.error) || (!containers.data && !containers.error);
  const nothing = !loading && agentHosts.length === 0 && proxyHosts.length === 0 && enrolling.length === 0;

  return (
    <div>
      <PageTitle
        kicker="Infrastructure"
        title="Docker"
        subtitle="Compose Projects and containers on every Docker host."
        actions={
          <span className="inline-flex flex-wrap gap-2">
            {ai && (
              <Button variant="outline" icon="sparkles" onClick={() => void navigate(ai)}>
                Draft with AI
              </Button>
            )}
            <Button variant="outline" icon="server" onClick={() => void navigate("agents")}>
              Docker Agents
            </Button>
            {agentHosts.length > 0 && (
              <Button icon="plus" onClick={() => void navigate("projects/new")}>
                Add Compose Project
              </Button>
            )}
          </span>
        }
      />
      {projects.error && !projects.data && projects.error.rawMessage !== "not configured" && (
        <div className="mb-6">
          <Notice tone="down" title="compose can't list the Compose Projects">
            {projects.error.rawMessage}
          </Notice>
        </div>
      )}
      {agentHosts.length > 0 && <ImageCheckBar checks={projects.data?.checks} />}
      {loading && <Skeleton lines={3} />}
      {nothing && (
        <Panel>
          <EmptyState
            icon="container"
            title="No Docker hosts yet"
            action={
              <Link to="agents" className="text-sm font-semibold text-primary hover:underline">
                Add a Docker Agent
              </Link>
            }
          >
            A Docker Agent on a host lets ddash list, edit, start, stop and update its Compose Projects. A host behind a read-only socket-proxy (docker-api&apos;s config) is only listed.
          </EmptyState>
        </Panel>
      )}
      <div className="flex flex-col gap-6">
        {enrolling.map((a) => (
          <Notice key={a.name} tone="info" title={`${a.name}: waiting for its Docker Agent`}>
            {a.message || "ddash pairs with it as soon as it answers."}{" "}
            <Link to="agents" className="font-semibold text-primary hover:underline">
              Setup
            </Link>
          </Notice>
        ))}
        {agentHosts.map((h) => (
          <AgentHost key={h.name} host={h} open={setDialog} close={close} />
        ))}
        {proxyHosts.map((h) => (
          <ProxyHost key={h.host} host={h} readOnly={agentHosts.length > 0 || enrolling.length > 0} />
        ))}
        {containers.error && !containers.data && <Notice tone="down">{containers.error.message}</Notice>}
      </div>
      {dialog}
    </div>
  );
}
