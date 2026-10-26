import { create, toBinary, type DescMessage, type MessageInitShape } from "@bufbuild/protobuf";
import { base64Encode } from "@bufbuild/protobuf/wire";
import { renderScoped as render, type Answers, type Call } from "@ddash/plugin/testing";

/** A Connect error answer with a detail, e.g. RunAction's stopped run or its refusal with the new Plan. */
export function failing<D extends DescMessage>(code: string, message: string, schema: D, detail: MessageInitShape<D>, status = 400) {
  const value = base64Encode(toBinary(schema, create(schema, detail)), "std_raw");
  return () => Response.json({ code, message, details: [{ type: schema.typeName, value }] }, { status });
}

const day = 86_400_000;

/** The Reverse Proxy's Host Override, as an alias's parent. */
const nginx = { host: "nginx", domain: "home.example" };

/** The wildcard certificate (id 3) as ListApps carries it on a Proxy Host: Not renewing. */
const wildcard = {
  name: "*.home.example",
  state: "CERTIFICATE_STATE_NOT_RENEWING",
  message: "Nginx Proxy Manager hasn't renewed this: check its Let's Encrypt log.",
};

/**
 * ListApps' answer (Connect JSON). No Warning counts: Old's is on a
 * Hidden App, Printer's No TLS and the orphan's are Ignored.
 */
export const list = {
  apps: [
    {
      id: "proxy:grafana.home.example",
      name: "Grafana",
      url: "https://grafana.home.example",
      group: "Monitoring",
      reach: "REACH_PROXIED",
      touched: true,
      settings: { group: "Monitoring" },
      links: { proxyHostId: "3" },
      proxyHost: {
        id: "3",
        domainNames: ["grafana.home.example"],
        forwardScheme: "http",
        forwardHost: "198.18.1.204",
        forwardPort: 3000,
        certificateId: "3",
        certificate: wildcard,
        blockExploits: true,
        forceSsl: true,
        http2: true,
        enabled: true,
      },
      workload: { ref: { kind: "KIND_LXC", host: "pve", id: "120" }, name: "grafana", state: "running" },
      dns: [{ host: "grafana", domain: "home.example", parent: nginx, ips: ["198.18.1.210"] }],
      network: { address: "198.18.1.204", internetPath: "INTERNET_PATH_TUNNELLED" },
    },
    {
      id: "proxy:jellyfin.home.example",
      name: "Jellyfin",
      url: "https://jellyfin.home.example",
      aliases: ["media.home.example", "tv.home.example"],
      reach: "REACH_PROXIED",
      settings: {},
      links: { proxyHostId: "7" },
      proxyHost: {
        id: "7",
        domainNames: ["jellyfin.home.example", "media.home.example", "tv.home.example"],
        forwardScheme: "http",
        forwardHost: "198.18.2.10",
        forwardPort: 8096,
        certificateId: "3",
        certificate: wildcard,
        websockets: true,
        enabled: true,
      },
      workload: { ref: { kind: "KIND_DOCKER", host: "media", id: "jellyfin" }, name: "jellyfin", state: "exited" },
      dns: [
        { host: "jellyfin", domain: "home.example", parent: nginx, ips: ["198.18.1.210"] },
        { host: "tv", domain: "home.example", parent: nginx, ips: ["198.18.1.210"] },
      ],
      network: { address: "198.18.2.10", internetPath: "INTERNET_PATH_DIRECT", adsAllowed: true },
    },
    {
      id: "proxy:old.home.example",
      name: "Old",
      url: "https://old.home.example",
      hidden: true,
      reach: "REACH_PROXIED",
      touched: true,
      settings: { hidden: true },
      links: { proxyHostId: "9" },
      warnings: [{ kind: "WARNING_KIND_NO_WORKLOAD", fixes: [{ kind: "FIX_KIND_SET_WORKLOAD_OVERRIDE" }] }],
    },
    {
      id: "app:00aa11bb22cc33dd",
      name: "pfSense",
      url: "https://198.18.0.1",
      icon: "pfsense",
      group: "Network",
      reach: "REACH_LINK_ONLY",
      touched: true,
      settings: { name: "pfSense", icon: "pfsense", group: "Network" },
      links: {},
    },
    {
      id: "proxy:printer.home.example",
      name: "Printer",
      url: "http://printer.home.example:8080",
      reach: "REACH_PROXIED",
      settings: {},
      links: { proxyHostId: "12" },
      proxyHost: { id: "12", domainNames: ["printer.home.example"], forwardScheme: "http", forwardHost: "198.18.1.10", forwardPort: 80 },
      warnings: [{ kind: "WARNING_KIND_NO_TLS", ignored: true, fixes: [{ kind: "FIX_KIND_PICK_CERTIFICATE" }] }],
    },
    {
      id: "proxy:paperless.home.example",
      name: "Documents",
      url: "https://paperless.home.example",
      group: "Office",
      orphaned: true,
      touched: true,
      settings: { name: "Documents", group: "Office", workload: { kind: "KIND_LXC", host: "pve", id: "130" } },
      links: {},
      warnings: [{ kind: "WARNING_KIND_ORPHANED", ignored: true, fixes: [{ kind: "FIX_KIND_REATTACH" }, { kind: "FIX_KIND_DELETE_ORPHAN" }] }],
    },
  ],
  proxyHosts: { readAt: new Date(Date.now() - 5 * 60_000).toISOString() },
  hostOverrides: { readAt: new Date(Date.now() - 6 * 60_000).toISOString() },
  reverseProxy: { address: "198.18.1.210", hostOverrides: ["nginx.home.example"] },
};

/** list with Warnings that count: Jellyfin's No Workload and Offline Proxy Host, Printer's No Workload. */
export const warned = {
  ...list,
  apps: list.apps.map((a) =>
    a.name === "Jellyfin"
      ? { ...a, warnings: [{ kind: "WARNING_KIND_NO_WORKLOAD" }, { kind: "WARNING_KIND_PROXY_HOST_OFFLINE", detail: "nginx: [emerg] bad" }] }
      : a.name === "Printer"
        ? { ...a, warnings: [{ kind: "WARNING_KIND_NO_WORKLOAD" }, ...(a.warnings ?? [])] }
        : a,
  ),
};

/** NAS: a Not-proxied App on an external host, sharing media.home.example with Jellyfin. */
const nas = {
  id: "app:5a5a5a5a5a5a5a5a",
  name: "NAS",
  url: "https://nas.home.example",
  aliases: ["media.home.example"],
  reach: "REACH_NOT_PROXIED",
  touched: true,
  settings: { workload: { kind: "KIND_EXTERNAL", host: "198.18.1.20" } },
  links: { hostOverride: { host: "nas", domain: "home.example" } },
  workload: { ref: { kind: "KIND_EXTERNAL", host: "198.18.1.20" }, name: "198.18.1.20", override: true },
  dns: [
    { host: "nas", domain: "home.example", ips: ["198.18.1.20"] },
    { host: "media", domain: "home.example", parent: { host: "nas", domain: "home.example" }, ips: ["198.18.1.20"] },
  ],
  network: { address: "198.18.1.20", internetPath: "INTERNET_PATH_NO_INTERNET", device: { mac: "02:1c:d5:5e:3d:e5", name: "nas" } },
  warnings: [
    {
      kind: "WARNING_KIND_HOSTNAME_ON_TWO_APPS",
      hostnames: ["media.home.example"],
      fixes: [{ kind: "FIX_KIND_MERGE", appId: "proxy:jellyfin.home.example" }],
    },
  ],
};

/** Proxmox: a Hidden Not-proxied App. */
const proxmox = {
  id: "app:7e7e7e7e7e7e7e7e",
  name: "Proxmox",
  url: "https://pve.home.example",
  group: "Infra",
  hidden: true,
  reach: "REACH_NOT_PROXIED",
  touched: true,
  settings: { hidden: true, group: "Infra" },
  links: { hostOverride: { host: "pve", domain: "home.example" } },
  dns: [{ host: "pve", domain: "home.example", ips: ["198.18.1.2"] }],
  warnings: [],
};

/**
 * The split view's ListApps: list plus NAS and the Hidden Proxmox, with
 * Warnings that count on Jellyfin (Proxy Host offline, Hostname on two
 * Apps), NAS (Hostname on two Apps) and the orphan Documents; Printer's
 * No TLS is the one Ignored Warning on a visible App.
 */
export const split = {
  ...list,
  apps: [
    ...list.apps.map((a) => {
      if (a.name === "Jellyfin") {
        return {
          ...a,
          warnings: [
            {
              kind: "WARNING_KIND_PROXY_HOST_OFFLINE",
              detail: "nginx: [emerg] host not found in upstream",
              fixes: [{ kind: "FIX_KIND_SAVE_AGAIN", proxyHostId: "7" }, { kind: "FIX_KIND_DELETE_APP" }],
            },
            { kind: "WARNING_KIND_HOSTNAME_ON_TWO_APPS", hostnames: ["media.home.example"], fixes: [{ kind: "FIX_KIND_MERGE", appId: nas.id }] },
          ],
        };
      }
      if (a.name === "Documents") return { ...a, warnings: a.warnings?.map((w) => ({ ...w, ignored: false })) };
      return a;
    }),
    nas,
    proxmox,
  ].sort((a, b) => a.name.localeCompare(b.name)),
};

/** ListWorkloads' answer: what an override can pick. */
export const workloads = {
  workloads: [
    { ref: { kind: "KIND_DOCKER", host: "media", id: "jellyfin" }, name: "jellyfin", state: "exited" },
    { ref: { kind: "KIND_LXC", host: "pve", id: "102" }, name: "media-stack", state: "running" },
    { ref: { kind: "KIND_LXC", host: "pve", id: "120" }, name: "grafana", state: "running" },
    { ref: { kind: "KIND_VM", host: "pve", id: "110" }, name: "home-assistant", state: "stopped" },
  ],
};

/** An App's Check as checks answers ListChecks (Connect JSON). */
export function appCheck(id: number, appId: string, state: string, extra: Record<string, unknown> = {}) {
  return { id: String(id), appId, spec: { name: appId, type: "CHECK_TYPE_HTTP", target: "https://app.home.example" }, state, ...extra };
}

/**
 * ListChecks' answer: Grafana up, Jellyfin down, Printer up with its
 * certificate expiring in 5 days, pfSense pending, Old disabled, and a
 * manual Check.
 */
export const checks = {
  checks: [
    appCheck(1, "proxy:grafana.home.example", "STATE_UP", { latencyMs: 12 }),
    appCheck(2, "proxy:jellyfin.home.example", "STATE_DOWN", { reason: "HTTP 502", failures: 3 }),
    appCheck(3, "proxy:printer.home.example", "STATE_UP", {
      certExpires: new Date(Date.now() + 5 * day + 3_600_000).toISOString(),
      certExpiring: true,
    }),
    appCheck(4, "app:00aa11bb22cc33dd", "STATE_PENDING", { reason: "timeout", failures: 1 }),
    appCheck(5, "proxy:old.home.example", "STATE_UP", { disabled: true }),
    { id: "6", spec: { name: "nas", type: "CHECK_TYPE_TCP", target: "198.18.1.10:22" }, state: "STATE_DOWN" },
  ],
};

/** ListChecks' answer with every Check up and no certificate warning. */
export const allUp = {
  checks: checks.checks.map(({ id, spec, ...c }) => ({ id, spec, appId: "appId" in c ? c.appId : undefined, state: "STATE_UP" })),
};

/** The start of today's day bucket, so 30 daily buckets end now. */
const today = Math.floor(Date.now() / day) * day;

/** GetHistory's answer over 30 days: up but for one day down. */
export const history = {
  uptime: [{ ratio: 0.99 }, { ratio: 0.97 }, { ratio: 0.99 }],
  window: "WINDOW_30D",
  bucket: "86400s",
  from: new Date(today - 29 * day).toISOString(),
  buckets: Array.from({ length: 30 }, (_, i) => ({
    start: new Date(today - (29 - i) * day).toISOString(),
    up: i === 20 ? "0" : "1440",
    down: i === 20 ? "1440" : "0",
  })),
};

/**
 * ListCertificates' answer, for the certificate pickers: the wildcard (id
 * 3), Let's Encrypt, OK. The Panels show the state ListApps carries
 * instead (wildcard, Not renewing).
 */
export const certificates = {
  certificates: [
    {
      id: "3",
      provider: "letsencrypt",
      niceName: "*.home.example",
      domainNames: ["*.home.example"],
      expiresOn: new Date(Date.now() + 60 * day).toISOString(),
      state: "CERTIFICATE_STATE_OK",
    },
  ],
};

/** The apps Plugin's scope in tests. */
export const apps = { name: "apps", requires: ["apps", "checks", "nginx-proxy-manager-api"] };

/**
 * Renders children in the apps Plugin's scope against apps answering by
 * method name (ListApps defaults to list, ListChecks to checks,
 * ListWorkloads to workloads, GetHistory to history, ListCertificates to
 * certificates; Actions answer {}), with the installed Plugins named, at
 * path (e.g. "/?q=media"), and returns the calls made.
 */
export const renderScoped = (children: React.ReactNode, answers: Answers = {}, installed: string[] = ["apps"], path?: string) =>
  render(apps, children, {
    answers: {
      ListApps: () => Response.json(list),
      ListChecks: () => Response.json(checks),
      ListWorkloads: () => Response.json(workloads),
      GetHistory: () => Response.json(history),
      ListCertificates: () => Response.json(certificates),
      ...answers,
    },
    installed,
    path,
  });

const getters = new Set(["ListApps", "ListChecks", "ListWorkloads", "GetHistory", "ListCertificates", "PlanAction"]);

/** The calls to Actions, i.e. anything but the getters, each as its method and body. */
export const actions = (calls: Call[]) => calls.filter((c) => !getters.has(c.method)).map(({ method, body }) => ({ method, body }));
