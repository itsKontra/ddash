import { useAction, useQuery } from "@ddash/plugin";
import { HostAgentApiService, type AuthorizedKey, type SSHState } from "@ddash/proto/host-agent-api/host_agent_api_pb";
import type { Workload } from "@ddash/proto/proxmox-api/proxmox_api_pb";
import { ProxmoxService } from "@ddash/proto/proxmox/proxmox_pb";
import { Button, Checkbox, DataTable, Facts, Notice, Pill, Select, Skeleton, Switch, type Column } from "@ddash/ui";
import { useState } from "react";
import { Link } from "react-router";
import { refOf, workloadLabel } from "./power";
import { ended, JobSteps } from "./steps";

// A Workload's SSH, through its Host Agent: sshd's state and settings, the
// keys in its users' authorized_keys (ddash's and others), installing
// stored SSH Keys and removing ddash's, enabling sshd, and turning
// password and root login off (which the Agent refuses when it would lock
// everyone out).

const rootLogins = [
  { value: "prohibit-password", label: "with a key only" },
  { value: "no", label: "no" },
  { value: "yes", label: "yes, with a password too" },
];

/** The "SSH" Panel's body, for a Workload with a paired Host Agent. */
export function SSH({ workload: w }: { workload: Workload }) {
  const { data, error } = useQuery(HostAgentApiService.method.getSSH, { vmid: w.vmid });
  if (error && !data) return <Notice tone="down">{error.rawMessage}</Notice>;
  if (!data?.ssh) return <Skeleton height="sm" />;
  return <SSHState workload={w} ssh={data.ssh} />;
}

function SSHState({ workload: w, ssh }: { workload: Workload; ssh: SSHState }) {
  const enable = useAction(ProxmoxService.method.enableSSH);
  const settings = useAction(HostAgentApiService.method.setSSHSettings);
  const [job, setJob] = useState<string>();
  const set = (password: boolean, root: string) =>
    void settings.run(
      { vmid: w.vmid, passwordAuthentication: password, permitRootLogin: root },
      {
        confirm: {
          text: `Set ${workloadLabel(w)}'s sshd to password login ${password ? "on" : "off"} and root login "${root}"? The Host Agent checks it with sshd -t first, then reloads sshd: open sessions stay.`,
          action: "Apply",
        },
      },
    );
  const facts = [
    { label: "sshd", value: <SSHDState ssh={ssh} /> },
    ...(ssh.installed
      ? [
          {
            label: "Password login",
            value: (
              <span className="inline-flex items-center gap-2">
                <Switch label="Password login" checked={ssh.passwordAuthentication} disabled={settings.pending} onChange={(on) => set(on, ssh.permitRootLogin || "prohibit-password")} />
                {ssh.passwordAuthentication ? "on" : "off"}
              </span>
            ),
          },
          {
            label: "Root login",
            value: (
              <Select label="Root login" value={ssh.permitRootLogin} disabled={settings.pending} onChange={(e) => set(ssh.passwordAuthentication, e.target.value)}>
                {!rootLogins.some((r) => r.value === ssh.permitRootLogin) && <option value={ssh.permitRootLogin}>{ssh.permitRootLogin || "unknown"}</option>}
                {rootLogins.map((r) => (
                  <option key={r.value} value={r.value}>
                    {r.label}
                  </option>
                ))}
              </Select>
            ),
          },
        ]
      : []),
  ];
  return (
    <div className="flex flex-col gap-4">
      <Facts items={facts} />
      {(!ssh.installed || !ssh.running) && (
        <div className="flex flex-col gap-2">
          <p className="text-sm text-muted-foreground">{ssh.installed ? "sshd is installed but not running." : "sshd isn't installed."} Enabling installs OpenSSH where it&apos;s missing and starts sshd at boot.</p>
          <div>
            <Button
              disabled={enable.pending || job !== undefined}
              onClick={async () => {
                const res = await enable.run({ workload: refOf(w) });
                if (res?.job) setJob(res.job.id);
              }}
            >
              Enable sshd
            </Button>
          </div>
        </div>
      )}
      {job && <EnableProgress vmid={w.vmid} id={job} onEnded={() => setJob(undefined)} />}
      {enable.error && <Notice tone="down">{enable.error.rawMessage}</Notice>}
      {settings.error && (
        <Notice tone="down" title="The Host Agent refused">
          {settings.error.rawMessage}
        </Notice>
      )}
      {!ssh.passwordAuthentication && ssh.installed && <p className="text-xs text-muted-foreground">Password login is off: only an installed key lets anyone in.</p>}
      <Keys workload={w} ssh={ssh} />
      <Install workload={w} ssh={ssh} />
    </div>
  );
}

function SSHDState({ ssh }: { ssh: SSHState }) {
  if (!ssh.installed) return <Pill>not installed</Pill>;
  return (
    <span className="inline-flex flex-wrap gap-1.5">
      <Pill tone={ssh.running ? "up" : "idle"} dot>
        {ssh.running ? "running" : "stopped"}
      </Pill>
      <Pill tone={ssh.enabled ? "info" : "idle"}>{ssh.enabled ? "starts at boot" : "not at boot"}</Pill>
    </span>
  );
}

/** The Enable sshd job's steps, from the Host Agent, until it ended. */
function EnableProgress({ vmid, id, onEnded }: { vmid: number; id: string; onEnded: () => void }) {
  const { data } = useQuery(HostAgentApiService.method.listJobs, {});
  const job = data?.hosts.find((h) => h.vmid === vmid)?.jobs.find((j) => j.id === id);
  if (!job) return null;
  return (
    <div className="flex flex-col gap-2">
      <JobSteps steps={job.steps} />
      {job.error && <Notice tone="down">{job.error}</Notice>}
      {ended(job.state) && (
        <div>
          <Button size="sm" variant="ghost" onClick={onEnded}>
            Dismiss
          </Button>
        </div>
      )}
    </div>
  );
}

function Keys({ workload: w, ssh }: { workload: Workload; ssh: SSHState }) {
  const remove = useAction(HostAgentApiService.method.removeKey);
  const columns: readonly Column<AuthorizedKey>[] = [
    { id: "user", header: "User", rowHeader: true, cell: (k) => k.user },
    { id: "name", header: "SSH Key", cell: (k) => k.name || <span className="text-muted-foreground">not from ddash</span> },
    { id: "type", header: "Type", hideBelow: "sm", cell: (k) => k.type },
    { id: "fingerprint", header: "Fingerprint", mono: true, cell: (k) => k.fingerprint },
    { id: "comment", header: "Comment", hideBelow: "sm", cell: (k) => k.comment || "–" },
    {
      id: "remove",
      header: "",
      align: "right",
      cell: (k) =>
        k.name ? (
          <Button
            size="sm"
            variant="danger"
            disabled={remove.pending}
            onClick={() =>
              void remove.run(
                { vmid: w.vmid, user: k.user, name: k.name },
                { confirm: { text: `Remove the SSH Key ${k.name} from ${k.user} on ${workloadLabel(w)}?`, action: "Remove" } },
              )
            }
          >
            Remove
          </Button>
        ) : null,
    },
  ];
  return (
    <div className="flex flex-col gap-2">
      <p className="text-sm font-semibold">Installed keys</p>
      <DataTable rows={ssh.keys} rowKey={(k) => `${k.user} ${k.fingerprint}`} columns={columns} empty="No keys in authorized_keys" />
      {remove.error && <Notice tone="down">{remove.error.rawMessage}</Notice>}
    </div>
  );
}

function Install({ workload: w, ssh }: { workload: Workload; ssh: SSHState }) {
  const keys = useQuery(ProxmoxService.method.listSSHKeys, {});
  const install = useAction(ProxmoxService.method.installSSHKey);
  const [user, setUser] = useState("");
  const [names, setNames] = useState<string[]>([]);
  const [already, setAlready] = useState<string[]>();
  const users = ssh.users.length ? ssh.users : ["root"];
  const picked = user || users[0];
  const stored = keys.data?.keys ?? [];
  const toggle = (name: string, on: boolean) => setNames((ns) => (on ? [...ns, name] : ns.filter((n) => n !== name)));
  return (
    <div className="flex flex-col gap-2">
      <p className="text-sm font-semibold">Install SSH Keys</p>
      {keys.data && !stored.length ? (
        <p className="text-sm text-muted-foreground">
          ddash stores no SSH Key yet: <Link to="../ssh-keys">add one on the SSH Keys Page</Link>.
        </p>
      ) : (
        <>
          <div className="flex flex-wrap gap-3">
            {stored.map((k) => (
              <Checkbox key={k.name} label={k.name} checked={names.includes(k.name)} onChange={(e) => toggle(k.name, e.target.checked)} />
            ))}
          </div>
          <div className="flex flex-wrap items-end gap-2">
            <Select label="For user" value={picked} onChange={(e) => setUser(e.target.value)}>
              {users.map((u) => (
                <option key={u} value={u}>
                  {u}
                </option>
              ))}
            </Select>
            <Button
              variant="outline"
              disabled={!names.length || install.pending}
              onClick={async () => {
                const res = await install.run({ workload: refOf(w), names, user: picked });
                if (res) {
                  setNames([]);
                  setAlready(res.already);
                }
              }}
            >
              Install
            </Button>
          </div>
        </>
      )}
      {already && already.length > 0 && (
        <Notice tone="info">
          {picked} already had {already.join(", ")}.
        </Notice>
      )}
      {install.error && <Notice tone="down">{install.error.rawMessage}</Notice>}
    </div>
  );
}
