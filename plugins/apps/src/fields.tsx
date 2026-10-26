import { workloadName } from "@ddash/display";
import type { App } from "@ddash/proto/apps/apps_pb";
import { AppIconPicker, Field, Notice, Select, TextInput } from "@ddash/ui";
import { useId } from "react";
import { external, useWorkloads, WorkloadOptions } from "./workload";

/** A labelled text input. */
export function Text({ label, hint, value, onChange, placeholder }: { label: string; hint?: string; value: string; onChange: (v: string) => void; placeholder?: string }) {
  return (
    <Field label={label} hint={hint}>
      <TextInput value={value} placeholder={placeholder} onChange={(e) => onChange(e.target.value)} />
    </Field>
  );
}

/** The Group field, offering the groups already in use. */
export function GroupField({ value, onChange, groups }: { value: string; onChange: (v: string) => void; groups: readonly string[] }) {
  const id = useId();
  return (
    <>
      <Field label="Group">
        <TextInput list={id} value={value} placeholder="None" onChange={(e) => onChange(e.target.value)} />
      </Field>
      <datalist id={id}>
        {groups.map((g) => (
          <option key={g} value={g} />
        ))}
      </datalist>
    </>
  );
}

const iconHint = "A homarr-labs/dashboard-icons logo. Automatic matches one by the App's name, else uses the site's favicon.";

/** An App's icon, picked from dashboard-icons; name and url are what Automatic draws from. */
export function IconField({ value, onChange, name, url }: { value: string; onChange: (v: string) => void; name: string; url?: string }) {
  return (
    <Field label="Icon" hint={iconHint}>
      <AppIconPicker label="Icon" value={value} onChange={onChange} name={name} url={url} />
    </Field>
  );
}

/** Why an Action failed, as an alert. */
export function Failed({ error }: { error?: { rawMessage: string } }) {
  return error ? <Notice tone="down">{error.rawMessage}</Notice> : null;
}

/** Picks an App's Workload override, or leaves it to the match. */
export function WorkloadField({ app, value, onChange, host, onHost }: { app: App; value: string; onChange: (v: string) => void; host: string; onHost: (v: string) => void }) {
  const { workloads, error } = useWorkloads();
  const w = app.workload;
  const matchLabel = w?.override ? "Match" : `Matched: ${w ? workloadName(w) : "none"}`;
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
