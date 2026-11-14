import { useAction, useQuery } from "@ddash/plugin";
import { AgentState, HostAgentApiService, type Agent, type AgentSetup } from "@ddash/proto/host-agent-api/host_agent_api_pb";
import type { Workload } from "@ddash/proto/proxmox-api/proxmox_api_pb";
import { Button, CodeBlock, Facts, Field, FormActions, Notice, Pill, Skeleton, TextInput, type Tone } from "@ddash/ui";
import { useState, type ReactNode } from "react";
import { workloadLabel } from "./power";

// A Workload's Host Agent: adding it (the install command with its
// one-time enrolment code), its pairing, Update Agent when it runs another
// Release, and removing it with how to uninstall it.

/** The port a Host Agent serves on. */
export const hostAgentPort = 7444;

/** The Workload's Host Agent as host-agent-api lists it, undefined while unknown, null when it has none. */
export function useHostAgent(w: Workload): Agent | null | undefined {
  const { data } = useQuery(HostAgentApiService.method.listAgents, {});
  if (!data) return undefined;
  return data.agents.find((a) => a.vmid === w.vmid) ?? null;
}

/** "https://198.18.1.10:7444": the Workload's first address, where its Host Agent answers. */
export function agentURL(w: Workload): string {
  const ip = w.nics.flatMap((n) => n.ips).find((a) => !a.includes(":")) ?? w.nics.flatMap((n) => n.ips)[0];
  if (!ip) return "";
  return `https://${ip.includes(":") ? `[${ip}]` : ip}:${hostAgentPort}`;
}

const stateLook: Record<AgentState, { tone: Tone; label: string }> = {
  [AgentState.UNSPECIFIED]: { tone: "idle", label: "unknown" },
  [AgentState.ENROLLING]: { tone: "maint", label: "waiting to pair" },
  [AgentState.PAIRED]: { tone: "up", label: "paired" },
  [AgentState.OUTDATED]: { tone: "warn", label: "outdated" },
  [AgentState.UNREACHABLE]: { tone: "down", label: "unreachable" },
  [AgentState.FINGERPRINT_CHANGED]: { tone: "down", label: "certificate changed" },
  [AgentState.REFUSED]: { tone: "down", label: "refuses ddash" },
};

/** The Agent's state as a Pill: "paired", "waiting to pair". */
export const AgentPill = ({ agent }: { agent: Agent }) => (
  <Pill tone={stateLook[agent.state].tone} dot>
    {stateLook[agent.state].label}
  </Pill>
);

const rootWarning =
  "The Host Agent runs as root: it has root-equivalent control of this Workload. Through it ddash updates its packages and changes its SSH settings. Install it only where ddash should have that power.";

/** The "Host Agent" Panel's body. */
export function HostAgent({ workload: w }: { workload: Workload }) {
  const agent = useHostAgent(w);
  // What the last Add or Remove answered: kept here, as the list catches up after.
  const [added, setAdded] = useState<AgentSetup>();
  const [removed, setRemoved] = useState<string[]>();
  if (agent === undefined) return <Skeleton height="sm" />;
  if (agent) return <Paired agent={agent} workload={w} onRemoved={setRemoved} />;
  if (added) return <Setup setup={added} />;
  return (
    <div className="flex flex-col gap-4">
      {removed && <Uninstall commands={removed} />}
      <AddForm workload={w} onAdded={setAdded} />
    </div>
  );
}

function AddForm({ workload: w, onAdded }: { workload: Workload; onAdded: (s: AgentSetup) => void }) {
  const [url, setURL] = useState(() => agentURL(w));
  const add = useAction(HostAgentApiService.method.addAgent);
  const go = async () => {
    const res = await add.run({ vmid: w.vmid, name: w.name, url: url.trim() });
    if (res?.setup) onAdded(res.setup);
  };
  return (
    <form
      className="flex flex-col gap-4"
      onSubmit={(e) => {
        e.preventDefault();
        void go();
      }}
    >
      <p className="text-sm text-muted-foreground">
        With a Host Agent on {workloadLabel(w)}, ddash updates its packages, takes a ddash Snapshot first, and sets up SSH, all without logging in. It supports Ubuntu, Debian,
        Alpine, Fedora and Arch Linux.
      </p>
      <Notice tone="warn" title="Root-equivalent control">
        {rootWarning}
      </Notice>
      <Field label="Agent URL" hint={`Where ddash reaches it: the Workload's address, port ${hostAgentPort}.`}>
        <TextInput value={url} onChange={(e) => setURL(e.target.value)} placeholder={`https://198.18.1.10:${hostAgentPort}`} />
      </Field>
      {add.error && <Notice tone="down">{add.error.rawMessage}</Notice>}
      <FormActions>
        <Button type="submit" disabled={!url.trim() || add.pending}>
          Add Host Agent
        </Button>
      </FormActions>
    </form>
  );
}

/** How to install the Agent: the one-line command with its code, and the script and checksums to verify first. */
function Setup({ setup }: { setup: AgentSetup }) {
  return (
    <div className="flex flex-col gap-3">
      <p className="text-sm">Run this on the Workload as root. ddash pairs with the Agent within a minute of it starting.</p>
      {setup.installCommand && (
        <CodeBlock label="Install command" copy>
          {setup.installCommand}
        </CodeBlock>
      )}
      <p className="text-xs text-muted-foreground">
        The enrolment code in it works once. Read the script before you run it:{" "}
        <a className="text-primary hover:underline" href={setup.scriptUrl} target="_blank" rel="noreferrer">
          install.sh
        </a>
        . It checks the download against these{" "}
        <a className="text-primary hover:underline" href={setup.checksumsUrl} target="_blank" rel="noreferrer">
          SHA-256 sums
        </a>
        .
      </p>
      <Notice tone="warn">{rootWarning}</Notice>
    </div>
  );
}

function Uninstall({ commands }: { commands: readonly string[] }) {
  return (
    <Notice tone="info" title="Removed from ddash">
      <p className="mb-2">ddash no longer reaches it. To uninstall it, run this on the Workload as root:</p>
      <CodeBlock label="Uninstall command" copy>
        {commands.join("\n")}
      </CodeBlock>
    </Notice>
  );
}

function Paired({ agent: a, workload: w, onRemoved }: { agent: Agent; workload: Workload; onRemoved: (commands: string[]) => void }) {
  const pair = useAction(HostAgentApiService.method.pairAgent);
  const update = useAction(HostAgentApiService.method.updateAgent);
  const remove = useAction(HostAgentApiService.method.removeAgent);
  const error = pair.error ?? update.error ?? remove.error;
  const busy = pair.pending || update.pending || remove.pending;

  const actions: ReactNode[] = [];
  switch (a.state) {
    case AgentState.ENROLLING:
      actions.push(
        <Button key="pair" variant="outline" disabled={busy} onClick={() => void pair.run({ vmid: a.vmid })}>
          Pair now
        </Button>,
      );
      break;
    case AgentState.OUTDATED:
      actions.push(
        <Button
          key="update"
          disabled={busy}
          onClick={() =>
            void update.run(
              { vmid: a.vmid },
              { confirm: { text: `Update the Host Agent on ${workloadLabel(w)}? It downloads this Release's Agent from ddash, checks its SHA-256 and restarts.`, action: "Update Agent" } },
            )
          }
        >
          Update Agent
        </Button>,
      );
      break;
    case AgentState.FINGERPRINT_CHANGED:
    case AgentState.REFUSED:
      actions.push(
        <Button key="repair" variant="outline" disabled={busy} onClick={() => void pair.run({ vmid: a.vmid, reset: true })}>
          Pair again
        </Button>,
      );
      break;
  }
  actions.push(
    <Button
      key="remove"
      variant="danger"
      disabled={busy}
      onClick={async () => {
        const res = await remove.run(
          { vmid: a.vmid },
          { confirm: { text: `Remove the Host Agent of ${workloadLabel(w)} from ddash? ddash then tells you how to uninstall it.`, action: "Remove" } },
        );
        if (res) onRemoved(res.uninstall);
      }}
    >
      Remove
    </Button>,
  );

  const facts = [
    { label: "State", value: <AgentPill agent={a} /> },
    { label: "URL", value: a.url, mono: true },
    ...(a.os?.pretty ? [{ label: "OS", value: a.os.pretty }] : []),
    ...(a.hostname ? [{ label: "Hostname", value: a.hostname }] : []),
    ...(a.release ? [{ label: "Release", value: a.release }] : []),
    ...(a.fingerprint ? [{ label: "Certificate", value: a.fingerprint, mono: true }] : []),
  ];
  return (
    <div className="flex flex-col gap-4">
      <Facts items={facts} />
      {a.message && <Notice tone={a.state === AgentState.ENROLLING ? "info" : "warn"}>{a.message}</Notice>}
      {a.state === AgentState.OUTDATED && <p className="text-sm text-muted-foreground">It runs another Release than ddash: Update Agent is the only thing ddash does with it until then.</p>}
      {a.state === AgentState.ENROLLING && <EnrollingSetup vmid={a.vmid} />}
      {error && <Notice tone="down">{error.rawMessage}</Notice>}
      <div className="flex flex-wrap gap-2">{actions}</div>
    </div>
  );
}

function EnrollingSetup({ vmid }: { vmid: number }) {
  const { data, error } = useQuery(HostAgentApiService.method.getAgentSetup, { vmid });
  if (error) return <Notice tone="down">{error.rawMessage}</Notice>;
  if (!data?.setup) return <Skeleton height="sm" />;
  return <Setup setup={data.setup} />;
}
