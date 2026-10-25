// @ddash/plugin/testing: what a Plugin's tests render it with. A fake
// core answering by method name, a settle that waits for its answers
// however long a loaded machine takes, and the answers and queries every
// Plugin's tests need. Only tests import it; no bundle does.
import { act, render, within } from "@testing-library/react";
import type { ReactNode } from "react";
import { MemoryRouter, Route, Routes, useRoutes } from "react-router";
import { afterEach, vi } from "vitest";
import type { PluginDefinition } from "../define";
import { InstalledPlugins } from "../link";
import { createQueryClient } from "../query";
import { PluginHost, PluginScope } from "../scope";
import type { Fetch } from "../transport";

/** A call the fake core received: the method's name, the URL's path and the decoded JSON body. */
export interface Call {
  method: string;
  url: string;
  body: unknown;
}

/** How the fake core answers one method, given the call's decoded body. */
export type Answer = (body: unknown) => Response | Promise<Response>;

/** The fake core's answers, by method name. */
export type Answers = Record<string, Answer>;

/** The calls in flight on the fake core rendered last, and how many of them never answer. */
let inFlight = { all: 0, stuck: 0 };

/** An answer that never comes: the call stays in flight, e.g. to see a skeleton. settle doesn't wait for it. */
export function pending(): Promise<Response> {
  inFlight.stuck++;
  return new Promise<Response>(() => {});
}

/** The core's answer while a Component is down: Connect's unavailable, as a 503. */
export const unavailable = (message: string) => Response.json({ code: "unavailable", message }, { status: 503 });

/**
 * Wraps a fake fetch so settle waits for its answers. The shell's tests
 * wrap their own fakes with it; renderScoped wraps its own.
 */
export function tracked(fetch: Fetch): Fetch {
  const counts = { all: 0, stuck: 0 };
  inFlight = counts;
  return async (input, init) => {
    counts.all++;
    try {
      return await fetch(input, init);
    } finally {
      counts.all--;
    }
  };
}

// The quiet turns of the event loop after the last answer before the page
// counts as settled: enough for the transport to read it and React Query
// and React to render it, however slowly each turn runs.
const quietTurns = 20;

/**
 * Waits until the fake core has answered every call (but pending ones) and
 * what the answers render has rendered, including calls the answers set
 * off. It counts turns of the event loop, not milliseconds, so a loaded
 * machine only makes it slower.
 */
export async function settle() {
  for (let quiet = 0, turns = 0; quiet < quietTurns && turns < 5000; turns++) {
    await act(() => new Promise((r) => setTimeout(r, 0)));
    quiet = inFlight.all > inFlight.stuck ? 0 : quiet + 1;
  }
}

const decode = (body: BodyInit | null | undefined): unknown => {
  if (!body) return undefined;
  return JSON.parse(typeof body === "string" ? body : new TextDecoder().decode(body as Uint8Array));
};

/** The Plugin scope a test renders in: its name and the Components it requires. */
export interface Scope {
  name: string;
  requires: string[];
}

export interface ScopedOptions {
  /** How the Components answer, by method name. */
  answers?: Answers;
  /** What a method without an answer gets; {} by default. */
  unanswered?: (method: string) => Response;
  /** The installed Plugins, for usePluginLink; none by default. */
  installed?: string[];
  /** Where the router starts; "/" by default. */
  path?: string;
}

/**
 * Renders children in a Plugin's scope, under a router, against a fake core
 * answering by method name, and returns the calls it receives. Icons the
 * page draws are answered apart and aren't calls. Then await settle().
 */
export function renderScoped(scope: Scope, children: ReactNode, { answers = {}, unanswered = () => Response.json({}), installed, path = "/" }: ScopedOptions = {}): Call[] {
  const calls: Call[] = [];
  const fetch = tracked(async (input, init) => {
    const url = new URL(String(input), "http://ddash.test").pathname;
    const method = url.split("/").pop()!;
    const body = decode(init?.body);
    calls.push({ method, url, body });
    return (answers[method] ?? (() => unanswered(method)))(body);
  });
  // Icon draws through the page's own fetch.
  vi.stubGlobal("fetch", async () => Response.json({}));
  const scoped = (
    <PluginScope plugin={scope} fetch={fetch}>
      <MemoryRouter initialEntries={[path]}>{children}</MemoryRouter>
    </PluginScope>
  );
  render(<PluginHost client={createQueryClient()}>{installed ? <InstalledPlugins names={installed}>{scoped}</InstalledPlugins> : scoped}</PluginHost>);
  return calls;
}

afterEach(() => vi.unstubAllGlobals());

/**
 * Renders a Plugin's Pages as the shell mounts them, under /<name>/, at
 * path (e.g. "/proxmox/workloads/101"), settles, and returns the calls.
 */
export async function showPlugin(plugin: PluginDefinition, scope: Scope, path: string, options: Omit<ScopedOptions, "path"> = {}) {
  function Pages() {
    return useRoutes(plugin.pages ?? []);
  }
  const calls = renderScoped(
    scope,
    <Routes>
      <Route path={`/${scope.name}/*`} element={<Pages />} />
    </Routes>,
    { ...options, path },
  );
  await settle();
  return calls;
}

/** A MetricTile's value, by its label: the text after the label, or undefined while it loads. */
export function metricValue(label: string, container: HTMLElement = document.body): string | undefined {
  const tile = within(container).getByRole("group", { name: label });
  if (within(tile).queryByRole("status", { name: "Loading" })) return undefined;
  return tile.textContent?.slice(label.length);
}
