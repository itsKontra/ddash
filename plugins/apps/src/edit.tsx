import { useAction } from "@ddash/plugin";
import { AppsService, Reach, type App } from "@ddash/proto/apps/apps_pb";
import { AppIconPicker, Button, Checkbox, Field, FormActions, Notice, Select, Sheet, TextInput } from "@ddash/ui";
import { Kind } from "@ddash/proto/ddash/workload/workload_pb";
import { useId, useState, type FormEvent } from "react";
import { workloadName } from "@ddash/display";
import { external, refKey, refOf, useWorkloads, WorkloadOptions } from "./workload";

function Text({
  label,
  hint,
  value,
  onChange,
  placeholder,
  list,
}: {
  label: string;
  hint?: string;
  value: string;
  onChange: (v: string) => void;
  placeholder?: string;
  list?: string;
}) {
  return (
    <Field label={label} hint={hint}>
      <TextInput list={list} value={value} placeholder={placeholder} onChange={(e) => onChange(e.target.value)} />
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

const iconHint = "A homarr-labs/dashboard-icons logo. Automatic matches one by the App's name, else uses the site's favicon.";

/** An App's icon, picked from dashboard-icons; name and url are what Automatic draws from. */
function IconField({ value, onChange, name, url }: { value: string; onChange: (v: string) => void; name: string; url?: string }) {
  return (
    <Field label="Icon" hint={iconHint}>
      <AppIconPicker label="Icon" value={value} onChange={onChange} name={name} url={url} />
    </Field>
  );
}

function Failed({ error }: { error?: { rawMessage: string } }) {
  return error ? <Notice tone="down">{error.rawMessage}</Notice> : null;
}

/** Picks a discovered App's Workload override, or leaves it to the match. */
function WorkloadField({ app, value, onChange, host, onHost }: { app: App; value: string; onChange: (v: string) => void; host: string; onHost: (v: string) => void }) {
  const { workloads, error } = useWorkloads();
  const w = app.workload;
  const matchLabel = w?.override ? "Match from the upstream" : `Matched: ${w ? workloadName(w) : "none"}`;
  const hint = error
    ? `Workloads can't be listed now (${error.rawMessage}); the match and the current override stay on offer.`
    : "The match is the container publishing the upstream's port on a Docker host, else the Proxmox guest at its address.";
  return (
    <>
      <Field label="Workload" hint={hint}>
        <Select value={value} onChange={(e) => onChange(e.target.value)}>
          <WorkloadOptions matchLabel={matchLabel} current={app.settings?.workload} listed={workloads ?? []} />
        </Select>
      </Field>
      {value === external && <Text label="External host" value={host} onChange={onHost} placeholder="198.18.1.50" />}
    </>
  );
}

/** Edits a discovered App's settings in its sheet: display name, icon, group, hidden, Workload. */
function SettingsSheet({ app, groups, onClose }: { app: App; groups: readonly string[]; onClose: () => void }) {
  const set = useAction(AppsService.method.setSettings);
  const reset = useAction(AppsService.method.setSettings);
  const o = app.settings;
  const [name, setName] = useState(o?.name ?? "");
  const [icon, setIcon] = useState(o?.icon ?? "");
  const [group, setGroup] = useState(o?.group ?? "");
  const [hidden, setHidden] = useState(o?.hidden ?? false);
  const [workload, setWorkload] = useState(o?.workload?.kind === Kind.EXTERNAL ? external : refKey(o?.workload));
  const [host, setHost] = useState(o?.workload?.kind === Kind.EXTERNAL ? o.workload.host : "");
  const listId = useId();
  const formId = useId();

  const save = async (e: FormEvent) => {
    e.preventDefault();
    const ref = workload === external ? { kind: Kind.EXTERNAL, host, id: "" } : refOf(workload);
    if (await set.run({ id: app.id, settings: { name, icon, group, hidden, workload: ref } })) onClose();
  };
  // Not disruptive, so unconfirmed: the settings are easily set again.
  const drop = async () => {
    if (await reset.run({ id: app.id, settings: {} })) onClose();
  };

  const footer = (
    <FormActions
      status={
        app.touched && (
          <Button variant="outline" disabled={reset.pending} onClick={() => void drop()}>
            Reset
          </Button>
        )
      }
    >
      <Button variant="outline" onClick={onClose}>
        Cancel
      </Button>
      <Button type="submit" form={formId} disabled={set.pending}>
        Save
      </Button>
    </FormActions>
  );

  return (
    <Sheet
      title={app.name}
      kicker="Edit App"
      subtitle={`From the Proxy Host ${hostname(app)}. These settings stay with the App, even while its Proxy Host is gone.`}
      footer={footer}
      onClose={onClose}
    >
      <form id={formId} onSubmit={(e) => void save(e)} className="flex flex-col gap-4">
        <Text label="Display name" value={name} onChange={setName} placeholder={o?.name ? undefined : app.name} />
        <IconField value={icon} onChange={setIcon} name={name || app.name} url={app.url} />
        <Text label="Group" value={group} onChange={setGroup} list={listId} placeholder="None" />
        <Groups id={listId} groups={groups} />
        <Checkbox label="Hidden" checked={hidden} onChange={(e) => setHidden(e.target.checked)} />
        <WorkloadField app={app} value={workload} onChange={setWorkload} host={host} onHost={setHost} />
        <Failed error={set.error ?? reset.error} />
      </form>
    </Sheet>
  );
}

/** Adds a Link-only App in its sheet, or edits or deletes one; its URL is fixed once added. */
function LinkOnlySheet({ app, groups, onClose }: { app?: App; groups: readonly string[]; onClose: () => void }) {
  const create = useAction(AppsService.method.createApp);
  const update = useAction(AppsService.method.setSettings);
  const remove = useAction(AppsService.method.deleteApp);
  const [name, setName] = useState(app?.name ?? "");
  const [url, setUrl] = useState(app?.url ?? "");
  const [icon, setIcon] = useState(app?.icon ?? "");
  const [group, setGroup] = useState(app?.group ?? "");
  const listId = useId();
  const formId = useId();

  const save = async (e: FormEvent) => {
    e.preventDefault();
    const done = app
      ? await update.run({ id: app.id, settings: { name, icon, group, hidden: app.settings?.hidden, workload: app.settings?.workload } })
      : await create.run({ app: { name, url, icon, group } });
    if (done) onClose();
  };
  const drop = async () => {
    if (app && (await remove.run({ id: app.id }, { confirm: { text: `Delete the App ${app.name}?`, action: "Delete" } }))) onClose();
  };

  const footer = (
    <FormActions
      status={
        app && (
          <Button variant="danger" disabled={remove.pending} onClick={() => void drop()}>
            Delete
          </Button>
        )
      }
    >
      <Button variant="outline" onClick={onClose}>
        Cancel
      </Button>
      <Button type="submit" form={formId} disabled={create.pending || update.pending}>
        {app ? "Save" : "Add"}
      </Button>
    </FormActions>
  );

  return (
    <Sheet title={app ? app.name : "Add App"} kicker={app ? "Edit App" : undefined} footer={footer} onClose={onClose}>
      <form id={formId} onSubmit={(e) => void save(e)} className="flex flex-col gap-4">
        <Text label="Name" value={name} onChange={setName} />
        {app ? (
          <Field label="URL" hint="Fixed once the App is added.">
            <TextInput value={url} readOnly />
          </Field>
        ) : (
          <Text label="URL" value={url} onChange={setUrl} placeholder="https://198.18.0.1" />
        )}
        <IconField value={icon} onChange={setIcon} name={name} url={url} />
        <Text label="Group" value={group} onChange={setGroup} list={listId} placeholder="None" />
        <Groups id={listId} groups={groups} />
        <Failed error={(app ? update.error : create.error) ?? remove.error} />
      </form>
    </Sheet>
  );
}

/** The hostname of an App's URL. */
const hostname = (app: App) => URL.parse(app.url)?.hostname ?? app.url;

/** The sheet editing an App, or adding a Link-only one when app is undefined. */
export function EditSheet({ app, groups, onClose }: { app?: App; groups: readonly string[]; onClose: () => void }) {
  return app && app.reach !== Reach.LINK_ONLY ? (
    <SettingsSheet app={app} groups={groups} onClose={onClose} />
  ) : (
    <LinkOnlySheet app={app} groups={groups} onClose={onClose} />
  );
}
