import { create, type MessageInitShape } from "@bufbuild/protobuf";
import { checkStatus, historyBars } from "@ddash/display";
import { useAction, usePluginLink, useQuery } from "@ddash/plugin";
import { AppsService, CertificateState, InternetPath, Reach, type App, type ListAppsResponse, type ProxyHost } from "@ddash/proto/apps/apps_pb";
import { ChecksService, Window, type Check } from "@ddash/proto/checks/checks_pb";
import { Kind, type WorkloadRefSchema } from "@ddash/proto/ddash/workload/workload_pb";
import { ProxyHostFieldsSchema, type ProxyHostFields } from "@ddash/proto/ddash/proxyhost/proxy_host_pb";
import { Button, Checkbox, Facts, FormActions, Panel, Pill, UptimeBars, type Fact, type Tone } from "@ddash/ui";
import { useState, type FormEvent, type ReactNode } from "react";
import { Link } from "react-router";
import { fqdn, hostOf } from "./apps";
import { Failed, GroupField, IconField, Text, WorkloadField } from "./fields";
import { PlanDialog, type DialogSlot } from "./plan";
import { newProxy, ProxyFields, resolve, useCertificates, type ProxyDraft } from "./proxy-fields";
import { external, refKey, refOf, WorkloadLine } from "./workload";

/** The Panels with an Edit; one edits at a time. */
export type PanelName = "display" | "address" | "proxy" | "workload";

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

/** How the Host Overrides and aliases registering a hostname do. */
function dnsLine(app: App, list: ListAppsResponse, name: string): ReactNode {
  const registering = app.dns.filter((e) => fqdn(e).toLowerCase() === name.toLowerCase());
  if (registering.length === 0) return list.hostOverrides?.readAt ? <span className="text-warn-text">not registered</span> : "not read yet";
  return registering
    .map((e) => {
      if (!e.parent) return `Host Override → ${e.ips.join(", ")}`;
      const parent = fqdn(e.parent);
      const rp = list.reverseProxy?.hostOverrides.includes(parent);
      return `alias of ${parent}${rp ? " (the Reverse Proxy)" : ` → ${e.ips.join(", ")}`}`;
    })
    .join("; ");
}

/**
 * Address and DNS: the URL, the aliases, and how pfSense registers each
 * hostname. Its Edit changes the URL and the aliases through a Plan, which
 * checks each new name is free, and promotes an alias to the URL at once,
 * a ddash-only change.
 */
export function AddressPanel({ app, list, editing, slot }: { app: App; list: ListAppsResponse; editing: Editing; slot: DialogSlot }) {
  const names = [hostOf(app), ...app.aliases];
  return (
    <Panel title="Address and DNS" actions={<EditButton panel="address" title="Address and DNS" editing={editing} />}>
      {editing.now === "address" ? (
        <AddressForm app={app} slot={slot} onDone={editing.done} />
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

/** Hostnames typed with commas or spaces between them. */
const splitNames = (s: string) => s.split(/[\s,]+/).filter(Boolean);

/** What saving a new address writes, by Reach. */
const addressHint: Record<Reach, string> = {
  [Reach.UNSPECIFIED]: "",
  [Reach.PROXIED]: "Saving shows the Plan first: the Proxy Host's names, and the aliases of the Reverse Proxy's Host Override.",
  [Reach.NOT_PROXIED]: "Saving shows the Plan first: the App's own Host Override and its aliases in pfSense.",
  [Reach.LINK_ONLY]: "Only ddash changes: a Link-only App has nothing in pfSense.",
};

function AddressForm({ app, slot, onDone }: { app: App; slot: DialogSlot; onDone: () => void }) {
  const promote = useAction(AppsService.method.promoteAlias);
  const [url, setUrl] = useState(app.url);
  const [aliases, setAliases] = useState(app.aliases.join(", "));
  const changed = url.trim() !== app.url || splitNames(aliases).join(" ") !== app.aliases.join(" ");
  const save = (e: FormEvent) => {
    e.preventDefault();
    if (!changed) return;
    slot.open(
      <PlanDialog
        title={`Change ${app.name}'s address`}
        action={{ action: { case: "changeAddress", value: { id: app.id, url: url.trim(), aliases: splitNames(aliases) } } }}
        confirm="Save"
        onClose={slot.close}
        onDone={() => {
          slot.close();
          onDone();
        }}
      />,
    );
  };
  return (
    <form onSubmit={save} className="flex flex-col gap-4">
      <Text label="URL" value={url} onChange={setUrl} />
      <Text label="Aliases" hint="Its further hostnames, with commas or spaces between them." value={aliases} onChange={setAliases} />
      <p className="text-sm text-muted-foreground">{addressHint[app.reach]}</p>
      {app.aliases.length > 0 && (
        <div className="flex flex-col gap-2">
          <p className="text-sm text-muted-foreground">Or make an alias the URL&apos;s hostname at once. Only ddash changes: pfSense and Nginx Proxy Manager keep every name.</p>
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
        </div>
      )}
      <Failed error={promote.error} />
      <FormActions>
        <Button variant="ghost" onClick={onDone}>
          Cancel
        </Button>
        <Button type="submit" disabled={!changed}>
          Save
        </Button>
      </FormActions>
    </form>
  );
}

const flags = (p: ProxyHostFields) =>
  [p.blockExploits && "Block Common Exploits", p.websockets && "Websockets", p.forceSsl && "Force SSL", p.http2 && "HTTP/2", p.hsts && "HSTS", p.hstsSubdomains && "HSTS subdomains"].filter(Boolean).join(" · ");

/** Each certificate state's tone and label. */
const certificateLook: Record<CertificateState, { tone: Tone; label: string }> = {
  [CertificateState.UNSPECIFIED]: { tone: "idle", label: "Unknown" },
  [CertificateState.OK]: { tone: "up", label: "OK" },
  [CertificateState.NOT_RENEWING]: { tone: "warn", label: "Not renewing" },
  [CertificateState.EXPIRES_SOON]: { tone: "warn", label: "Expires soon" },
  [CertificateState.EXPIRED]: { tone: "down", label: "Expired" },
};

/** The certificate a Proxy Host uses, with its state (as ListApps carries it) linking to the Reverse Proxy page. */
function CertificateLine({ p }: { p: ProxyHost }) {
  const page = usePluginLink("reverse-proxy");
  const id = p.fields?.certificateId ?? 0n;
  if (id === 0n) return <>None: plain HTTP</>;
  const cert = p.certificate;
  if (!cert) return <>#{String(id)}, not listed now</>;
  const s = certificateLook[cert.state];
  const pill = <Pill tone={s.tone}>{s.label}</Pill>;
  return (
    <span className="flex flex-wrap items-center gap-2">
      <span>{cert.name}</span>
      {page ? (
        <Link to={page} aria-label={`${s.label}: see the Reverse Proxy`}>
          {pill}
        </Link>
      ) : (
        pill
      )}
      {cert.message && <span className="text-xs text-muted-foreground">{cert.message}</span>}
    </span>
  );
}

/**
 * Reverse proxy: the Proxy Host serving the App, its certificate's state
 * and whether it is disabled or Offline. Its Edit changes the Proxy Host,
 * or removes the reverse proxy; Add reverse proxy… puts an App without a
 * Proxy Host behind it. Each writes through a Plan.
 */
export function ProxyPanel({ app, list, editing, slot }: { app: App; list: ListAppsResponse; editing: Editing; slot: DialogSlot }) {
  const p = app.proxyHost;
  const ip = app.dns.find((e) => !e.parent)?.ips[0] ?? app.dns[0]?.ips[0];
  const canAdd = !p && (app.reach === Reach.NOT_PROXIED || app.reach === Reach.PROXIED);
  const actions = p ? (
    <EditButton panel="proxy" title="Reverse proxy" editing={editing} />
  ) : (
    canAdd &&
    editing.now !== "proxy" && (
      <Button size="sm" variant="outline" disabled={editing.now !== undefined} onClick={() => editing.start("proxy")}>
        Add reverse proxy…
      </Button>
    )
  );
  let body: ReactNode;
  if (editing.now === "proxy" && p) body = <ProxyForm app={app} p={p} slot={slot} onDone={editing.done} />;
  else if (editing.now === "proxy" && canAdd) body = <AddProxyForm app={app} slot={slot} onDone={editing.done} />;
  else if (p) {
    const f = fieldsOf(p);
    body = (
      <div className="flex flex-col gap-3">
        {(!p.enabled || p.offline) && (
          <div className="flex flex-wrap gap-2">
            {!p.enabled && <Pill>Proxy Host disabled</Pill>}
            {p.offline && <Pill tone="warn">Offline</Pill>}
          </div>
        )}
        <Facts
          items={[
            { label: "Upstream", value: `${f.forwardScheme}://${f.forwardHost}:${f.forwardPort}`, mono: true },
            { label: "Certificate", value: <CertificateLine p={p} /> },
            { label: "Options", value: flags(f) || "None" },
            { label: "Proxy Host", value: `#${p.id} in Nginx Proxy Manager` },
          ]}
        />
      </div>
    );
  } else {
    body = (
      <p className="text-sm text-muted-foreground">
        {app.reach === Reach.NOT_PROXIED
          ? `Not proxied: pfSense resolves ${hostOf(app)} to ${ip ?? "its own address"}.`
          : app.reach === Reach.LINK_ONLY
            ? "A link only: no Proxy Host."
            : list.proxyHosts?.readAt
              ? "No Proxy Host serves it."
              : "Nginx Proxy Manager not read yet."}
      </p>
    );
  }
  return (
    <Panel title="Reverse proxy" actions={actions}>
      {body}
    </Panel>
  );
}

/** An IP address rather than a hostname. */
const isIP = (s: string) => /^\d{1,3}(\.\d{1,3}){3}$/.test(s) || s.includes(":");

/** A Proxy Host's fields; empty when ListApps sent none. */
const fieldsOf = (p: ProxyHost): ProxyHostFields => p.fields ?? create(ProxyHostFieldsSchema);

/** A Proxy Host's fields as the form edits them. */
const draftOf = (p: ProxyHostFields): ProxyDraft => ({
  scheme: p.forwardScheme === "https" ? "https" : "http",
  host: p.forwardHost,
  port: String(p.forwardPort),
  certificateId: p.certificateId,
  blockExploits: p.blockExploits,
  websockets: p.websockets,
  forceSsl: p.forceSsl,
  http2: p.http2,
  hsts: p.hsts,
  hstsSubdomains: p.hstsSubdomains,
});

/** The Reverse proxy Panel's Edit: the Proxy Host's fields, saved through a Plan, and Remove reverse proxy for a proxied App. */
function ProxyForm({ app, p, slot, onDone }: { app: App; p: ProxyHost; slot: DialogSlot; onDone: () => void }) {
  const certificates = useCertificates();
  const [draft, setDraft] = useState(() => draftOf(fieldsOf(p)));
  const upstream = fieldsOf(p).forwardHost;
  const [address, setAddress] = useState(isIP(upstream) ? upstream : (app.network?.address ?? ""));
  const host = hostOf(app);
  const done = () => {
    slot.close();
    onDone();
  };
  const save = (e: FormEvent) => {
    e.preventDefault();
    slot.open(
      <PlanDialog
        title={`Update ${app.name}'s Proxy Host`}
        action={{ action: { case: "changeProxyHost", value: { id: app.id, proxyHost: resolve(draft, certificates, host) } } }}
        confirm="Save"
        onClose={slot.close}
        onDone={done}
      />,
    );
  };
  const remove = () =>
    slot.open(
      <PlanDialog
        title={`Remove ${app.name}'s reverse proxy`}
        action={{ action: { case: "removeReverseProxy", value: { id: app.id, address: address.trim() } } }}
        confirm="Remove reverse proxy"
        danger
        onClose={slot.close}
        onDone={done}
      />,
    );
  return (
    <div className="flex flex-col gap-6">
      <form onSubmit={save} className="flex flex-col gap-4">
        <ProxyFields value={draft} onChange={setDraft} host={host} certificates={certificates} />
        <p className="text-sm text-muted-foreground">Saving shows the Plan first. What ddash doesn&apos;t manage, such as custom locations and the advanced config, stays as it is.</p>
        <FormActions>
          <Button variant="ghost" onClick={onDone}>
            Cancel
          </Button>
          <Button type="submit">Save</Button>
        </FormActions>
      </form>
      {app.reach === Reach.PROXIED && (
        <section aria-label="Remove reverse proxy" className="flex flex-col gap-3 border-t border-border pt-4">
          <p className="text-sm text-muted-foreground">
            Deletes the Proxy Host, and gives {host} a Host Override of its own in pfSense, resolving straight to the address.
          </p>
          <Text label="Address" value={address} onChange={setAddress} placeholder="198.18.1.60" />
          <div className="flex">
            <Button variant="danger" disabled={!address.trim()} onClick={remove}>
              Remove reverse proxy…
            </Button>
          </div>
        </section>
      )}
    </div>
  );
}

/** Add reverse proxy: a new Proxy Host's fields, the upstream at the App's address to begin with, added through a Plan. */
function AddProxyForm({ app, slot, onDone }: { app: App; slot: DialogSlot; onDone: () => void }) {
  const certificates = useCertificates();
  const [draft, setDraft] = useState<ProxyDraft>(() => ({ ...newProxy(), host: app.dns.find((e) => !e.parent)?.ips[0] ?? app.network?.address ?? "" }));
  const host = hostOf(app);
  const save = (e: FormEvent) => {
    e.preventDefault();
    slot.open(
      <PlanDialog
        title={`Add a reverse proxy to ${app.name}`}
        action={{ action: { case: "addReverseProxy", value: { id: app.id, proxyHost: resolve(draft, certificates, host) } } }}
        confirm="Add reverse proxy"
        onClose={slot.close}
        onDone={() => {
          slot.close();
          onDone();
        }}
      />,
    );
  };
  return (
    <form onSubmit={save} className="flex flex-col gap-4">
      <p className="text-sm text-muted-foreground">
        A Proxy Host for {[host, ...app.aliases].join(", ")}, each an alias of the Reverse Proxy&apos;s Host Override in place of its own Host Override.
      </p>
      <ProxyFields value={draft} onChange={setDraft} host={host} certificates={certificates} />
      <FormActions>
        <Button variant="ghost" onClick={onDone}>
          Cancel
        </Button>
        <Button type="submit">Save</Button>
      </FormActions>
    </form>
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
