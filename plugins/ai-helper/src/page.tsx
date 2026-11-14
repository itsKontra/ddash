import { useAction, usePluginLink, useQuery } from "@ddash/plugin";
import { AiHelperService, type Draft } from "@ddash/proto/ai-helper/ai_helper_pb";
import { ComposeService } from "@ddash/proto/compose/compose_pb";
import { Button, CodeBlock, Facts, Field, FormActions, Notice, PageTitle, Panel, Select, Skeleton, TextArea, TextInput } from "@ddash/ui";
import { useState, type FormEvent } from "react";
import { Link } from "react-router";
import { contextOf, draftFragment, prompt, type DraftContext, type DraftJSON } from "./request";

/** What was previewed: the request DraftApp sends once the Operator says so. */
interface Preview {
  description: string;
  context: DraftContext;
}

/** The draft as the forms read it from "#draft=…". */
function draftJSON(d: Draft, host: string): DraftJSON {
  const a = d.app;
  return {
    ...(host && { host }),
    name: d.name,
    compose: d.compose,
    app: { url: a?.url, name: a?.name, icon: a?.icon, group: a?.group, service: a?.service, port: a?.port || undefined },
  };
}

/** Says the helper isn't set up, linking where its API key goes. */
function NotSetUp() {
  return (
    <Notice tone="warn" title="Not set up">
      Enter an Anthropic API key in the AI App helper&apos;s settings, under{" "}
      <Link to="/plugins" className="font-semibold text-primary hover:underline">
        Plugins
      </Link>
      , to draft Apps with Claude.
    </Notice>
  );
}

/** The draft, with the forms it opens in: the Docker Plugin's Add Compose Project and the apps Plugin's Add App, each while installed. */
function DraftPanel({ draft, host }: { draft: Draft; host: string }) {
  const fragment = draftFragment(draftJSON(draft, host));
  const docker = usePluginLink("docker", `projects/new${fragment}`);
  const apps = usePluginLink("apps");
  const a = draft.app;
  return (
    <Panel title="Draft" description={`By ${draft.model}. Check it: nothing has run, and the Plan checks it against the host.`}>
      <div className="flex flex-col gap-4">
        {draft.notes.length > 0 && (
          <Notice tone="info">
            {draft.notes.map((n) => (
              <p key={n}>{n}</p>
            ))}
          </Notice>
        )}
        <Facts
          items={[
            { label: "Compose Project", value: draft.name, mono: true },
            { label: "App", value: a?.name || "—" },
            { label: "URL", value: a?.url || "—", mono: true },
            { label: "Icon", value: a?.icon || "—", mono: true },
            { label: "Group", value: a?.group || "—" },
            { label: "Proxied to", value: a?.service ? `${a.service}${a.port ? `, port ${a.port}` : ""}` : "—", mono: true },
          ]}
        />
        <CodeBlock label="Compose file" copy>
          {draft.compose}
        </CodeBlock>
        {!docker && !apps && <p className="text-sm text-muted-foreground">Install the Docker or Apps Plugin to open the draft in its form.</p>}
        <div className="flex flex-wrap justify-end gap-4 text-sm">
          {docker && (
            <Link to={docker} className="font-semibold text-primary hover:underline">
              Open in Add Compose Project
            </Link>
          )}
          {apps && (
            <Link to={`${apps}?add=compose${fragment}`} className="font-semibold text-primary hover:underline">
              Open in Add App
            </Link>
          )}
        </div>
      </div>
    </Panel>
  );
}

/**
 * The AI App helper's Page: describe the app and pick the Docker host;
 * Preview shows exactly what will go to Anthropic (the description and
 * the host's Compose Project names, used ports and networks, never a
 * file's content); Send to Anthropic drafts; the draft opens in the
 * normal forms. Nothing runs on its own.
 */
export function HelperPage() {
  const status = useQuery(AiHelperService.method.getStatus, {});
  const projects = useQuery(ComposeService.method.listProjects, {});
  const drafting = useAction(AiHelperService.method.draftApp);
  const [description, setDescription] = useState("");
  const [host, setHost] = useState("");
  const [networks, setNetworks] = useState("");
  const [homeDomain, setHomeDomain] = useState("");
  const [preview, setPreview] = useState<Preview>();
  const [draft, setDraft] = useState<{ draft: Draft; host: string }>();
  const hosts = projects.data?.hosts ?? [];
  const setUp = status.data?.setUp;

  /** set, dropping the preview: what is sent changed. */
  const edit = (set: (v: string) => void) => (v: string) => {
    setPreview(undefined);
    set(v);
  };

  const look = (e: FormEvent) => {
    e.preventDefault();
    if (!description.trim()) return;
    setPreview({ description: description.trim(), context: contextOf(hosts.find((h) => h.name === host), networks, homeDomain) });
  };

  const send = async () => {
    if (!preview) return;
    const c = preview.context;
    const res = await drafting.run({
      description: preview.description,
      context: { host: c.host, projects: c.projects, usedPorts: c.used_ports, networks: c.networks, homeDomain: c.home_domain },
    });
    if (res?.draft) setDraft({ draft: res.draft, host: c.host });
  };

  return (
    <div>
      <PageTitle kicker="Services" title="AI App helper" subtitle="Describe an app: Claude drafts its Compose file and the Add App fields for you to review." />
      <div className="flex flex-col gap-4">
        {!status.data && !status.error && <Skeleton height="lg" />}
        {status.error && <Notice tone="down">ai-helper unavailable: {status.error.rawMessage}</Notice>}
        {status.data && !setUp && <NotSetUp />}
        {setUp && (
          <div className="flex flex-col gap-4 xl:flex-row xl:items-start">
            <div className="flex min-w-0 flex-1 flex-col gap-4">
              <Panel title="Describe the app">
                <form onSubmit={look} className="flex flex-col gap-4">
                  <Field label="Description" hint="What to run, as you'd say it. Never paste passwords or keys here: this is sent to Anthropic.">
                    <TextArea value={description} rows={3} placeholder="Paperless-ngx with Postgres and Redis" onChange={(e) => edit(setDescription)(e.target.value)} />
                  </Field>
                  <div className="grid grid-cols-1 gap-4 sm:grid-cols-3">
                    <Field label="Docker host" hint={projects.error ? `Compose Projects can't be listed: ${projects.error.rawMessage}` : "Its Compose Project names and used ports are sent, so the draft avoids them."}>
                      <Select value={host} onChange={(e) => edit(setHost)(e.target.value)}>
                        <option value="">None</option>
                        {hosts.map((h) => (
                          <option key={h.name} value={h.name}>
                            {h.name}
                          </option>
                        ))}
                      </Select>
                    </Field>
                    <Field label="Home domain" hint="The App's URL goes under it, e.g. home.example.">
                      <TextInput value={homeDomain} placeholder="home.example" onChange={(e) => edit(setHomeDomain)(e.target.value)} />
                    </Field>
                    <Field label="Networks" hint="Docker networks it may join, comma-separated; usually none.">
                      <TextInput value={networks} placeholder="proxy" onChange={(e) => edit(setNetworks)(e.target.value)} />
                    </Field>
                  </div>
                  <FormActions>
                    <Button type="submit" disabled={!description.trim()}>
                      Preview what is sent
                    </Button>
                  </FormActions>
                </form>
              </Panel>
              {preview && (
                <Panel title="What is sent to Anthropic" description="Exactly this, under ddash's fixed instructions for a Compose file. No Compose file or .env leaves ddash, and nothing runs.">
                  <div className="flex flex-col gap-4">
                    <CodeBlock label="Sent to Anthropic">{prompt(preview.description, preview.context)}</CodeBlock>
                    {drafting.error && <Notice tone="down">{drafting.error.rawMessage}</Notice>}
                    <FormActions>
                      <Button variant="ghost" onClick={() => setPreview(undefined)} disabled={drafting.pending}>
                        Edit
                      </Button>
                      <Button icon="wand-sparkles" onClick={() => void send()} disabled={drafting.pending}>
                        {drafting.pending ? "Drafting…" : "Send to Anthropic"}
                      </Button>
                    </FormActions>
                  </div>
                </Panel>
              )}
            </div>
            <div className="min-w-0 xl:w-1/2">{draft && <DraftPanel draft={draft.draft} host={draft.host} />}</div>
          </div>
        )}
      </div>
    </div>
  );
}
