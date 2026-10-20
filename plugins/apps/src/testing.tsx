import { act, render } from "@testing-library/react";
import { createQueryClient, PluginHost, PluginScope } from "@ddash/plugin";
import { vi } from "vitest";

/** ListApps' answer (Connect JSON). */
export const list = {
  apps: [
    { id: "proxy:grafana.home.example", serverName: "grafana.home.example", name: "Grafana", url: "https://grafana.home.example", group: "Monitoring", overlay: { serverName: "grafana.home.example", group: "Monitoring" } },
    { id: "proxy:jellyfin.home.example", serverName: "jellyfin.home.example", name: "Jellyfin", url: "https://jellyfin.home.example", aliases: ["media.home.example", "tv.home.example"] },
    { id: "proxy:old.home.example", serverName: "old.home.example", name: "Old", url: "https://old.home.example", hidden: true, overlay: { serverName: "old.home.example", hidden: true } },
    { id: "manual:00aa11bb22cc33dd", name: "pfSense", url: "https://198.18.0.1", icon: "pfsense", group: "Network", manual: true },
    { id: "proxy:printer.home.example", serverName: "printer.home.example", name: "Printer", url: "http://printer.home.example:8080" },
  ],
  unmatched: [{ serverName: "paperless.home.example", name: "Documents", group: "Office" }],
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

/** A request a test's fake fetch received: the method and its JSON body. */
export interface Call {
  method: string;
  body: unknown;
}

export const settle = () => act(() => new Promise((r) => setTimeout(r, 50)));

/**
 * Renders children in the apps Plugin's scope against apps answering by
 * method name (ListApps defaults to list, ListChecks to checks; Actions
 * answer {}), and returns the calls made.
 */
export function renderScoped(children: React.ReactNode, answers: Record<string, () => Response> = {}) {
  const calls: Call[] = [];
  const fetch = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const method = String(input).split("/").pop()!;
    const raw = init?.body;
    const body = raw ? JSON.parse(typeof raw === "string" ? raw : new TextDecoder().decode(raw as Uint8Array)) : undefined;
    calls.push({ method, body });
    const getters: Record<string, () => Response> = { ListApps: () => Response.json(list), ListChecks: () => Response.json(checks) };
    const answer = answers[method] ?? getters[method] ?? (() => Response.json({}));
    return answer();
  });
  render(
    <PluginHost client={createQueryClient()}>
      <PluginScope plugin={{ name: "apps", requires: ["apps", "checks"] }} fetch={fetch}>
        {children}
      </PluginScope>
    </PluginHost>,
  );
  return calls;
}

/** The calls to Actions, i.e. anything but the getters. */
export const actions = (calls: Call[]) => calls.filter((c) => c.method !== "ListApps" && c.method !== "ListChecks");
