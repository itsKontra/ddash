// PROTOTYPE (wayfinder #139): the in-memory Apps the variants draw. Nothing
// here calls a Component; every "write" mutates this store and logs what
// ddash would have sent where. Throwaway; lives on prototype/apps-split only.
import type { Tone } from "@ddash/ui";
import { useSyncExternalStore } from "react";

/** How an App is reached: behind the Reverse Proxy, by its own Host Override, or a bare link. */
export type Reach = "proxied" | "not-proxied" | "link-only";

/** The Reverse Proxy: the Host Override nginx-config's Host names, and its address. */
export const reverseProxy = { host: "proxy.home.example", ip: "198.18.1.20" };
export const homeDomain = "home.example";

/** A Proxy Host as Nginx Proxy Manager's API has it (v2.10 field set). */
export interface ProxyHost {
  id: number;
  scheme: "http" | "https";
  host: string;
  port: number;
  blockExploits: boolean;
  websockets: boolean;
  forceSsl: boolean;
  http2: boolean;
  hsts: boolean;
  /** 0 is none. */
  certId: number;
}

export interface Cert {
  id: number;
  name: string;
  domains: string[];
  expires: number;
  provider: "letsencrypt" | "other";
}

/** One of an App's hostnames and how pfSense registers it. */
export interface Dns {
  host: string;
  how: "alias" | "override" | "none";
  /** Where it resolves: the Reverse Proxy's address for an alias of its Host Override. */
  ip?: string;
  /** The Host Override an alias hangs off. */
  parent?: string;
}

export interface Workload {
  kind: "LXC" | "VM" | "Docker";
  name: string;
  where: string;
  state: "running" | "stopped" | "exited";
  override?: boolean;
}

export type Path = "Tunnelled" | "Direct" | "No Internet";

/** The internet path and Ads Allowed of the Workload's address (a Docker App's host's). */
export interface Net {
  address: string;
  path: Path;
  adsAllowed: boolean;
  device?: string;
  via?: string;
}

export interface Check {
  tone: Tone;
  label: string;
  bars: Tone[];
}

export type WarningKind =
  | "no-host-override"
  | "no-proxy-host"
  | "no-workload"
  | "no-tls"
  | "dns-bypass"
  | "registered-twice"
  | "hostname-two-apps"
  | "url-not-on-proxy-host";

export interface Warning {
  kind: WarningKind;
  detail: string;
  ignored?: boolean;
  /** For hostname-two-apps: the other App's id. */
  other?: string;
}

export interface PApp {
  id: string;
  name: string;
  icon: string;
  group: string;
  hidden: boolean;
  url: string;
  aliases: string[];
  reach: Reach;
  proxy?: ProxyHost;
  /** A Not-proxied App's own Host Override address. */
  overrideIp?: string;
  dns: Dns[];
  workload?: Workload;
  check?: Check;
  net?: Net;
  warnings: Warning[];
}

/** A touched App whose sources are all gone: its settings, kept until re-attached or deleted. */
export interface Orphan {
  id: string;
  name: string;
  icon: string;
  group: string;
  was: string;
}

export type Scenario = "normal" | "bad-host" | "npm-down" | "fail-pfsense" | "fail-npm";
export const scenarios: Record<Scenario, string> = {
  normal: "Normal",
  "bad-host": "Reverse Proxy Host is an IP",
  "npm-down": "Nginx Proxy Manager unreachable",
  "fail-pfsense": "Writes fail at pfSense",
  "fail-npm": "Writes fail at Nginx Proxy Manager",
};

const day = 86_400_000;
const now = Date.now();
const up = (n = 30): Tone[] => Array.from({ length: n }, (_, i) => (i === 7 ? "warn" : "up"));
const bars = (pattern: string): Tone[] => [...pattern].map((c) => ({ u: "up", d: "down", w: "warn", m: "maint", i: "idle" })[c] as Tone);

export const certs: Cert[] = [
  { id: 1, name: "*.home.example", domains: ["*.home.example", "home.example"], expires: now + 61 * day, provider: "letsencrypt" },
  { id: 2, name: "cloud.home.example", domains: ["cloud.home.example"], expires: now + 9 * day, provider: "letsencrypt" },
  { id: 3, name: "Internal CA (lab)", domains: ["*.lab.home.example"], expires: now + 300 * day, provider: "other" },
];

const alias = (host: string): Dns => ({ host, how: "alias", ip: reverseProxy.ip, parent: reverseProxy.host });
const override = (host: string, ip: string): Dns => ({ host, how: "override", ip });
const ph = (id: number, scheme: "http" | "https", host: string, port: number, more: Partial<ProxyHost> = {}): ProxyHost => ({
  id,
  scheme,
  host,
  port,
  blockExploits: true,
  websockets: false,
  forceSsl: true,
  http2: true,
  hsts: false,
  certId: 1,
  ...more,
});

const media: Net = { address: "198.18.1.30", path: "Direct", adsAllowed: true, device: "media", via: "Docker host media" };

const seed: PApp[] = [
  {
    id: "proxy:grafana.home.example",
    name: "Grafana",
    icon: "",
    group: "Monitoring",
    hidden: false,
    url: "https://grafana.home.example",
    aliases: [],
    reach: "proxied",
    proxy: ph(3, "http", "198.18.1.120", 3000),
    dns: [alias("grafana.home.example")],
    workload: { kind: "LXC", name: "grafana", where: "pve/120", state: "running" },
    check: { tone: "up", label: "up · 12 ms", bars: up() },
    net: { address: "198.18.1.120", path: "Tunnelled", adsAllowed: false, device: "grafana" },
    warnings: [],
  },
  {
    id: "proxy:jellyfin.home.example",
    name: "Jellyfin",
    icon: "",
    group: "Media",
    hidden: false,
    url: "https://jellyfin.home.example",
    aliases: ["media.home.example", "tv.home.example"],
    reach: "proxied",
    proxy: ph(5, "http", "198.18.1.30", 8096, { websockets: true }),
    dns: [alias("jellyfin.home.example"), alias("media.home.example"), alias("tv.home.example")],
    workload: { kind: "Docker", name: "jellyfin", where: "media", state: "exited" },
    check: { tone: "down", label: "down · HTTP 502", bars: bars("uuuuuuuuuuuuuuuuuuuuuuuuuuuddd") },
    net: media,
    warnings: [],
  },
  {
    id: "a7f3c0de",
    name: "Home Assistant",
    icon: "home-assistant",
    group: "Home",
    hidden: false,
    url: "https://ha.home.example",
    aliases: [],
    reach: "proxied",
    proxy: ph(7, "http", "198.18.1.40", 8123, { websockets: true, hsts: true }),
    dns: [alias("ha.home.example")],
    workload: { kind: "VM", name: "home-assistant", where: "pve/110", state: "running" },
    check: { tone: "up", label: "up · 31 ms", bars: up() },
    net: { address: "198.18.1.40", path: "Tunnelled", adsAllowed: false, device: "home-assistant" },
    warnings: [],
  },
  {
    id: "proxy:paperless.home.example",
    name: "Paperless",
    icon: "paperless-ngx",
    group: "Office",
    hidden: false,
    url: "https://paperless.home.example",
    aliases: [],
    reach: "proxied",
    proxy: ph(9, "http", "198.18.1.30", 8000),
    dns: [{ host: "paperless.home.example", how: "none" }],
    workload: { kind: "Docker", name: "paperless", where: "media", state: "running" },
    check: { tone: "down", label: "down · no such host", bars: bars("iiiiiiiiiiiiiiiiiiiiiiiiiiiddd") },
    net: media,
    warnings: [{ kind: "no-host-override", detail: "paperless.home.example has a Proxy Host but no Host Override, so nothing resolves it." }],
  },
  {
    id: "proxy:sync.home.example",
    name: "Syncthing",
    icon: "",
    group: "",
    hidden: false,
    url: "http://sync.home.example",
    aliases: [],
    reach: "proxied",
    proxy: ph(11, "http", "198.18.1.50", 8384, { certId: 0, forceSsl: false, http2: false }),
    dns: [alias("sync.home.example")],
    workload: { kind: "LXC", name: "syncthing", where: "pve/130", state: "running" },
    check: { tone: "up", label: "up · 8 ms", bars: up() },
    net: { address: "198.18.1.50", path: "No Internet", adsAllowed: false, device: "syncthing" },
    warnings: [{ kind: "no-tls", detail: "Proxy Host #11 has no certificate: served over plain HTTP.", ignored: true }],
  },
  {
    id: "c41d09aa",
    name: "Wiki",
    icon: "",
    group: "",
    hidden: false,
    url: "https://wiki.home.example",
    aliases: [],
    reach: "proxied",
    dns: [alias("wiki.home.example")],
    check: { tone: "down", label: "down · HTTP 404", bars: bars("dddddddddddddddddddddddddddddd") },
    warnings: [{ kind: "no-proxy-host", detail: "wiki.home.example points at the Reverse Proxy, but no Proxy Host serves it." }],
  },
  {
    id: "proxy:cloud.home.example",
    name: "Nextcloud",
    icon: "",
    group: "Office",
    hidden: false,
    url: "https://cloud.home.example",
    aliases: [],
    reach: "proxied",
    proxy: ph(13, "http", "198.18.1.60", 80, { certId: 2 }),
    dns: [alias("cloud.home.example"), override("cloud.home.example", "198.18.1.60")],
    workload: { kind: "LXC", name: "nextcloud", where: "pve/140", state: "running" },
    check: { tone: "warn", label: "up · certificate expires in 9 days", bars: up() },
    net: { address: "198.18.1.60", path: "Tunnelled", adsAllowed: false, device: "nextcloud" },
    warnings: [{ kind: "registered-twice", detail: "cloud.home.example is an alias of proxy.home.example and its own Host Override → 198.18.1.60." }],
  },
  {
    id: "proxy:photos.home.example",
    name: "Immich",
    icon: "",
    group: "Media",
    hidden: false,
    url: "https://photos.home.example",
    aliases: [],
    reach: "not-proxied",
    proxy: ph(15, "http", "198.18.1.31", 2283, { websockets: true }),
    overrideIp: "198.18.1.31",
    dns: [override("photos.home.example", "198.18.1.31")],
    workload: { kind: "Docker", name: "immich-server", where: "photos", state: "running" },
    check: { tone: "up", label: "up · 22 ms", bars: up() },
    net: { address: "198.18.1.31", path: "Tunnelled", adsAllowed: false, device: "photos", via: "Docker host photos" },
    warnings: [{ kind: "dns-bypass", detail: "Proxy Host #15 serves photos.home.example, but its Host Override resolves to 198.18.1.31, not the Reverse Proxy." }],
  },
  {
    id: "e90b2f11",
    name: "Outline",
    icon: "",
    group: "Office",
    hidden: false,
    url: "https://outline.home.example",
    aliases: ["docs.home.example"],
    reach: "proxied",
    proxy: ph(17, "http", "198.18.1.61", 3000),
    dns: [alias("outline.home.example"), alias("docs.home.example")],
    workload: { kind: "LXC", name: "outline", where: "pve/141", state: "running" },
    check: { tone: "up", label: "up · 40 ms", bars: up() },
    net: { address: "198.18.1.61", path: "Tunnelled", adsAllowed: false, device: "outline" },
    warnings: [{ kind: "hostname-two-apps", detail: "docs.home.example is also the URL of Docs.", other: "manual:5be1a2c3" }],
  },
  {
    id: "manual:5be1a2c3",
    name: "Docs",
    icon: "outline",
    group: "Office",
    hidden: false,
    url: "https://docs.home.example",
    aliases: [],
    reach: "link-only",
    dns: [alias("docs.home.example")],
    check: { tone: "up", label: "up · 41 ms", bars: up() },
    warnings: [{ kind: "hostname-two-apps", detail: "docs.home.example is also an alias of Outline.", other: "e90b2f11" }],
  },
  {
    id: "b2c9e7f0",
    name: "pfSense",
    icon: "pfsense",
    group: "Network",
    hidden: false,
    url: "https://fw.home.example",
    aliases: [],
    reach: "not-proxied",
    overrideIp: "198.18.0.1",
    dns: [override("fw.home.example", "198.18.0.1")],
    check: { tone: "up", label: "up · 2 ms", bars: up() },
    warnings: [],
  },
  {
    id: "d3a8b6c4",
    name: "Proxmox",
    icon: "proxmox",
    group: "Infrastructure",
    hidden: false,
    url: "https://pve.home.example:8006",
    aliases: ["pve1.home.example"],
    reach: "not-proxied",
    overrideIp: "198.18.1.2",
    dns: [override("pve.home.example", "198.18.1.2"), { host: "pve1.home.example", how: "alias", ip: "198.18.1.2", parent: "pve.home.example" }],
    check: { tone: "up", label: "up · 3 ms", bars: up() },
    net: { address: "198.18.1.2", path: "Direct", adsAllowed: false, device: "pve" },
    warnings: [],
  },
  {
    id: "f0e1d2c3",
    name: "NAS",
    icon: "synology",
    group: "Infrastructure",
    hidden: false,
    url: "https://nas.home.example:5001",
    aliases: [],
    reach: "not-proxied",
    overrideIp: "198.18.1.10",
    dns: [override("nas.home.example", "198.18.1.10")],
    check: { tone: "up", label: "up · 4 ms", bars: up() },
    net: { address: "198.18.1.10", path: "Direct", adsAllowed: false, device: "nas" },
    warnings: [],
  },
  {
    id: "a1b2c3d4",
    name: "Printer",
    icon: "",
    group: "",
    hidden: true,
    url: "https://printer.home.example",
    aliases: [],
    reach: "not-proxied",
    overrideIp: "198.18.2.40",
    dns: [override("printer.home.example", "198.18.2.40")],
    check: { tone: "up", label: "up · 6 ms", bars: up() },
    net: { address: "198.18.2.40", path: "No Internet", adsAllowed: false, device: "printer" },
    warnings: [{ kind: "no-tls", detail: "The printer answers on https with a self-signed certificate." }],
  },
  {
    id: "a1b2c3d5",
    name: "Switch",
    icon: "",
    group: "",
    hidden: true,
    url: "https://switch.home.example",
    aliases: [],
    reach: "not-proxied",
    overrideIp: "198.18.0.2",
    dns: [override("switch.home.example", "198.18.0.2")],
    warnings: [],
  },
  {
    id: "manual:00aa11bb22cc33dd",
    name: "Fritz!Box",
    icon: "fritzbox",
    group: "Network",
    hidden: false,
    url: "http://198.18.255.1",
    aliases: [],
    reach: "link-only",
    dns: [],
    check: { tone: "up", label: "up · 5 ms", bars: up() },
    warnings: [],
  },
  {
    id: "manual:77ee88ff",
    name: "Status page",
    icon: "uptime-kuma",
    group: "",
    hidden: false,
    url: "https://status.example.org",
    aliases: [],
    reach: "link-only",
    dns: [],
    warnings: [],
  },
];

export const workloads: Workload[] = [
  { kind: "LXC", name: "grafana", where: "pve/120", state: "running" },
  { kind: "VM", name: "home-assistant", where: "pve/110", state: "running" },
  { kind: "LXC", name: "syncthing", where: "pve/130", state: "running" },
  { kind: "LXC", name: "nextcloud", where: "pve/140", state: "running" },
  { kind: "LXC", name: "outline", where: "pve/141", state: "running" },
  { kind: "LXC", name: "wiki", where: "pve/150", state: "stopped" },
  { kind: "Docker", name: "jellyfin", where: "media", state: "exited" },
  { kind: "Docker", name: "paperless", where: "media", state: "running" },
  { kind: "Docker", name: "immich-server", where: "photos", state: "running" },
];

export interface State {
  apps: PApp[];
  orphans: Orphan[];
  /** What ddash would have done, newest first. */
  log: string[];
}

const initial: State = {
  apps: seed,
  orphans: [{ id: "proxy:books.home.example", name: "Bookstack", icon: "bookstack", group: "Office", was: "https://books.home.example" }],
  log: [],
};

let state = initial;
const listeners = new Set<() => void>();

export const store = {
  get: () => state,
  subscribe: (fn: () => void) => {
    listeners.add(fn);
    return () => listeners.delete(fn);
  },
  set(fn: (s: State) => State) {
    state = fn(state);
    for (const l of listeners) l();
  },
  log(line: string) {
    store.set((s) => ({ ...s, log: [line, ...s.log].slice(0, 6) }));
  },
  update(id: string, fn: (a: PApp) => PApp) {
    store.set((s) => ({ ...s, apps: s.apps.map((a) => (a.id === id ? fn(a) : a)) }));
  },
  remove(id: string) {
    store.set((s) => ({ ...s, apps: s.apps.filter((a) => a.id !== id) }));
  },
  add(a: PApp) {
    store.set((s) => ({ ...s, apps: [...s.apps, a] }));
  },
  reset() {
    state = initial;
    for (const l of listeners) l();
  },
};

export const useStore = () => useSyncExternalStore(store.subscribe, store.get);

export const hostOf = (url: string) => URL.parse(url)?.hostname ?? url;
export const certById = (id: number) => certs.find((c) => c.id === id);

/** Whether a certificate's domains cover a hostname (one wildcard level). */
export const covers = (c: Cert, host: string) =>
  c.domains.some((d) => d === host || (d.startsWith("*.") && host.endsWith(d.slice(1)) && !host.slice(0, -d.length + 1).includes(".")));

/** The certificate Add App picks by default: the first covering the hostname, else none. */
export const coveringCert = (host: string) => certs.find((c) => covers(c, host))?.id ?? 0;

/** Every hostname pfSense or Nginx Proxy Manager already holds, for "is the name free". */
export function takenBy(s: State, host: string, except?: string) {
  const h = host.toLowerCase();
  for (const a of s.apps) {
    if (a.id === except) continue;
    if (a.dns.some((d) => d.host === h) || hostOf(a.url) === h || a.aliases.includes(h)) return a;
  }
  if (h === reverseProxy.host) return { name: "the Reverse Proxy" } as PApp;
  return undefined;
}

/** The Warnings that count toward the Overview and the Nav badge: not Ignored, not on a Hidden App. */
export const counting = (s: State) => s.apps.filter((a) => !a.hidden).flatMap((a) => a.warnings.filter((w) => !w.ignored)).length + s.orphans.length;

export const newId = () => Math.random().toString(16).slice(2, 10);
