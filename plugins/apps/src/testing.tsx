import { renderScoped as render, type Answers, type Call } from "@ddash/plugin/testing";

/** ListApps' answer (Connect JSON). */
export const list = {
  apps: [
    {
      id: "proxy:grafana.home.example",
      serverName: "grafana.home.example",
      name: "Grafana",
      url: "https://grafana.home.example",
      group: "Monitoring",
      overlay: { serverName: "grafana.home.example", group: "Monitoring" },
      workload: { ref: { kind: "KIND_LXC", host: "pve", id: "120" }, name: "grafana", state: "running" },
    },
    {
      id: "proxy:jellyfin.home.example",
      serverName: "jellyfin.home.example",
      name: "Jellyfin",
      url: "https://jellyfin.home.example",
      aliases: ["media.home.example", "tv.home.example"],
      workload: { ref: { kind: "KIND_DOCKER", host: "media", id: "jellyfin" }, name: "jellyfin", state: "exited" },
    },
    { id: "proxy:old.home.example", serverName: "old.home.example", name: "Old", url: "https://old.home.example", hidden: true, overlay: { serverName: "old.home.example", hidden: true } },
    { id: "manual:00aa11bb22cc33dd", name: "pfSense", url: "https://198.18.0.1", icon: "pfsense", group: "Network", manual: true },
    { id: "proxy:printer.home.example", serverName: "printer.home.example", name: "Printer", url: "http://printer.home.example:8080" },
  ],
  unmatched: [{ serverName: "paperless.home.example", name: "Documents", group: "Office" }],
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

const day = 86_400_000;

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
    appCheck(4, "manual:00aa11bb22cc33dd", "STATE_PENDING", { reason: "timeout", failures: 1 }),
    appCheck(5, "proxy:old.home.example", "STATE_UP", { disabled: true }),
    { id: "6", spec: { name: "nas", type: "CHECK_TYPE_TCP", target: "198.18.1.10:22" }, state: "STATE_DOWN" },
  ],
};

/** ListChecks' answer with every Check up and no certificate warning. */
export const allUp = {
  checks: checks.checks.map(({ id, spec, ...c }) => ({ id, spec, appId: "appId" in c ? c.appId : undefined, state: "STATE_UP" })),
};

/** The apps Plugin's scope in tests. */
export const apps = { name: "apps", requires: ["apps", "checks"] };

/**
 * Renders children in the apps Plugin's scope against apps answering by
 * method name (ListApps defaults to list, ListChecks to checks,
 * ListWorkloads to workloads; Actions answer {}), with the installed
 * Plugins named, at path (e.g. "/?q=media"), and returns the calls made.
 */
export const renderScoped = (children: React.ReactNode, answers: Answers = {}, installed: string[] = ["apps"], path?: string) =>
  render(apps, children, {
    answers: {
      ListApps: () => Response.json(list),
      ListChecks: () => Response.json(checks),
      ListWorkloads: () => Response.json(workloads),
      ...answers,
    },
    installed,
    path,
  });

const getters = new Set(["ListApps", "ListChecks", "ListWorkloads"]);

/** The calls to Actions, i.e. anything but the getters, each as its method and body. */
export const actions = (calls: Call[]) => calls.filter((c) => !getters.has(c.method)).map(({ method, body }) => ({ method, body }));
