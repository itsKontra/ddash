import { useAction, useQuery } from "@ddash/plugin";
import { AgentState, DockerAgentApiService, type Agent, type AgentSetup } from "@ddash/proto/docker-agent-api/docker_agent_api_pb";
import { ago } from "@ddash/display";
import { Button, CodeBlock, DataTable, Dialog, EmptyState, Field, FormActions, Notice, PageTitle, Panel, Pill, Skeleton, TextArea, TextInput, type Column, type Tone } from "@ddash/ui";
import { useState, type FormEvent, type ReactNode } from "react";
import { useNow } from "./now";

const back = { href: "..", label: "Docker" };

const stateLook: Record<AgentState, { tone: Tone; label: string }> = {
  [AgentState.UNSPECIFIED]: { tone: "idle", label: "unknown" },
  [AgentState.ENROLLING]: { tone: "maint", label: "waiting to pair" },
  [AgentState.PAIRED]: { tone: "up", label: "paired" },
  [AgentState.OUTDATED]: { tone: "warn", label: "other Release" },
  [AgentState.UNREACHABLE]: { tone: "down", label: "unreachable" },
  [AgentState.FINGERPRINT_CHANGED]: { tone: "down", label: "certificate changed" },
  [AgentState.REFUSED]: { tone: "down", label: "refuses ddash" },
};

/** The fingerprint's first bytes, as pairs: "3f:a9:…". */
const shortFingerprint = (fp: string) =>
  fp
    ? fp
        .slice(0, 16)
        .match(/../g)!
        .join(":") + "…"
    : "–";

/** What every Agent setup says first: the Agent's power. */
function PowerWarning() {
  return (
    <Notice tone="warn" title="The Docker Agent has root-equivalent control of its host">
      It mounts the Docker socket: through it, ddash can read and change every Compose Project under the directories you pick, and start, stop and recreate any container. Install it only on
      hosts ddash should control. Removing it from ddash revokes ddash&apos;s access; uninstall it from the host too.
    </Notice>
  );
}

/** The setup: the Compose file and what to run. */
function Setup({ setup }: { setup: AgentSetup }) {
  return (
    <div className="flex flex-col gap-3">
      <p className="text-sm text-muted-foreground">On the host, save this as compose.yaml in its own directory, then start it. ddash pairs with the Agent within 10 seconds of it answering.</p>
      <CodeBlock label="compose.yaml" copy>
        {setup.composeFile}
      </CodeBlock>
      <CodeBlock label="On the host" copy>
        {setup.commands.join("\n")}
      </CodeBlock>
      {setup.enrolmentCode && (
        <p className="text-xs text-muted-foreground">
          One-time enrolment code: <span className="font-mono text-foreground">{setup.enrolmentCode}</span>. It is ignored once the Agent is paired.
        </p>
      )}
    </div>
  );
}

/** Add a Docker Agent: name, URL and Compose roots, then its setup. */
function AddAgent({ onClose }: { onClose: () => void }) {
  const add = useAction(DockerAgentApiService.method.addAgent);
  const [name, setName] = useState("");
  const [address, setAddress] = useState("");
  const [roots, setRoots] = useState("/opt/stacks");
  const [setup, setSetup] = useState<AgentSetup>();
  const url = address.trim() ? (address.includes("://") ? address.trim() : `https://${address.trim()}:7443`) : "";
  const rootList = roots
    .split(/[\n,]/)
    .map((r) => r.trim())
    .filter(Boolean);
  const submit = async (e: FormEvent) => {
    e.preventDefault();
    const res = await add.run({ name: name.trim(), url, roots: rootList });
    if (res?.setup) setSetup(res.setup);
  };
  return (
    <Dialog
      title={setup ? `Install the Docker Agent on ${name}` : "Add a Docker Agent"}
      onClose={onClose}
      footer={
        <FormActions>
          <Button variant="ghost" onClick={onClose}>
            {setup ? "Done" : "Cancel"}
          </Button>
          {!setup && (
            <Button type="submit" form="add-agent" disabled={!name.trim() || !url || rootList.length === 0 || add.pending}>
              Add and show the setup
            </Button>
          )}
        </FormActions>
      }
    >
      <div className="flex flex-col gap-4">
        <PowerWarning />
        {setup ? (
          <Setup setup={setup} />
        ) : (
          <form id="add-agent" className="flex flex-col gap-4" onSubmit={(e) => void submit(e)}>
            <Field label="Name" hint="How ddash names the host, e.g. media. Workloads on it are named after it.">
              <TextInput value={name} onChange={(e) => setName(e.target.value.toLowerCase())} placeholder="media" autoFocus />
            </Field>
            <Field label="Address" hint="The host's address; ddash reaches the Agent at https://<address>:7443.">
              <TextInput value={address} onChange={(e) => setAddress(e.target.value)} placeholder="198.18.2.10" />
            </Field>
            <Field label="Compose roots" hint="The directories holding your Compose Projects, one per line. Only these are mounted.">
              <TextArea mono rows={3} value={roots} onChange={(e) => setRoots(e.target.value)} />
            </Field>
            {add.error && <Notice tone="down">{add.error.rawMessage}</Notice>}
          </form>
        )}
      </div>
    </Dialog>
  );
}

function SetupDialog({ agent, onClose }: { agent: Agent; onClose: () => void }) {
  const setup = useQuery(DockerAgentApiService.method.getAgentSetup, { name: agent.name });
  return (
    <Dialog title={`Install the Docker Agent on ${agent.name}`} onClose={onClose} footer={<Button onClick={onClose}>Done</Button>}>
      <div className="flex flex-col gap-4">
        <PowerWarning />
        {agent.message && <Notice tone="info">{agent.message}</Notice>}
        {setup.data?.setup ? <Setup setup={setup.data.setup} /> : setup.error ? <Notice tone="down">{setup.error.rawMessage}</Notice> : <Skeleton height="lg" />}
      </div>
    </Dialog>
  );
}

function RemovedDialog({ name, uninstall, onClose }: { name: string; uninstall: readonly string[]; onClose: () => void }) {
  return (
    <Dialog title={`${name} is removed from ddash`} onClose={onClose} footer={<Button onClick={onClose}>Done</Button>}>
      <div className="flex flex-col gap-3">
        <p className="text-sm text-muted-foreground">ddash forgot the Agent&apos;s token, so it can no longer reach it. Uninstall it from the host, so nothing is left listening:</p>
        <CodeBlock label="On the host" copy>
          {uninstall.join("\n")}
        </CodeBlock>
      </div>
    </Dialog>
  );
}

/** Update Agent, for an Agent on another Release than ddash: it recreates itself on ddash's, once confirmed. */
export function UpdateAgent({ agent: a }: { agent: Agent }) {
  const update = useAction(DockerAgentApiService.method.updateAgent);
  return (
    <>
      <Button
        size="sm"
        disabled={update.pending}
        onClick={() =>
          void update.run({ name: a.name }, { confirm: { text: `Update the Docker Agent on ${a.name}? It recreates itself on this Release's image; Compose Projects keep running.`, action: "Update Agent" } })
        }
      >
        Update Agent
      </Button>
      {update.error && <span className="text-xs text-down-text">{update.error.rawMessage}</span>}
    </>
  );
}

/** One Agent's Actions: setup while it enrols, pair again, update, remove. */
function AgentActions({ agent: a, open, close }: { agent: Agent; open: (d: ReactNode) => void; close: () => void }) {
  const pair = useAction(DockerAgentApiService.method.pairAgent);
  const remove = useAction(DockerAgentApiService.method.removeAgent);
  const broken = a.state === AgentState.FINGERPRINT_CHANGED || a.state === AgentState.REFUSED;
  return (
    <span className="inline-flex flex-wrap justify-end gap-2">
      {(a.state === AgentState.ENROLLING || broken) && (
        <Button size="sm" variant="outline" onClick={() => open(<SetupDialog agent={a} onClose={close} />)}>
          Setup
        </Button>
      )}
      {broken && (
        <Button
          size="sm"
          variant="outline"
          disabled={pair.pending}
          onClick={() => void pair.run({ name: a.name, reset: true })}
          title="Forget the pinned certificate and pair with a new enrolment code"
        >
          Pair again
        </Button>
      )}
      {a.state === AgentState.OUTDATED && <UpdateAgent agent={a} />}
      <Button
        size="sm"
        variant="danger"
        disabled={remove.pending}
        onClick={async () => {
          const res = await remove.run({ name: a.name }, { confirm: { text: `Remove the Docker Agent on ${a.name} from ddash? ddash forgets its token; the Agent keeps running until you uninstall it.`, action: "Remove" } });
          if (res) open(<RemovedDialog name={a.name} uninstall={res.uninstall} onClose={close} />);
        }}
      >
        Remove
      </Button>
      {(pair.error ?? remove.error) && <span className="text-xs text-down-text">{(pair.error ?? remove.error)!.rawMessage}</span>}
    </span>
  );
}

/** The Docker Agents Page: every Agent with its pairing state and versions, and adding one. */
export function AgentsPage() {
  const now = useNow();
  const { data, error } = useQuery(DockerAgentApiService.method.listAgents, {});
  const [dialog, setDialog] = useState<ReactNode>();
  const close = () => setDialog(undefined);
  const columns: readonly Column<Agent>[] = [
    {
      id: "name",
      header: "Host",
      rowHeader: true,
      cell: (a) => (
        <span className="flex flex-col">
          <span className="font-semibold">{a.name}</span>
          <span className="font-mono text-xs text-muted-foreground">{a.url}</span>
        </span>
      ),
    },
    {
      id: "state",
      header: "State",
      cell: (a) => (
        <span className="flex flex-col gap-1">
          <span>
            <Pill tone={stateLook[a.state].tone} dot>
              {stateLook[a.state].label}
            </Pill>
          </span>
          {a.message && <span className="max-w-xs text-xs text-muted-foreground">{a.message}</span>}
        </span>
      ),
    },
    {
      id: "versions",
      header: "Versions",
      hideBelow: "sm",
      cell: (a) =>
        a.release ? (
          <span className="flex flex-col text-xs">
            <span>Agent {a.release}</span>
            {a.dockerVersion && (
              <span className="text-muted-foreground">
                Docker {a.dockerVersion}
                {a.composeVersion ? ` · Compose ${a.composeVersion}` : ""}
              </span>
            )}
          </span>
        ) : (
          <span className="text-muted-foreground">–</span>
        ),
    },
    { id: "roots", header: "Compose roots", hideBelow: "sm", mono: true, cell: (a) => a.roots.join(", ") },
    {
      id: "fingerprint",
      header: "Certificate",
      hideBelow: "sm",
      cell: (a) => (
        <span className="flex flex-col text-xs" title={a.fingerprint}>
          <span className="font-mono">{shortFingerprint(a.fingerprint)}</span>
          {a.paired && <span className="text-muted-foreground">paired {ago(a.paired, now)}</span>}
        </span>
      ),
    },
    { id: "actions", header: "", align: "right", cell: (a) => <AgentActions agent={a} open={setDialog} close={close} /> },
  ];
  return (
    <div>
      <PageTitle
        kicker="Docker"
        title="Docker Agents"
        subtitle="The Agents that let ddash manage each Docker host's Compose Projects."
        back={back}
        actions={
          <Button icon="plus" onClick={() => setDialog(<AddAgent onClose={close} />)}>
            Add a Docker Agent
          </Button>
        }
      />
      {error && !data ? (
        <Notice tone="down">{error.rawMessage}</Notice>
      ) : (
        <Panel flush>
          <DataTable
            rows={data?.agents ?? []}
            rowKey={(a) => a.name}
            columns={columns}
            loading={!data}
            empty={
              <EmptyState icon="server" title="No Docker Agents yet">
                Add one per Docker host ddash should manage. Hosts read through a socket-proxy stay listed, read-only.
              </EmptyState>
            }
          />
        </Panel>
      )}
      {dialog}
    </div>
  );
}
