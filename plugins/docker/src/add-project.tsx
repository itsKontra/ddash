import { PlanDialog } from "@ddash/compose-ui";
import { usePluginLink, useQuery } from "@ddash/plugin";
import { ComposeService } from "@ddash/proto/compose/compose_pb";
import { Button, Checkbox, Field, FormActions, Notice, PageTitle, Panel, Select, Skeleton, TextArea, TextInput } from "@ddash/ui";
import { useState, type FormEvent, type ReactNode } from "react";
import { Link, useLocation, useNavigate } from "react-router";
import { projectPath } from "./projects";

const back = { href: "..", label: "Docker" };

/** A draft handed over in the URL's fragment, e.g. by the AI App helper: #draft=<base64url JSON>. */
export interface Draft {
  host?: string;
  name?: string;
  directory?: string;
  compose?: string;
  env?: string;
}

/** Reads #draft=… : base64url of the draft's JSON; undefined when absent or unreadable. */
export function readDraft(hash: string): Draft | undefined {
  const m = /(?:^#|&)draft=([^&]+)/.exec(hash);
  if (!m) return undefined;
  try {
    const b64 = m[1].replace(/-/g, "+").replace(/_/g, "/");
    const bytes = Uint8Array.from(atob(b64), (c) => c.charCodeAt(0));
    const d = JSON.parse(new TextDecoder().decode(bytes)) as unknown;
    return d && typeof d === "object" ? (d as Draft) : undefined;
  } catch {
    return undefined;
  }
}

const starter = `services:
  app:
    image: nginx:1.27
    restart: unless-stopped
    ports: ["8080:80"]
`;

/** Add Compose Project (/docker/projects/new): host, name, directory, Compose file and .env, then a Plan that validates them on the host. */
export function AddProjectPage() {
  const { data, error } = useQuery(ComposeService.method.listProjects, {});
  const location = useLocation();
  const navigate = useNavigate();
  const ai = usePluginLink("ai-helper");
  const [draft] = useState(() => readDraft(location.hash));
  const hosts = data?.hosts.filter((h) => !h.error) ?? [];
  const [host, setHost] = useState(draft?.host ?? "");
  const [name, setName] = useState(draft?.name ?? "");
  const [directory, setDirectory] = useState(draft?.directory ?? "");
  const [content, setContent] = useState(draft?.compose ?? starter);
  const [env, setEnv] = useState(typeof draft?.env === "string" ? draft.env : "");
  const [start, setStart] = useState(true);
  const [dialog, setDialog] = useState<ReactNode>();

  const chosen = hosts.find((h) => h.name === host) ?? hosts[0];
  const root = chosen?.roots[0]?.replace(/\/$/, "") ?? "";
  const dir = directory || (root && name ? `${root}/${name}` : "");

  const submit = (e: FormEvent) => {
    e.preventDefault();
    if (!chosen) return;
    setDialog(
      <PlanDialog
        title={`Add ${name}`}
        action={{ action: { case: "add", value: { host: chosen.name, name, directory: dir, content, env, start } } }}
        confirm={start ? "Add and start" : "Add"}
        onClose={() => setDialog(undefined)}
        onDone={() => void navigate(`../../${projectPath(chosen.name, name)}`, { relative: "path" })}
      />,
    );
  };

  return (
    <div>
      <PageTitle
        kicker="Docker"
        title="Add Compose Project"
        subtitle="A new Compose Project on a host with a Docker Agent."
        back={back}
        actions={
          ai && (
            <Button variant="outline" icon="sparkles" onClick={() => void navigate(ai)}>
              Draft with AI
            </Button>
          )
        }
      />
      {error && !data && <Notice tone="down">{error.rawMessage}</Notice>}
      {!data && !error && <Skeleton height="lg" />}
      {data && hosts.length === 0 && (
        <Notice tone="info" title="No Docker Agent is paired">
          A new Compose Project needs a host with a Docker Agent. <Link to="../agents">Add a Docker Agent</Link>
        </Notice>
      )}
      {draft && <div className="mb-6"><Notice tone="info">This is a draft: check every line before you add it. Nothing runs until you confirm its Plan.</Notice></div>}
      {chosen && (
        <Panel>
          <form className="flex flex-col gap-4" onSubmit={submit}>
            <div className="grid gap-4 sm:grid-cols-2">
              <Field label="Host">
                <Select value={chosen.name} onChange={(e) => setHost(e.target.value)}>
                  {hosts.map((h) => (
                    <option key={h.name} value={h.name}>
                      {h.name}
                    </option>
                  ))}
                </Select>
              </Field>
              <Field label="Name" hint="Lower-case letters, digits, - and _: the Compose project's name.">
                <TextInput value={name} onChange={(e) => setName(e.target.value.toLowerCase().replace(/[^a-z0-9_-]/g, ""))} placeholder="paperless" />
              </Field>
            </div>
            <Field label="Directory" hint={`Under ${chosen.roots.join(" or ")}. It is created with compose.yaml in it.`}>
              <TextInput value={dir} onChange={(e) => setDirectory(e.target.value)} placeholder={`${root}/<name>`} />
            </Field>
            <Field label="Compose file" hint="Checked with `docker compose config` on the host before anything is written.">
              <TextArea mono rows={18} value={content} onChange={(e) => setContent(e.target.value)} />
            </Field>
            <Field label=".env" hint="Optional. Written beside compose.yaml, readable by its owner only; Compose reads it for the Compose file's variables.">
              <TextArea mono rows={5} value={env} placeholder={"TZ=Europe/Berlin\nDB_PASSWORD=…"} onChange={(e) => setEnv(e.target.value)} />
            </Field>
            <Checkbox label="Start it once it's added (docker compose up -d)" checked={start} onChange={(e) => setStart(e.target.checked)} />
            <FormActions>
              <Button variant="ghost" type="button" onClick={() => void navigate("../..", { relative: "path" })}>
                Cancel
              </Button>
              <Button type="submit" disabled={!name || !dir || !content.trim()}>
                Review the Plan
              </Button>
            </FormActions>
          </form>
        </Panel>
      )}
      {dialog}
    </div>
  );
}
