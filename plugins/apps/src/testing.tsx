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

/** A request a test's fake fetch received: the method and its JSON body. */
export interface Call {
  method: string;
  body: unknown;
}

export const settle = () => act(() => new Promise((r) => setTimeout(r, 50)));

/**
 * Renders children in the apps Plugin's scope against apps answering by
 * method name (ListApps defaults to list; Actions answer {}), and returns
 * the calls made.
 */
export function renderScoped(children: React.ReactNode, answers: Record<string, () => Response> = {}) {
  const calls: Call[] = [];
  const fetch = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const method = String(input).split("/").pop()!;
    const raw = init?.body;
    const body = raw ? JSON.parse(typeof raw === "string" ? raw : new TextDecoder().decode(raw as Uint8Array)) : undefined;
    calls.push({ method, body });
    const answer = answers[method] ?? (method === "ListApps" ? () => Response.json(list) : () => Response.json({}));
    return answer();
  });
  render(
    <PluginHost client={createQueryClient()}>
      <PluginScope plugin={{ name: "apps", requires: ["apps"] }} fetch={fetch}>
        {children}
      </PluginScope>
    </PluginHost>,
  );
  return calls;
}

/** The calls to Actions, i.e. anything but ListApps. */
export const actions = (calls: Call[]) => calls.filter((c) => c.method !== "ListApps");
