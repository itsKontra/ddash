import { useAction } from "@ddash/plugin";
import { AppsService, type App } from "@ddash/proto/apps/apps_pb";
import { Button, iconName, Sheet } from "@ddash/ui";
import { Kind } from "@ddash/proto/ddash/workload/workload_pb";
import { useId, useState, type FormEvent, type ReactNode } from "react";
import { workloadName } from "@ddash/display";
import { external, refKey, refOf, useWorkloads, WorkloadOptions } from "./workload";

const inputClass = "h-9 w-full rounded-ui border border-border bg-card px-3 text-sm placeholder:text-muted-foreground";

function Field({ label, help, children }: { label: string; help?: string; children: (id: string) => ReactNode }) {
  const id = useId();
  return (
    <div className="flex flex-col gap-1">
      <label htmlFor={id} className="text-sm font-medium">
        {label}
      </label>
      {children(id)}
      {help && <p className="text-xs text-muted-foreground">{help}</p>}
    </div>
  );
}

function Text({
  label,
  help,
  value,
  onChange,
  placeholder,
  list,
}: {
  label: string;
  help?: string;
  value: string;
  onChange: (v: string) => void;
  placeholder?: string;
  list?: string;
}) {
  return (
    <Field label={label} help={help}>
      {(id) => (
        <input id={id} type="text" list={list} className={inputClass} value={value} placeholder={placeholder} onChange={(e) => onChange(e.target.value)} />
      )}
    </Field>
  );
}

/** The groups already in use, offered while typing one. */
function Groups({ id, groups }: { id: string; groups: readonly string[] }) {
  return (
    <datalist id={id}>
      {groups.map((g) => (
        <option key={g} value={g} />
      ))}
    </datalist>
  );
}

const iconHelp = "A homarr-labs/dashboard-icons name, e.g. jellyfin. Empty matches one by the App's name, else the site's favicon is used.";

function Failed({ error }: { error?: { rawMessage: string } }) {
  if (!error) return null;
  return (
    <p role="alert" className="text-sm text-down-text">
      {error.rawMessage}
    </p>
  );
}

/** Picks a discovered App's Workload override, or leaves it to the match. */
function WorkloadField({ app, value, onChange, host, onHost }: { app: App; value: string; onChange: (v: string) => void; host: string; onHost: (v: string) => void }) {
  const { workloads, error } = useWorkloads();
  const w = app.workload;
  const matchLabel = w?.override ? "Match from the upstream" : `Matched: ${w ? workloadName(w) : "none"}`;
  const help = error
    ? `Workloads can't be listed now (${error.rawMessage}); the match and the current override stay on offer.`
    : "The match is the container publishing the upstream's port on a Docker host, else the Proxmox guest at its address.";
  return (
    <>
      <Field label="Workload" help={help}>
        {(id) => (
          <select id={id} className={inputClass} value={value} onChange={(e) => onChange(e.target.value)}>
            <WorkloadOptions matchLabel={matchLabel} current={app.overlay?.workload} listed={workloads ?? []} />
          </select>
        )}
      </Field>
      {value === external && <Text label="External host" value={host} onChange={onHost} placeholder="198.18.1.50" />}
    </>
  );
}

/** Edits a discovered App's overlay: display name, icon, group, hidden, Workload. */
function OverlayForm({ app, groups, onDone }: { app: App; groups: readonly string[]; onDone: () => void }) {
  const set = useAction(AppsService.method.setOverlay);
  const reset = useAction(AppsService.method.deleteOverlay);
  const o = app.overlay;
  const [name, setName] = useState(o?.name ?? "");
  const [icon, setIcon] = useState(o?.icon ?? "");
  const [group, setGroup] = useState(o?.group ?? "");
  const [hidden, setHidden] = useState(o?.hidden ?? false);
  const [workload, setWorkload] = useState(o?.workload?.kind === Kind.EXTERNAL ? external : refKey(o?.workload));
  const [host, setHost] = useState(o?.workload?.kind === Kind.EXTERNAL ? o.workload.host : "");
  const listId = useId();

  const save = async (e: FormEvent) => {
    e.preventDefault();
    const ref = workload === external ? { kind: Kind.EXTERNAL, host, id: "" } : refOf(workload);
    if (await set.run({ overlay: { serverName: app.serverName, name, icon, group, hidden, workload: ref } })) onDone();
  };
  const drop = async () => {
    const confirm = { text: `Reset ${app.name} to its defaults? Its display name, icon, group, hidden setting and Workload are dropped.`, action: "Reset" };
    if (await reset.run({ serverName: app.serverName }, { confirm })) onDone();
  };

  return (
    <form onSubmit={(e) => void save(e)} className="flex flex-col gap-4">
      <p className="text-sm text-muted-foreground">
        From the Proxy Host <span className="font-medium text-foreground">{app.serverName}</span>. These settings stay with that name, even while
        the Proxy Host is gone.
      </p>
      <Text label="Display name" value={name} onChange={setName} placeholder={o?.name ? undefined : app.name} />
      <Text label="Icon" help={iconHelp} value={icon} onChange={setIcon} placeholder={iconName(name || app.name)} />
      <Text label="Group" value={group} onChange={setGroup} list={listId} placeholder="None" />
      <Groups id={listId} groups={groups} />
      <label className="flex items-center gap-2 text-sm">
        <input type="checkbox" checked={hidden} onChange={(e) => setHidden(e.target.checked)} />
        Hidden
      </label>
      <WorkloadField app={app} value={workload} onChange={setWorkload} host={host} onHost={setHost} />
      <Failed error={set.error ?? reset.error} />
      <div className="flex gap-2">
        <Button type="submit" disabled={set.pending}>
          Save
        </Button>
        {o && (
          <Button variant="outline" className="ml-auto" disabled={reset.pending} onClick={() => void drop()}>
            Reset
          </Button>
        )}
      </div>
    </form>
  );
}

/** Adds a manual App, or edits or deletes one. */
function ManualForm({ app, groups, onDone }: { app?: App; groups: readonly string[]; onDone: () => void }) {
  const create = useAction(AppsService.method.createApp);
  const update = useAction(AppsService.method.updateApp);
  const remove = useAction(AppsService.method.deleteApp);
  const [name, setName] = useState(app?.name ?? "");
  const [url, setUrl] = useState(app?.url ?? "");
  const [icon, setIcon] = useState(app?.icon ?? "");
  const [group, setGroup] = useState(app?.group ?? "");
  const listId = useId();

  const save = async (e: FormEvent) => {
    e.preventDefault();
    const spec = { name, url, icon, group };
    const done = app ? await update.run({ id: app.id, app: spec }) : await create.run({ app: spec });
    if (done) onDone();
  };
  const drop = async () => {
    if (app && (await remove.run({ id: app.id }, { confirm: { text: `Delete the App ${app.name}?`, action: "Delete" } }))) onDone();
  };

  return (
    <form onSubmit={(e) => void save(e)} className="flex flex-col gap-4">
      <Text label="Name" value={name} onChange={setName} />
      <Text label="URL" value={url} onChange={setUrl} placeholder="https://198.18.0.1" />
      <Text label="Icon" help={iconHelp} value={icon} onChange={setIcon} placeholder={iconName(name)} />
      <Text label="Group" value={group} onChange={setGroup} list={listId} placeholder="None" />
      <Groups id={listId} groups={groups} />
      <Failed error={(app ? update.error : create.error) ?? remove.error} />
      <div className="flex gap-2">
        <Button type="submit" disabled={create.pending || update.pending}>
          {app ? "Save" : "Add"}
        </Button>
        {app && (
          <Button variant="danger" className="ml-auto" disabled={remove.pending} onClick={() => void drop()}>
            Delete
          </Button>
        )}
      </div>
    </form>
  );
}

/** The sheet editing an App, or adding a manual one when app is undefined. */
export function EditSheet({ app, groups, onClose }: { app?: App; groups: readonly string[]; onClose: () => void }) {
  return (
    <Sheet title={app ? app.name : "Add App"} onClose={onClose}>
      {app && !app.manual ? (
        <OverlayForm app={app} groups={groups} onDone={onClose} />
      ) : (
        <ManualForm app={app} groups={groups} onDone={onClose} />
      )}
    </Sheet>
  );
}
