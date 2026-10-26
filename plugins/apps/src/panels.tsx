import type { MessageInitShape } from "@bufbuild/protobuf";
import { certificateState, checkStatus, historyBars } from "@ddash/display";
import { useAction, usePluginLink, useQuery } from "@ddash/plugin";
import { AppsService, InternetPath, Reach, type App, type ListAppsResponse, type ProxyHost } from "@ddash/proto/apps/apps_pb";
import { ChecksService, Window, type Check } from "@ddash/proto/checks/checks_pb";
import { Kind, type WorkloadRefSchema } from "@ddash/proto/ddash/workload/workload_pb";
import { NginxProxyManagerApiService } from "@ddash/proto/nginx-proxy-manager-api/nginx_proxy_manager_api_pb";
import { Button, Checkbox, Facts, FormActions, Panel, Pill, UptimeBars, type Fact, type Tone } from "@ddash/ui";
import { useState, type FormEvent, type ReactNode } from "react";
import { Link } from "react-router";
import { hostOf } from "./apps";
import { Failed, GroupField, IconField, Text, WorkloadField } from "./fields";
import { external, refKey, refOf, WorkloadLine } from "./workload";

/** The Panels with an Edit; one edits at a time. */
export type PanelName = "display" | "address" | "workload";

/** Which Panel edits now, and how to start and end it. */
export interface Editing {
  now?: PanelName;
  start: (p: PanelName) => void;
  done: () => void;
}

/** A Panel's Edit button; disabled while another Panel edits, gone while its own does. */
function EditButton({ panel, title, editing }: { panel: PanelName; title: string; editing: Editing }) {
  if (editing.now === panel) return null;
  return (
    <Button size="sm" variant="ghost" icon="pencil" aria-label={`Edit ${title}`} disabled={editing.now !== undefined} onClick={() => editing.start(panel)}>
      Edit
    </Button>
  );
}

/** Saves an App's settings at once: the settings it has, with patch over them. */
function useSaveSettings(app: App) {
  const set = useAction(AppsService.method.setSettings);
  const s = app.settings;
  const save = (patch: { name?: string; icon?: string; group?: string; hidden?: boolean; workload?: MessageInitShape<typeof WorkloadRefSchema> }) =>
    set.run({ id: app.id, settings: { name: s?.name, icon: s?.icon, group: s?.group, hidden: s?.hidden, workload: s?.workload, ...patch } });
  return { ...set, save };
}

/** Display: the name, icon, group and Hidden, ddash's own and saved at once. */
export function DisplayPanel({ app, groups, editing }: { app: App; groups: readonly string[]; editing: Editing }) {
  const s = app.settings;
  return (
    <Panel title="Display" actions={<EditButton panel="display" title="Display" editing={editing} />}>
      {editing.now === "display" ? (
        <DisplayForm app={app} groups={groups} onDone={editing.done} />
      ) : (
        <Facts
          items={[
            { label: "Name", value: s?.name ? app.name : `${app.name} (from the URL)` },
            { label: "Icon", value: app.icon || "Automatic" },
            { label: "Group", value: app.group || "None" },
            { label: "Hidden", value: app.hidden ? "Yes: no Check, and its Warnings don't count" : "No" },
          ]}
        />
      )}
    </Panel>
  );
}

function DisplayForm({ app, groups, onDone }: { app: App; groups: readonly string[]; onDone: () => void }) {
  const set = useSaveSettings(app);
  const reset = useAction(AppsService.method.setSettings);
  const s = app.settings;
  const [name, setName] = useState(s?.name ?? "");
  const [icon, setIcon] = useState(s?.icon ?? "");
  const [group, setGroup] = useState(s?.group ?? "");
  const [hidden, setHidden] = useState(s?.hidden ?? false);
  const save = async (e: FormEvent) => {
    e.preventDefault();
    if (await set.save({ name, icon, group, hidden })) onDone();
  };
  // Not disruptive, so unconfirmed: the settings are easily set again.
  const drop = async () => {
    if (await reset.run({ id: app.id, settings: {} })) onDone();
  };
  return (
    <form onSubmit={(e) => void save(e)} className="flex flex-col gap-4">
      <Text label="Name" value={name} onChange={setName} placeholder={s?.name ? undefined : app.name} />
      <IconField value={icon} onChange={setIcon} name={name || app.name} url={app.url} />
      <GroupField value={group} onChange={setGroup} groups={groups} />
      <Checkbox label="Hidden" checked={hidden} onChange={(e) => setHidden(e.target.checked)} />
      <Failed error={set.error ?? reset.error} />
      <FormActions
        status={
          app.touched &&
          app.reach !== Reach.LINK_ONLY && (
            <Button variant="outline" disabled={reset.pending} onClick={() => void drop()}>
              Reset all settings
            </Button>
          )
        }
      >
        <Button variant="ghost" onClick={onDone}>
          Cancel
        </Button>
        <Button type="submit" disabled={set.pending}>
          Save
        </Button>
      </FormActions>
    </form>
  );
}

/** How one DNS entry registers a hostname. */
function dnsLine(app: App, list: ListAppsResponse, name: string): ReactNode {
  const entries = app.dns.filter((e) => `${e.host ? `${e.host}.` : ""}${e.domain}`.toLowerCase() === name.toLowerCase());
  if (entries.length === 0) return list.hostOverrides?.readAt ? <span className="text-warn-text">not registered</span> : "not read yet";
  return entries
    .map((e) => {
      if (!e.parent) return `Host Override → ${e.ips.join(", ")}`;
      const parent = `${e.parent.host ? `${e.parent.host}.` : ""}${e.parent.domain}`;
      const rp = list.reverseProxy?.hostOverrides.includes(parent);
      return `alias of ${parent}${rp ? " (the Reverse Proxy)" : ` → ${e.ips.join(", ")}`}`;
    })
    .join("; ");
}

/**
 * Address and DNS: the URL, the aliases, and how pfSense registers each
 * hostname. Its Edit promotes an alias to the URL, a ddash-only change;
 * changing the URL or the aliases writes to pfSense and Nginx Proxy
 * Manager through a Plan, which the write tickets add here.
 */
export function AddressPanel({ app, list, editing }: { app: App; list: ListAppsResponse; editing: Editing }) {
  const names = [hostOf(app), ...app.aliases];
  return (
    <Panel title="Address and DNS" actions={app.aliases.length > 0 && <EditButton panel="address" title="Address and DNS" editing={editing} />}>
      {editing.now === "address" ? (
        <PromoteForm app={app} onDone={editing.done} />
      ) : (
        <div className="flex flex-col gap-3">
          <Facts
            items={[
              {
                label: "URL",
                value: (
                  <a href={app.url} target="_blank" rel="noreferrer" className="hover:underline">
                    {app.url}
                  </a>
                ),
                mono: true,
              },
              { label: "Aliases", value: app.aliases.length ? app.aliases.join(", ") : "None", mono: app.aliases.length > 0 },
            ]}
          />
          {app.reach === Reach.LINK_ONLY ? (
            <p className="text-sm text-muted-foreground">A link only: nothing in pfSense.</p>
          ) : (
            <Facts items={names.map((n): Fact => ({ label: n, value: dnsLine(app, list, n) }))} />
          )}
        </div>
      )}
    </Panel>
  );
}

function PromoteForm({ app, onDone }: { app: App; onDone: () => void }) {
  const promote = useAction(AppsService.method.promoteAlias);
  return (
    <div className="flex flex-col gap-3">
      <p className="text-sm text-muted-foreground">Make an alias the URL&apos;s hostname. Only ddash changes: pfSense and Nginx Proxy Manager keep every name.</p>
      <ul className="flex flex-col gap-2">
        {app.aliases.map((a) => (
          <li key={a} className="flex items-center gap-2">
            <span className="flex-1 font-mono text-sm break-all">{a}</span>
            <Button
              size="sm"
              variant="outline"
              aria-label={`Make ${a} the URL`}
              disabled={promote.pending}
              onClick={async () => {
                if (await promote.run({ id: app.id, alias: a })) onDone();
              }}
            >
              Make URL
            </Button>
          </li>
        ))}
      </ul>
      <Failed error={promote.error} />
      <FormActions>
        <Button variant="ghost" onClick={onDone}>
          Done
        </Button>
      </FormActions>
    </div>
  );
}

const flags = (p: ProxyHost) =>
  [p.blockExploits && "Block Common Exploits", p.websockets && "Websockets", p.forceSsl && "Force SSL", p.http2 && "HTTP/2", p.hsts && "HSTS", p.hstsSubdomains && "HSTS subdomains"].filter(Boolean).join(" · ");

/** The certificate a Proxy Host uses, with its state linking to the Reverse Proxy page. */
function CertificateLine({ id }: { id: bigint }) {
  const { data, error } = useQuery(NginxProxyManagerApiService.method.listCertificates, {});
  const page = usePluginLink("reverse-proxy");
  if (id === 0n) return <>None: plain HTTP</>;
  const cert = data?.certificates.find((c) => c.id === id);
  if (!cert) return <>{data || error ? `#${id}, not listed now` : `#${id}`}</>;
  const s = certificateState(cert);
  const pill = <Pill tone={s.tone}>{s.label}</Pill>;
  return (
    <span className="flex flex-wrap items-center gap-2">
      <span>{cert.niceName}</span>
      {page ? (
        <Link to={page} aria-label={`${s.label}: see the Reverse Proxy`}>
          {pill}
        </Link>
      ) : (
        pill
      )}
      {s.message && <span className="text-xs text-muted-foreground">{s.message}</span>}
    </span>
  );
}

/**
 * Reverse proxy: the Proxy Host serving the App, its certificate's state
 * and whether it is disabled or Offline; Add reverse proxy… on a
 * Not-proxied App. Its Edit and Add reverse proxy… write to Nginx Proxy
 * Manager through a Plan, which the write tickets add here.
 */
export function ProxyPanel({ app, list }: { app: App; list: ListAppsResponse }) {
  const p = app.proxyHost;
  const ip = app.dns.find((e) => !e.parent)?.ips[0] ?? app.dns[0]?.ips[0];
  const add = app.reach === Reach.NOT_PROXIED && !p && (
    <Button size="sm" variant="outline" disabled title="Writes to pfSense or Nginx Proxy Manager aren't available yet">
      Add reverse proxy…
    </Button>
  );
  return (
    <Panel title="Reverse proxy" actions={add}>
      {p ? (
        <div className="flex flex-col gap-3">
          {(!p.enabled || p.offline) && (
            <div className="flex flex-wrap gap-2">
              {!p.enabled && <Pill>Proxy Host disabled</Pill>}
              {p.offline && <Pill tone="warn">Offline</Pill>}
            </div>
          )}
          <Facts
            items={[
              { label: "Upstream", value: `${p.forwardScheme}://${p.forwardHost}:${p.forwardPort}`, mono: true },
              { label: "Certificate", value: <CertificateLine id={p.certificateId} /> },
              { label: "Options", value: flags(p) || "None" },
              { label: "Proxy Host", value: `#${p.id} in Nginx Proxy Manager` },
            ]}
          />
        </div>
      ) : (
        <p className="text-sm text-muted-foreground">
          {app.reach === Reach.NOT_PROXIED
            ? `Not proxied: pfSense resolves ${hostOf(app)} to ${ip ?? "its own address"}.`
            : app.reach === Reach.LINK_ONLY
              ? "A link only: no Proxy Host."
              : list.proxyHosts?.readAt
                ? "No Proxy Host serves it."
                : "Nginx Proxy Manager not read yet."}
        </p>
      )}
    </Panel>
  );
}

/** The last 30 days of an App's Check, one bar a day. */
function Uptime({ check }: { check: Check }) {
  const { data } = useQuery(ChecksService.method.getHistory, { id: check.id, window: Window.WINDOW_30D });
  if (!data) return null;
  return <UptimeBars label="Uptime, last 30 days" bars={historyBars(data, 30 * 86_400_000)} />;
}

/** Workload and Check: the Workload behind the App (its Edit sets an override, at once), the Check's status and its last 30 days. */
export function WorkloadPanel({ app, check, editing }: { app: App; check?: Check; editing: Editing }) {
  const status = checkStatus(check);
  return (
    <Panel title="Workload and Check" actions={<EditButton panel="workload" title="Workload and Check" editing={editing} />}>
      {editing.now === "workload" ? (
        <WorkloadForm app={app} onDone={editing.done} />
      ) : (
        <div className="flex flex-col gap-3">
          {app.workload ? (
            <div className="flex items-center gap-2">
              <WorkloadLine workload={app.workload} />
              {app.workload.override && <Pill>override</Pill>}
            </div>
          ) : (
            <p className="text-sm text-muted-foreground">No Workload matched.</p>
          )}
          <div role="status" aria-label="Check" className="flex">
            <Pill tone={status.tone} dot>
              {status.label}
            </Pill>
          </div>
          {check && <Uptime check={check} />}
        </div>
      )}
    </Panel>
  );
}

function WorkloadForm({ app, onDone }: { app: App; onDone: () => void }) {
  const set = useSaveSettings(app);
  const o = app.settings?.workload;
  const [workload, setWorkload] = useState(o?.kind === Kind.EXTERNAL ? external : refKey(o));
  const [host, setHost] = useState(o?.kind === Kind.EXTERNAL ? o.host : "");
  const save = async (e: FormEvent) => {
    e.preventDefault();
    const ref = workload === external ? { kind: Kind.EXTERNAL, host, id: "" } : refOf(workload);
    if (await set.save({ workload: ref })) onDone();
  };
  return (
    <form onSubmit={(e) => void save(e)} className="flex flex-col gap-4">
      <WorkloadField app={app} value={workload} onChange={setWorkload} host={host} onHost={setHost} />
      <Failed error={set.error} />
      <FormActions>
        <Button variant="ghost" onClick={onDone}>
          Cancel
        </Button>
        <Button type="submit" disabled={set.pending}>
          Save
        </Button>
      </FormActions>
    </form>
  );
}

const paths: Record<InternetPath, { tone: Tone; label: string }> = {
  [InternetPath.UNSPECIFIED]: { tone: "idle", label: "Unknown" },
  [InternetPath.TUNNELLED]: { tone: "up", label: "Tunnelled" },
  [InternetPath.DIRECT]: { tone: "info", label: "Direct" },
  [InternetPath.NO_INTERNET]: { tone: "maint", label: "No Internet" },
};

/** The Device holding the address, linked to its detail on the Network Page when that Plugin is installed. */
function DeviceLink({ mac, name }: { mac: string; name: string }) {
  const href = usePluginLink("network", `devices/${mac}`);
  const text = name || mac;
  return href ? <Link to={href}>{text}</Link> : <>{text}</>;
}

/** Network: how the App's Workload reaches the internet, read only, with the Device holding its address. */
export function NetworkPanel({ app, list }: { app: App; list: ListAppsResponse }) {
  const n = app.network;
  const docker = app.workload?.ref?.kind === Kind.DOCKER;
  let body: ReactNode;
  if (n) {
    const path = paths[n.internetPath];
    body = (
      <div className="flex flex-col gap-3">
        <div className="flex flex-wrap gap-2">
          <Pill tone={path.tone}>{path.label}</Pill>
          {!list.adguardError && <Pill tone={n.adsAllowed ? "warn" : "idle"}>{n.adsAllowed ? "Ads Allowed" : "Ads blocked"}</Pill>}
        </div>
        <Facts
          items={[
            { label: "Address", value: docker ? `${n.address} (the Docker host ${app.workload?.ref?.host})` : n.address, mono: !docker },
            { label: "Device", value: n.device ? <DeviceLink mac={n.device.mac} name={n.device.name} /> : "None: a fixed address" },
          ]}
        />
        {list.adguardError && <p className="text-xs text-muted-foreground">Ads Allowed unknown: {list.adguardError}</p>}
      </div>
    );
  } else if (list.networkError && app.workload) {
    body = <p className="text-sm text-muted-foreground">Network info unavailable: {list.networkError}</p>;
  } else {
    body = <p className="text-sm text-muted-foreground">{app.workload ? "Its Workload has no IP address ddash knows." : "No Workload: no address to read."}</p>;
  }
  return (
    <Panel title="Network" description="Changed on the Network Page.">
      {body}
    </Panel>
  );
}
