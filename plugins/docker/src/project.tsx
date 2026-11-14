import { useQuery } from "@ddash/plugin";
import { ComposeService, type Container, type Project, type Run } from "@ddash/proto/compose/compose_pb";
import { DockerAgentApiService, type ComposeFile } from "@ddash/proto/docker-agent-api/docker_agent_api_pb";
import { ago } from "@ddash/display";
import { Button, CodeBlock, DataTable, EmptyState, Facts, FormActions, Notice, PageTitle, Panel, Pill, Skeleton, StatusDot, Switch, Tabs, TextArea, type Column } from "@ddash/ui";
import { useState, type ReactNode } from "react";
import { useParams } from "react-router";
import { tones } from "./containers";
import { ImageBadge, ProjectStatePill, shortDigest } from "./images";
import { useNow } from "./now";
import { ending, JobLog, PlanDialog, PlanSteps } from "./plan";
import { projectActions } from "./projects";

// Relative to the route, so ".." is the Plugin's root.
const back = { href: "..", label: "Docker" };

interface Row {
  service: string;
  image: string;
  ports: string[];
  container?: Container;
}

const ports = (c: Container) => c.ports.map((p) => `${p.ip ? `${p.ip}:` : ""}${p.publicPort}→${p.privatePort}/${p.protocol}`).join(", ");

const columns: readonly Column<Row>[] = [
  { id: "service", header: "Compose service", rowHeader: true, cell: (r) => r.service },
  {
    id: "state",
    header: "Container",
    cell: (r) =>
      r.container ? (
        <span className="inline-flex items-center gap-2">
          <StatusDot tone={tones[r.container.state] ?? "idle"} label={r.container.state} />
          <span>
            {r.container.name}
            <span className="block text-xs text-muted-foreground">{r.container.status}</span>
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
        <span className="font-mono text-xs break-all">{r.container?.image ?? r.image}</span>
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

/** The Project's run in flight, or its last one in the past 15 minutes. */
function useProjectRun(p: Project | undefined): Run | undefined {
  const runs = useQuery(ComposeService.method.listRuns, {});
  if (!p) return undefined;
  const mine = runs.data?.runs.filter((r) => r.host === p.host && r.project === p.name) ?? [];
  return mine[mine.length - 1];
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
      </div>
    </Panel>
  );
}

type FileTab = "compose" | "env";

/** The Compose file (and .env) viewer and editor: Edit, then Save through a Plan that validates it on the host. */
function FileEditor(props: { project: Project; open: (d: ReactNode) => void; close: () => void }) {
  if (!props.project.configFile) return <Notice tone="info">This Compose Project&apos;s file isn&apos;t known: it was started from a path the Docker Agent doesn&apos;t mount.</Notice>;
  return <Editor {...props} />;
}

function Editor({ project: p, open, close }: { project: Project; open: (d: ReactNode) => void; close: () => void }) {
  const file = useQuery(DockerAgentApiService.method.getComposeFile, { host: p.host, path: p.configFile });
  const [tab, setTab] = useState<FileTab>("compose");
  const [draft, setDraft] = useState<{ path: string; base: ComposeFile; text: string }>();
  const shown = tab === "env" ? file.data?.env : file.data?.file;
  const editing = draft && draft.path === shown?.path ? draft : undefined;

  if (file.error) return <Notice tone="down">{file.error.rawMessage}</Notice>;
  if (!file.data) return <Skeleton height="lg" />;

  const save = () => {
    if (!editing) return;
    open(<SaveDialog project={p} file={editing.base} content={editing.text} onClose={close} onDone={() => setDraft(undefined)} />);
  };
  const tabs = [
    { id: "compose" as const, label: file.data.file?.path.split("/").pop() ?? "Compose file" },
    ...(file.data.env ? [{ id: "env" as const, label: ".env" }] : []),
  ];
  return (
    <div className="flex flex-col gap-3">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <Tabs label="File" value={tab} options={tabs} onChange={(t) => setTab(t)} />
        {!editing ? (
          <Button size="sm" variant="outline" icon="pencil" onClick={() => shown && setDraft({ path: shown.path, base: shown, text: shown.content })}>
            Edit
          </Button>
        ) : null}
      </div>
      <p className="font-mono text-xs break-all text-muted-foreground">{shown?.path}</p>
      {editing ? (
        <>
          <TextArea mono rows={Math.min(32, Math.max(12, editing.text.split("\n").length + 2))} aria-label={`Edit ${shown?.path}`} value={editing.text} onChange={(e) => setDraft({ ...editing, text: e.target.value })} />
          <FormActions>
            <Button variant="ghost" onClick={() => setDraft(undefined)}>
              Discard
            </Button>
            <Button disabled={editing.text === editing.base.content} onClick={save}>
              Save…
            </Button>
          </FormActions>
        </>
      ) : (
        <CodeBlock label={shown?.path} copy>
          {shown?.content ?? ""}
        </CodeBlock>
      )}
    </div>
  );
}

/** Save's Plan: the diff, the validation, and whether to recreate the changed Compose services now. */
function SaveDialog({ project: p, file, content, onClose, onDone }: { project: Project; file: ComposeFile; content: string; onClose: () => void; onDone: () => void }) {
  const [recreate, setRecreate] = useState(false);
  return (
    <PlanDialog
      title={`Save ${file.path.split("/").pop()}`}
      action={{ action: { case: "save", value: { host: p.host, project: p.name, path: file.path, content, expectSha256: file.sha256, recreate } } }}
      confirm={recreate ? "Save and recreate" : "Save"}
      danger={recreate}
      onClose={onClose}
      onDone={onDone}
      extra={<Switch label="Recreate the changed Compose services now" checked={recreate} onChange={setRecreate} />}
    />
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
        <Facts
          items={[
            { label: "State", value: <ProjectStatePill project={p} /> },
            { label: "Images", value: p.updateAvailable ? <Pill tone="warn">update available</Pill> : <Pill tone="up">no update known</Pill> },
            { label: "Protected", value: p.protected ? "yes: Stop, Restart and Update need an override" : "no" },
            { label: "Compose file", value: p.configFile || "unknown", mono: true },
          ]}
        />
        {p.error && <Notice tone="warn">{p.error}</Notice>}
        {run && <RunPanel run={run} />}
        <Panel title="Compose services" flush>
          <DataTable rows={rows(p)} rowKey={(r) => r.service + (r.container?.id ?? "")} columns={columns} empty="The file defines no Compose services" />
        </Panel>
        <Panel title="Compose file" description="Saved only after `docker compose config` accepts it on the host; the previous file is kept as .bak.">
          <FileEditor project={p} open={setDialog} close={close} />
        </Panel>
      </div>
      {dialog}
    </div>
  );
}
