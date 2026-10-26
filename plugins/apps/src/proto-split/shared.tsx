// PROTOTYPE (wayfinder #139): what the variants share: copy, the plans an
// Action runs, the step runner, the Proxy Host fields and the Warning fixes.
// The layouts are each variant's own.
import {
  Button,
  Checkbox,
  Dialog,
  Field,
  FormActions,
  Notice,
  Pill,
  Select,
  StatusList,
  TextInput,
  type StatusItem,
  type Tone,
} from "@ddash/ui";
import { useState, type ReactNode } from "react";
import { useSearchParams } from "react-router";
import {
  certById,
  certs,
  coveringCert,
  homeDomain,
  hostOf,
  newId,
  reverseProxy,
  scenarios,
  store,
  takenBy,
  workloads,
  type Net,
  type PApp,
  type ProxyHost,
  type Reach,
  type Scenario,
  type Warning,
  type WarningKind,
  type Workload,
} from "./data";

export function useScenario(): Scenario {
  const [params] = useSearchParams();
  const s = params.get("scenario") as Scenario;
  return s in scenarios ? s : "normal";
}

/** Keeps ?variant= and ?scenario= on a link into the detail route. */
export function useKeep() {
  const [params] = useSearchParams();
  const keep = new URLSearchParams();
  for (const k of ["variant", "scenario"]) if (params.get(k)) keep.set(k, params.get(k)!);
  return keep.toString() ? `?${keep}` : "";
}

export const reachLabel: Record<Reach, string> = { proxied: "Proxied", "not-proxied": "Not proxied", "link-only": "Link only" };
const reachTone: Record<Reach, Tone> = { proxied: "info", "not-proxied": "idle", "link-only": "idle" };

export function ReachPill({ reach }: { reach: Reach }) {
  return <Pill tone={reachTone[reach]}>{reachLabel[reach]}</Pill>;
}

export function CheckPill({ app }: { app: PApp }) {
  if (!app.check) return <span className="text-xs text-muted-foreground">no Check</span>;
  return (
    <Pill tone={app.check.tone} dot>
      {app.check.label}
    </Pill>
  );
}

export const workloadText = (w?: Workload) => (w ? `${w.kind} ${w.name} · ${w.state}${w.override ? " (override)" : ""}` : "none");

export function certText(p?: ProxyHost) {
  if (!p) return "—";
  const c = certById(p.certId);
  if (!c) return "none (plain HTTP)";
  const days = Math.round((c.expires - Date.now()) / 86_400_000);
  return `${c.name} · expires in ${days} days`;
}

export const pathTone: Record<Net["path"], Tone> = { Tunnelled: "up", Direct: "info", "No Internet": "maint" };

export function NetLine({ net }: { net?: Net }) {
  if (!net) return <span className="text-muted-foreground">No Device at this address</span>;
  return (
    <span className="flex flex-wrap items-center gap-2">
      <Pill tone={pathTone[net.path]}>{net.path}</Pill>
      <Pill tone={net.adsAllowed ? "warn" : "idle"}>{net.adsAllowed ? "Ads allowed" : "Ads blocked"}</Pill>
      <span className="text-xs text-muted-foreground">
        {net.via ? `${net.via}, ` : ""}
        {net.address}
        {net.device && (
          <>
            {" · "}
            <a href={`/network/devices/${net.device}`} className="underline hover:text-foreground">
              Device {net.device}
            </a>
          </>
        )}
      </span>
    </span>
  );
}

/** "alias of proxy.home.example", "Host Override → 198.18.0.1", "not registered". */
export const dnsText = (d: PApp["dns"][number]) =>
  d.how === "alias" ? `alias of ${d.parent}${d.parent === reverseProxy.host ? " (Reverse Proxy)" : ` → ${d.ip}`}` : d.how === "override" ? `Host Override → ${d.ip}` : "not registered";

export const flags = (p: ProxyHost) =>
  [p.blockExploits && "Block Common Exploits", p.websockets && "Websockets", p.forceSsl && "Force SSL", p.http2 && "HTTP/2", p.hsts && "HSTS"].filter(Boolean) as string[];

// ---- Warnings ---------------------------------------------------------------

export type FixEffect = "done" | "edit" | "add-proxy" | "merge";
export interface Fix {
  label: string;
  danger?: boolean;
  effect: FixEffect;
  /** What ddash would do, for the log. */
  log: string;
}

export const warningTitle: Record<WarningKind, string> = {
  "no-host-override": "Proxy Host without a Host Override",
  "no-proxy-host": "Host Override without a Proxy Host",
  "no-workload": "No Workload",
  "no-tls": "No TLS",
  "dns-bypass": "DNS bypasses the Reverse Proxy",
  "registered-twice": "Registered twice",
  "hostname-two-apps": "Hostname on two Apps",
  "url-not-on-proxy-host": "URL not on its Proxy Host",
};

export function fixesFor(app: PApp, w: Warning): Fix[] {
  const host = hostOf(app.url);
  switch (w.kind) {
    case "no-host-override":
      return [
        { label: `Add alias to ${reverseProxy.host}`, effect: "done", log: `pfSense: add alias ${host} to ${reverseProxy.host}, apply once` },
        { label: "Delete Proxy Host", danger: true, effect: "done", log: `NPM: delete Proxy Host #${app.proxy?.id}` },
      ];
    case "no-proxy-host":
      return [
        { label: "Add Proxy Host…", effect: "add-proxy", log: "" },
        { label: "Delete alias", danger: true, effect: "done", log: `pfSense: delete alias ${host}, apply once` },
      ];
    case "no-workload":
      return [{ label: "Pick a Workload…", effect: "edit", log: "" }];
    case "no-tls":
      return [{ label: "Pick a certificate…", effect: "edit", log: "" }];
    case "dns-bypass":
      return [
        { label: "Point at Reverse Proxy", effect: "done", log: `pfSense: replace Host Override ${host} → ${app.overrideIp} with an alias of ${reverseProxy.host}, apply once` },
        { label: "Delete Proxy Host", danger: true, effect: "done", log: `NPM: delete Proxy Host #${app.proxy?.id}` },
      ];
    case "registered-twice":
      return [
        { label: `Delete Host Override ${host}`, danger: true, effect: "done", log: `pfSense: delete Host Override ${host}, apply once` },
        { label: "Delete alias", danger: true, effect: "done", log: `pfSense: delete alias ${host} of ${reverseProxy.host}, apply once` },
      ];
    case "hostname-two-apps":
      return [{ label: "Merge…", effect: "merge", log: "" }];
    case "url-not-on-proxy-host":
      return [{ label: "Use the Proxy Host's name", effect: "done", log: `ddash: set the App's URL to the Proxy Host's first domain` }];
  }
}

/** Applies a "done" fix in the store: drops the Warning (and its twin on the other App) and does the obvious. */
export function applyFix(app: PApp, w: Warning, fix: Fix) {
  store.log(fix.log);
  if (w.kind === "dns-bypass" && fix.label === "Point at Reverse Proxy") {
    store.update(app.id, (a) => ({ ...a, reach: "proxied", overrideIp: undefined, dns: a.dns.map((d) => ({ ...d, how: "alias", ip: reverseProxy.ip, parent: reverseProxy.host })), warnings: a.warnings.filter((x) => x !== w) }));
    return;
  }
  if (fix.label === "Delete Proxy Host") {
    store.update(app.id, (a) => ({ ...a, proxy: undefined, warnings: a.warnings.filter((x) => x !== w) }));
    return;
  }
  if (w.kind === "no-host-override") {
    store.update(app.id, (a) => ({ ...a, dns: a.dns.map((d) => ({ ...d, how: "alias", ip: reverseProxy.ip, parent: reverseProxy.host })), warnings: a.warnings.filter((x) => x !== w) }));
    return;
  }
  if (w.kind === "no-proxy-host") {
    store.remove(app.id);
    return;
  }
  store.update(app.id, (a) => ({ ...a, warnings: a.warnings.filter((x) => x !== w) }));
}

export function toggleIgnore(app: PApp, w: Warning) {
  store.log(`ddash: ${w.ignored ? "un-ignore" : "ignore"} "${warningTitle[w.kind]}" on ${app.name}`);
  store.update(app.id, (a) => ({ ...a, warnings: a.warnings.map((x) => (x === w ? { ...x, ignored: !x.ignored } : x)) }));
}

/** A Warning's fix buttons and Ignore; edit and add-proxy are the variant's to open. */
export function WarningButtons({ app, w, onEdit, onAddProxy, size = "sm" }: { app: PApp; w: Warning; onEdit: () => void; onAddProxy: () => void; size?: "sm" | "md" }) {
  const [merging, setMerging] = useState(false);
  return (
    <>
      {fixesFor(app, w).map((f) => (
        <Button
          key={f.label}
          size={size}
          variant={f.danger ? "danger" : "outline"}
          onClick={() => {
            if (f.effect === "edit") onEdit();
            else if (f.effect === "add-proxy") onAddProxy();
            else if (f.effect === "merge") setMerging(true);
            else applyFix(app, w, f);
          }}
        >
          {f.label}
        </Button>
      ))}
      <Button size={size} variant="ghost" icon={w.ignored ? "eye" : "eye-off"} onClick={() => toggleIgnore(app, w)}>
        {w.ignored ? "Un-ignore" : "Ignore"}
      </Button>
      {merging && <MergeDialog app={app} otherId={w.other!} onClose={() => setMerging(false)} />}
    </>
  );
}

/** Merge: keep one App's id and settings, delete the other. */
function MergeDialog({ app, otherId, onClose }: { app: PApp; otherId: string; onClose: () => void }) {
  const other = store.get().apps.find((a) => a.id === otherId);
  const [keep, setKeep] = useState(app.reach === "link-only" && other ? other.id : app.id);
  if (!other) return null;
  const pair = [app, other];
  const kept = pair.find((a) => a.id === keep)!;
  const dropped = pair.find((a) => a.id !== keep)!;
  return (
    <Dialog
      title={`Merge ${app.name} and ${other.name}`}
      subtitle="One App keeps its id, settings and Check history; the other is deleted. Nothing changes in pfSense or Nginx Proxy Manager."
      onClose={onClose}
      footer={
        <FormActions>
          <Button variant="ghost" onClick={onClose}>
            Cancel
          </Button>
          <Button
            onClick={() => {
              store.log(`ddash: merge ${dropped.name} into ${kept.name}; delete ${dropped.name}'s record and Check`);
              store.remove(dropped.id);
              store.update(kept.id, (a) => ({ ...a, warnings: a.warnings.filter((w) => w.kind !== "hostname-two-apps") }));
              onClose();
            }}
          >
            Merge into {kept.name}
          </Button>
        </FormActions>
      }
    >
      <div className="flex flex-col gap-2">
        {pair.map((a) => (
          <label key={a.id} className="flex cursor-pointer items-start gap-3 rounded-ui-sm border border-border p-3 has-checked:border-primary">
            <input type="radio" name="keep" checked={keep === a.id} onChange={() => setKeep(a.id)} className="mt-1" />
            <span className="flex flex-col">
              <span className="font-semibold">Keep {a.name}</span>
              <span className="text-xs text-muted-foreground">
                {reachLabel[a.reach]} · {a.url} · group {a.group || "none"} · id {a.id}
              </span>
            </span>
          </label>
        ))}
      </div>
    </Dialog>
  );
}

// ---- Plans and the runner ---------------------------------------------------

export type System = "ddash" | "pfSense" | "Nginx Proxy Manager";
export interface Step {
  label: string;
  system: System;
}

export interface Draft {
  name: string;
  url: string;
  icon: string;
  group: string;
  proxy: boolean;
  /** With proxy off: create a Host Override to address, or link only. */
  dns: "override" | "link";
  address: string;
  ph: ProxyHost;
}

export function emptyDraft(): Draft {
  return { name: "", url: "", icon: "", group: "", proxy: true, dns: "override", address: "", ph: blankProxy(0) };
}

export function blankProxy(certId: number): ProxyHost {
  return { id: 0, scheme: "http", host: "", port: 80, blockExploits: true, websockets: false, forceSsl: certId > 0, http2: certId > 0, hsts: false, certId };
}

export const inHome = (host: string) => host === homeDomain || host.endsWith(`.${homeDomain}`);

export function planAdd(d: Draft): Step[] {
  const host = hostOf(d.url);
  const steps: Step[] = [{ label: `Check ${host} is free`, system: "ddash" }];
  if (d.proxy) {
    steps.push(
      { label: `Create Proxy Host ${host} → ${d.ph.scheme}://${d.ph.host || "?"}:${d.ph.port}`, system: "Nginx Proxy Manager" },
      { label: `Add alias ${host} to ${reverseProxy.host}`, system: "pfSense" },
      { label: "Apply the DNS Resolver (Unbound restarts once)", system: "pfSense" },
    );
  } else if (d.dns === "override") {
    steps.push(
      { label: `Create Host Override ${host} → ${d.address || "?"}`, system: "pfSense" },
      { label: "Apply the DNS Resolver (Unbound restarts once)", system: "pfSense" },
    );
  }
  steps.push({ label: `Create the App ${d.name || host} and its Check`, system: "ddash" });
  return steps;
}

export function planUrlChange(app: PApp, url: string): Step[] {
  const from = hostOf(app.url);
  const to = hostOf(url);
  if (from === to) return [{ label: "Update the App's URL", system: "ddash" }];
  const steps: Step[] = [{ label: `Check ${to} is free`, system: "ddash" }];
  if (app.reach === "proxied") {
    steps.push(
      { label: `Update Proxy Host #${app.proxy?.id}: ${from} → ${to}`, system: "Nginx Proxy Manager" },
      { label: `Add alias ${to}, delete alias ${from} (one write)`, system: "pfSense" },
      { label: "Apply the DNS Resolver once", system: "pfSense" },
    );
  } else if (app.reach === "not-proxied") {
    steps.push(
      { label: `Create Host Override ${to} → ${app.overrideIp}, delete ${from} (one write)`, system: "pfSense" },
      { label: "Apply the DNS Resolver once", system: "pfSense" },
    );
  }
  steps.push({ label: "Update the App's URL and links (its settings and Check stay)", system: "ddash" });
  return steps;
}

export function planProxyEdit(app: PApp, p: ProxyHost): Step[] {
  const before = app.proxy!;
  const changed = (Object.keys(p) as (keyof ProxyHost)[]).filter((k) => p[k] !== before[k]);
  if (!changed.length) return [];
  const steps: Step[] = [{ label: `Update Proxy Host #${before.id}: ${changed.join(", ")}`, system: "Nginx Proxy Manager" }];
  if (p.host !== before.host || p.port !== before.port) steps.push({ label: "Re-match the Workload from the new upstream", system: "ddash" });
  return steps;
}

export function planAddProxy(app: PApp, p: ProxyHost): Step[] {
  const host = hostOf(app.url);
  return [
    { label: `Create Proxy Host ${host} → ${p.scheme}://${p.host || "?"}:${p.port}`, system: "Nginx Proxy Manager" },
    { label: `Replace Host Override ${host} → ${app.overrideIp ?? "?"} with an alias of ${reverseProxy.host}`, system: "pfSense" },
    { label: "Apply the DNS Resolver once", system: "pfSense" },
    { label: "Link the App to Proxy Host and re-match its Workload", system: "ddash" },
  ];
}

export function planDelete(app: PApp): Step[] {
  const hosts = app.dns.filter((d) => d.how !== "none").map((d) => d.host);
  const steps: Step[] = [];
  if (app.proxy) steps.push({ label: `Delete Proxy Host #${app.proxy.id}`, system: "Nginx Proxy Manager" });
  if (hosts.length) {
    steps.push(
      { label: `Delete ${app.reach === "proxied" ? "aliases" : "Host Override"} ${hosts.join(", ")}`, system: "pfSense" },
      { label: "Apply the DNS Resolver once", system: "pfSense" },
    );
  }
  steps.push({ label: "Delete the App, its settings and its Check", system: "ddash" });
  return steps;
}

type StepState = "pending" | "running" | "done" | "failed";

/** Runs steps one at a time, stopping at the first failure (the scenario's system). No rollback. */
export function useRunner() {
  const scenario = useScenario();
  const [states, setStates] = useState<StepState[]>();
  const [steps, setSteps] = useState<Step[]>([]);
  const failing: System | undefined = scenario === "fail-pfsense" ? "pfSense" : scenario === "fail-npm" ? "Nginx Proxy Manager" : undefined;
  const run = async (plan: Step[]): Promise<number | undefined> => {
    setSteps(plan);
    const s: StepState[] = plan.map(() => "pending");
    setStates([...s]);
    for (let i = 0; i < plan.length; i++) {
      s[i] = "running";
      setStates([...s]);
      await new Promise((r) => setTimeout(r, 450));
      if (plan[i].system === failing) {
        s[i] = "failed";
        setStates([...s]);
        store.log(`${plan[i].system} failed at: ${plan[i].label}. Stopped; nothing rolled back.`);
        return i;
      }
      s[i] = "done";
      setStates([...s]);
    }
    store.log(plan.map((p) => `${p.system}: ${p.label}`).join(" → "));
    return undefined;
  };
  return { run, steps, states, running: states?.includes("running") ?? false, reset: () => setStates(undefined) };
}

const stepTone: Record<StepState, Tone> = { pending: "idle", running: "maint", done: "up", failed: "down" };

/** A plan as a timeline: before it runs, all pending; while it runs, each step's state. */
export function Steps({ steps, states }: { steps: Step[]; states?: StepState[] }) {
  const items: StatusItem[] = steps.map((s, i) => {
    const st = states?.[i] ?? "pending";
    return {
      tone: stepTone[st],
      title: s.label,
      meta: s.system,
      aside: st === "pending" ? undefined : st === "running" ? "…" : st,
    };
  });
  return <StatusList timeline items={items} />;
}

/** What a failure leaves behind: the ordinary Warnings a sync would then find. */
export function failureWarnings(app: PApp, url: string, failedAt: Step): Warning[] {
  const from = hostOf(app.url);
  const to = hostOf(url);
  if (failedAt.system === "pfSense" && app.reach === "proxied") {
    return [
      { kind: "no-host-override", detail: `${to} has a Proxy Host (#${app.proxy?.id}) but no Host Override.` },
      { kind: "no-proxy-host", detail: `${from} still points at the Reverse Proxy, but no Proxy Host serves it.` },
      { kind: "url-not-on-proxy-host", detail: `The App's URL ${from} is no longer among Proxy Host #${app.proxy?.id}'s names.` },
    ];
  }
  return [];
}

// ---- Forms ------------------------------------------------------------------

export const groupsOf = (apps: readonly PApp[]) => [...new Set(apps.map((a) => a.group).filter(Boolean))].sort();

export function Groups({ id, apps }: { id: string; apps: readonly PApp[] }) {
  return (
    <datalist id={id}>
      {groupsOf(apps).map((g) => (
        <option key={g} value={g} />
      ))}
    </datalist>
  );
}

/** "Is the name free": every Host Override, alias and Proxy Host domain. */
export function nameError(url: string, except?: string) {
  const host = hostOf(url);
  if (!host) return undefined;
  const t = takenBy(store.get(), host, except);
  return t ? `${host} is taken: it's ${t.name === "the Reverse Proxy" ? "the Reverse Proxy's own name" : `a hostname of ${t.name}`}.` : undefined;
}

/**
 * The Proxy Host fields Add App, Edit and "Add reverse proxy" share: the
 * upstream, the four flags, and the certificate from Nginx Proxy Manager's
 * existing ones. The server's own rules show as disabled boxes: no
 * certificate turns off Force SSL and HTTP/2, no Force SSL turns off HSTS.
 */
export function ProxyFields({ value: p, onChange, host }: { value: ProxyHost; onChange: (p: ProxyHost) => void; host: string }) {
  const set = (patch: Partial<ProxyHost>) => {
    const next = { ...p, ...patch };
    if (!next.certId) Object.assign(next, { forceSsl: false, http2: false });
    if (!next.forceSsl) next.hsts = false;
    onChange(next);
  };
  const covering = coveringCert(host);
  return (
    <div className="flex flex-col gap-4">
      <div className="grid grid-cols-[6rem_1fr_6rem] gap-2">
        <Field label="Scheme">
          <Select value={p.scheme} onChange={(e) => set({ scheme: e.target.value as "http" | "https" })}>
            <option value="http">http</option>
            <option value="https">https</option>
          </Select>
        </Field>
        <Field label="Upstream host">
          <TextInput value={p.host} placeholder="198.18.1.30" onChange={(e) => set({ host: e.target.value })} />
        </Field>
        <Field label="Port">
          <TextInput value={String(p.port)} inputMode="numeric" onChange={(e) => set({ port: Number(e.target.value) || 0 })} />
        </Field>
      </div>
      <Field
        label="Certificate"
        hint={covering ? `From Nginx Proxy Manager. ${certById(covering)!.name} covers ${host || "the hostname"}.` : "From Nginx Proxy Manager. None of them covers this hostname; ddash never requests one."}
      >
        <Select value={String(p.certId)} onChange={(e) => set({ certId: Number(e.target.value) })}>
          <option value="0">None (plain HTTP)</option>
          {certs.map((c) => (
            <option key={c.id} value={c.id}>
              {c.name}
              {c.id === covering ? " (covers it)" : ""}
            </option>
          ))}
        </Select>
      </Field>
      <div className="grid grid-cols-2 gap-2">
        <Checkbox label="Block Common Exploits" checked={p.blockExploits} onChange={(e) => set({ blockExploits: e.target.checked })} />
        <Checkbox label="Websockets" checked={p.websockets} onChange={(e) => set({ websockets: e.target.checked })} />
        <Checkbox label="Force SSL" checked={p.forceSsl} disabled={!p.certId} onChange={(e) => set({ forceSsl: e.target.checked })} />
        <Checkbox label="HTTP/2" checked={p.http2} disabled={!p.certId} onChange={(e) => set({ http2: e.target.checked })} />
        <Checkbox label="HSTS" checked={p.hsts} disabled={!p.forceSsl} onChange={(e) => set({ hsts: e.target.checked })} />
      </div>
    </div>
  );
}

export function WorkloadSelect({ app, value, onChange }: { app: PApp; value: string; onChange: (v: string) => void }) {
  const matched = app.workload && !app.workload.override ? app.workload.name : "none";
  return (
    <Field label="Workload" hint="Matched from the upstream (proxied), the Host Override's address and the URL's port (Not proxied), or the URL (Link only).">
      <Select value={value} onChange={(e) => onChange(e.target.value)}>
        <option value="">Matched: {matched}</option>
        {workloads.map((w) => (
          <option key={w.name} value={w.name}>
            {w.kind} {w.name} ({w.where})
          </option>
        ))}
      </Select>
    </Field>
  );
}

/** The Notices a scenario puts above the Apps. */
export function ScenarioNotices() {
  const s = useScenario();
  if (s === "bad-host")
    return (
      <Notice tone="warn" title="Proxied Apps can't be created or moved">
        Set the Reverse Proxy's Host to the name of its Host Override to create proxied Apps. It's 198.18.1.20 now.
      </Notice>
    );
  if (s === "npm-down")
    return <Notice tone="warn">Couldn't reach Nginx Proxy Manager: dial tcp 198.18.1.20:81: connection refused. Showing the Proxy Hosts of the last read; proxied writes wait.</Notice>;
  return null;
}

/** Whether a proxied write is blocked in this scenario, and why. */
export function useProxyBlocked() {
  const s = useScenario();
  return s === "bad-host" ? "The Reverse Proxy's Host isn't a Host Override name." : s === "npm-down" ? "Nginx Proxy Manager is unreachable." : undefined;
}

/** Builds the App Add App creates, for the store. */
export function appFromDraft(d: Draft): PApp {
  const host = hostOf(d.url);
  const reach: Reach = d.proxy ? "proxied" : d.dns === "override" ? "not-proxied" : "link-only";
  return {
    id: newId(),
    name: d.name || host,
    icon: d.icon,
    group: d.group,
    hidden: false,
    url: d.url,
    aliases: [],
    reach,
    proxy: d.proxy ? { ...d.ph, id: 20 + Math.floor(Math.random() * 50) } : undefined,
    overrideIp: reach === "not-proxied" ? d.address : undefined,
    dns:
      reach === "proxied"
        ? [{ host, how: "alias", ip: reverseProxy.ip, parent: reverseProxy.host }]
        : reach === "not-proxied"
          ? [{ host, how: "override", ip: d.address }]
          : [],
    check: { tone: "idle", label: "pending", bars: [] },
    warnings: [],
  };
}

/** A two-line label/value used in detail views. */
export function Row({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="flex flex-col gap-0.5">
      <span className="text-xs text-muted-foreground">{label}</span>
      <span className="text-sm">{children}</span>
    </div>
  );
}

// ---- Edit -------------------------------------------------------------------

export interface EditDraft {
  name: string;
  icon: string;
  group: string;
  hidden: boolean;
  url: string;
  /** Comma-separated. */
  aliases: string;
  ph?: ProxyHost;
  workload: string;
}

export const editDraft = (a: PApp): EditDraft => ({
  name: a.name,
  icon: a.icon,
  group: a.group,
  hidden: a.hidden,
  url: a.url,
  aliases: a.aliases.join(", "),
  ph: a.proxy && { ...a.proxy },
  workload: a.workload?.override ? a.workload.name : "",
});

const splitAliases = (s: string) =>
  s
    .split(/[\s,]+/)
    .map((x) => x.trim().toLowerCase())
    .filter(Boolean);

/** Edit's plan: display settings first (ddash only), then the Proxy Host, the aliases, then the URL change. */
export function planEdit(app: PApp, d: EditDraft): Step[] {
  const steps: Step[] = [];
  const display = (["name", "icon", "group", "hidden", "workload"] as const).filter((k) => d[k] !== editDraft(app)[k]);
  if (display.length) steps.push({ label: `Save ${display.join(", ")}`, system: "ddash" });
  if (d.ph && app.proxy) steps.push(...planProxyEdit(app, d.ph));
  const before = new Set(app.aliases);
  const after = new Set(splitAliases(d.aliases));
  const added = [...after].filter((x) => !before.has(x));
  const gone = [...before].filter((x) => !after.has(x));
  if (added.length || gone.length) {
    const what = [added.length && `+ ${added.join(", ")}`, gone.length && `− ${gone.join(", ")}`].filter(Boolean).join(" ");
    if (added.length) steps.push({ label: `Check ${added.join(", ")} ${added.length > 1 ? "are" : "is"} free`, system: "ddash" });
    if (app.proxy) steps.push({ label: `Update Proxy Host #${app.proxy.id} domains: ${what}`, system: "Nginx Proxy Manager" });
    if (app.reach !== "link-only") {
      const parent = app.reach === "proxied" ? reverseProxy.host : hostOf(app.url);
      steps.push({ label: `Aliases of ${parent}: ${what}`, system: "pfSense" });
    }
  }
  if (hostOf(d.url) !== hostOf(app.url) || d.url !== app.url) steps.push(...planUrlChange(app, d.url));
  // One apply at the end, however many pfSense writes.
  const rest = steps.filter((s) => !s.label.startsWith("Apply"));
  const lastPf = rest.map((s) => s.system).lastIndexOf("pfSense");
  if (lastPf >= 0) rest.splice(lastPf + 1, 0, { label: "Apply the DNS Resolver once", system: "pfSense" });
  return rest;
}

/** Writes an Edit's outcome to the store: everything when it ran through, else what ran before the failure plus the Warnings it leaves. */
export function commitEdit(app: PApp, d: EditDraft, plan: Step[], failedAt?: number) {
  const ran = (pred: (s: Step) => boolean) => plan.some((s, i) => pred(s) && (failedAt === undefined || i < failedAt));
  const w = d.workload ? workloads.find((x) => x.name === d.workload) : undefined;
  store.update(app.id, (a) => {
    let next: PApp = {
      ...a,
      name: d.name || a.name,
      icon: d.icon,
      group: d.group,
      hidden: d.hidden,
      workload: w ? { ...w, override: true } : a.workload?.override ? undefined : a.workload,
    };
    if (d.ph && ran((s) => s.label.startsWith("Update Proxy Host") && !s.label.includes("domains"))) next = { ...next, proxy: d.ph };
    if (failedAt === undefined) {
      const to = hostOf(d.url);
      const aliases = splitAliases(d.aliases);
      const mk = (host: string) => (a.reach === "proxied" ? { host, how: "alias" as const, ip: reverseProxy.ip, parent: reverseProxy.host } : { host, how: "override" as const, ip: a.overrideIp });
      next = {
        ...next,
        url: d.url,
        aliases,
        dns: a.reach === "link-only" ? a.dns : [mk(to), ...aliases.map(mk)],
        warnings: next.warnings.filter((x) => x.kind !== "no-tls" || !d.ph?.certId),
      };
    } else {
      next = { ...next, warnings: [...next.warnings, ...failureWarnings(a, d.url, plan[failedAt])] };
    }
    return next;
  });
}
