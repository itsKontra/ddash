import { useQuery } from "@ddash/plugin";
import { DockerAgentApiService } from "@ddash/proto/docker-agent-api/docker_agent_api_pb";
import { Button, Checkbox, CodeBlock, FormActions, Notice, Skeleton, Tabs, TextArea } from "@ddash/ui";
import { useState, type ReactNode } from "react";
import { PlanDialog } from "./plan";

/** The Compose Project whose files are shown: its Docker host, its name and its Compose file, as ListProjects gives them. */
export interface ComposeProjectFiles {
  host: string;
  name: string;
  /** Empty when the Docker Agent doesn't mount it. */
  configFile: string;
}

type FileTab = "compose" | "env";

/** A file as the editor saves it: an empty sha256 is a .env not there yet. */
interface Base {
  path: string;
  content: string;
  sha256: string;
}

/**
 * A Compose Project's Compose file and its .env: shown, then edited and
 * saved through a Plan (the compose Component validates a Compose file on
 * the host and keeps the previous content as .bak). A Compose Project
 * without a .env offers to add one.
 */
export function ComposeFiles({ project }: { project: ComposeProjectFiles }) {
  if (!project.configFile) return <Notice tone="info">This Compose Project&apos;s file isn&apos;t known: it was started from a path the Docker Agent doesn&apos;t mount.</Notice>;
  return <Editor project={project} />;
}

function Editor({ project: p }: { project: ComposeProjectFiles }) {
  const file = useQuery(DockerAgentApiService.method.getComposeFile, { host: p.host, path: p.configFile });
  const [tab, setTab] = useState<FileTab>("compose");
  const [draft, setDraft] = useState<{ base: Base; text: string }>();
  const [dialog, setDialog] = useState<ReactNode>();

  if (file.error) return <Notice tone="down">{file.error.rawMessage}</Notice>;
  if (!file.data) return <Skeleton height="lg" />;

  const env = file.data.env;
  const envPath = `${p.configFile.slice(0, p.configFile.lastIndexOf("/"))}/.env`;
  const shown: Base | undefined = tab === "env" ? (env ?? { path: envPath, content: "", sha256: "" }) : file.data.file;
  const editing = draft && draft.base.path === shown?.path ? draft : undefined;
  const adding = !env && (tab === "env" || editing?.base.path === envPath);

  const close = () => setDialog(undefined);
  const discard = () => {
    setDraft(undefined);
    if (!env) setTab("compose");
  };
  const save = () => {
    if (!editing) return;
    setDialog(<SaveDialog project={p} file={editing.base} content={editing.text} onClose={close} onDone={() => setDraft(undefined)} />);
  };
  const tabs = [
    { id: "compose" as const, label: file.data.file?.path.split("/").pop() ?? "Compose file" },
    ...(env || adding ? [{ id: "env" as const, label: ".env" }] : []),
  ];
  return (
    <div className="flex flex-col gap-3">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <Tabs label="File" value={tab} options={tabs} onChange={(t) => setTab(t)} />
        {!editing ? (
          <span className="inline-flex flex-wrap gap-2">
            {!env && tab === "compose" && (
              <Button
                size="sm"
                variant="outline"
                icon="plus"
                onClick={() => {
                  setTab("env");
                  setDraft({ base: { path: envPath, content: "", sha256: "" }, text: "" });
                }}
              >
                Add .env
              </Button>
            )}
            <Button size="sm" variant="outline" icon="pencil" onClick={() => shown && setDraft({ base: shown, text: shown.content })}>
              Edit
            </Button>
          </span>
        ) : null}
      </div>
      <p className="font-mono text-xs break-all text-muted-foreground">
        {shown?.path}
        {adding ? " (not there yet)" : ""}
      </p>
      {editing ? (
        <>
          <TextArea
            mono
            rows={Math.min(32, Math.max(12, editing.text.split("\n").length + 2))}
            aria-label={`Edit ${shown?.path}`}
            placeholder={adding ? "TZ=Europe/Berlin\nDB_PASSWORD=…" : undefined}
            value={editing.text}
            onChange={(e) => setDraft({ ...editing, text: e.target.value })}
          />
          {adding && <p className="text-xs text-muted-foreground">Created readable by its owner only. Compose reads it for the Compose file&apos;s variables.</p>}
          <FormActions>
            <Button variant="ghost" onClick={discard}>
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
      {dialog}
    </div>
  );
}

/** Save's Plan: the diff, the validation, and whether to recreate the changed Compose services now. */
function SaveDialog({ project: p, file, content, onClose, onDone }: { project: ComposeProjectFiles; file: Base; content: string; onClose: () => void; onDone: () => void }) {
  const [recreate, setRecreate] = useState(false);
  const name = file.path.split("/").pop();
  return (
    <PlanDialog
      title={file.sha256 ? `Save ${name}` : `Add ${name}`}
      action={{ action: { case: "save", value: { host: p.host, project: p.name, path: file.path, content, expectSha256: file.sha256, recreate } } }}
      confirm={recreate ? "Save and recreate" : "Save"}
      danger={recreate}
      onClose={onClose}
      onDone={onDone}
      extra={<Checkbox label="Recreate the changed Compose services now (docker compose up -d)" checked={recreate} onChange={(e) => setRecreate(e.target.checked)} />}
    />
  );
}
