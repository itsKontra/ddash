import { usePluginLink, useQuery } from "@ddash/plugin";
import { AppsService, type ComposeService } from "@ddash/proto/apps/apps_pb";
import { Field, Notice, Select, Skeleton, TextArea, TextInput } from "@ddash/ui";
import { useEffect } from "react";
import { Link } from "react-router";

/** A new Compose Project as the Operator types it: what Add App sends as its compose source. */
export interface ComposeDraft {
  host: string;
  name: string;
  directory: string;
  content: string;
  /** "" for the first Compose service publishing a port. */
  service: string;
  /** "" for the service's first published port. */
  port: string;
}

export const newCompose = (): ComposeDraft => ({ host: "", name: "", directory: "", content: "", service: "", port: "" });

/** The draft as AddApp's compose field. */
export const composeSource = (c: ComposeDraft) => ({
  host: c.host,
  name: c.name.trim(),
  directory: c.directory.trim(),
  content: c.content,
  service: c.service,
  port: Number(c.port) || 0,
});

/**
 * The New Compose Project source's fields: the Docker host (a paired
 * Docker Agent's, from ListDockerHosts), the Compose Project's name and
 * directory, its Compose file, and the Compose service and published
 * port the App opens, picked from what the Plan read in the file.
 */
export function ComposeFields({ value: c, onChange, services }: { value: ComposeDraft; onChange: (c: ComposeDraft) => void; services: readonly ComposeService[] }) {
  const { data, error } = useQuery(AppsService.method.listDockerHosts, {});
  const docker = usePluginLink("docker");
  const set = (patch: Partial<ComposeDraft>) => onChange({ ...c, ...patch });
  const hosts = data?.hosts ?? [];
  const host = hosts.find((h) => h.name === c.host);

  // The first host, until the Operator picks one.
  const first = data?.hosts[0]?.name;
  useEffect(() => {
    if (!c.host && first) onChange({ ...c, host: first });
  }, [c, first, onChange]);

  const picked = services.find((s) => s.name === c.service);
  const auto = services.find((s) => s.ports.length > 0) ?? services[0];
  const ports = (picked ?? auto)?.ports ?? [];
  const root = host?.roots[0];

  if (error) return <Notice tone="down">The Docker hosts can&apos;t be listed: {error.rawMessage}</Notice>;
  if (!data) return <Skeleton height="lg" />;
  if (hosts.length === 0) {
    return (
      <Notice tone="warn">
        No Docker host has a paired Docker Agent yet.{" "}
        {docker ? <Link to={docker}>Add one on the Docker Page</Link> : "Add one with the Docker Plugin"} to start Compose Projects from ddash.
      </Notice>
    );
  }

  return (
    <div className="flex flex-col gap-4">
      <div className="grid grid-cols-1 gap-4 sm:grid-cols-3">
        <Field label="Docker host" hint={host ? `Published ports answer at ${host.address}.` : undefined}>
          <Select value={c.host} onChange={(e) => set({ host: e.target.value })}>
            {hosts.map((h) => (
              <option key={h.name} value={h.name}>
                {h.name}
                {h.error ? " (unreachable)" : ""}
              </option>
            ))}
          </Select>
        </Field>
        <Field label="Project name">
          <TextInput value={c.name} placeholder="paperless" onChange={(e) => set({ name: e.target.value })} />
        </Field>
        <Field label="Directory">
          <TextInput value={c.directory} placeholder={root ? `${root}/${c.name.trim() || "<name>"}` : ""} onChange={(e) => set({ directory: e.target.value })} />
        </Field>
      </div>
      {host?.error && <Notice tone="warn">{host.name}&apos;s Docker Agent can&apos;t be reached: {host.error}</Notice>}
      <Field label="Compose file" hint="Written as compose.yaml in the directory, then started with docker compose up -d. Never an .env: add one on the host.">
        <div className="font-mono text-xs">
          <TextArea value={c.content} rows={14} spellCheck={false} placeholder={"services:\n  app:\n    image: …"} onChange={(e) => set({ content: e.target.value })} />
        </div>
      </Field>
      <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
        <Field label="Compose service" hint="The service the App opens.">
          <Select value={c.service} onChange={(e) => set({ service: e.target.value, port: "" })}>
            <option value="">{auto ? `Automatic: ${auto.name}` : "Automatic"}</option>
            {services.map((s) => (
              <option key={s.name} value={s.name}>
                {s.name}
              </option>
            ))}
          </Select>
        </Field>
        <Field label="Published port" hint={host?.usedPorts.length ? `In use on ${host.name}: ${host.usedPorts.join(", ")}.` : undefined}>
          <Select value={c.port} onChange={(e) => set({ port: e.target.value })}>
            <option value="">{ports.length > 0 ? `Automatic: ${ports[0]}` : "None published"}</option>
            {ports.map((p) => (
              <option key={p} value={String(p)}>
                {p}
              </option>
            ))}
          </Select>
        </Field>
      </div>
    </div>
  );
}
