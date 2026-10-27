// @ddash/plugin/testing: what a Plugin's tests render it with. A fake
// core answering by method name, a settle that waits for its answers
// however long a loaded machine takes, and the answers and queries every
// Plugin's tests need. Only tests import it; no bundle does.
import { act, render, within } from "@testing-library/react";
import type { ReactNode } from "react";
import { MemoryRouter, Route, Routes, useRoutes } from "react-router";
import { afterEach, vi } from "vitest";
import type { PluginDefinition, Status, Widget, WidgetSize } from "../define";
import { InstalledPlugins } from "../link";
import { createQueryClient } from "../query";
import { PluginHost, PluginScope } from "../scope";
import type { Fetch } from "../transport";

/**
 * A call the fake core received: the method's name, the URL's path and the
 * decoded JSON body (a streamed call's too, taken out of its envelope).
 */
export interface Call {
  method: string;
  url: string;
  body: unknown;
}

/**
 * How the fake core answers one method, given the call's decoded body and
 * what it was fetched with (e.g. its signal, to keep a stream open on),
 * which renderScoped always passes.
 */
export type Answer = (body: unknown, init?: RequestInit) => Response | Promise<Response>;

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

/** An error's detail as Connect JSON carries it: the message's full name and its binary encoding in unpadded base64. */
export interface Detail {
  type: string;
  value: string;
}

/** A streamed call's end-of-stream error, as the host writes it: e.g. failed_precondition, with details. */
export interface StreamError {
  code: string;
  message: string;
  details?: Detail[];
}

export interface StreamOptions {
  /** The error the stream ends with after its messages; a clean end by default. */
  error?: StreamError;
  /**
   * Keeps the stream open after its messages, as a running Action's stays,
   * until this signal aborts (the call's own: init.signal), when its body
   * fails as an aborted fetch's does.
   */
  openUntil?: AbortSignal | null;
}

/** One Connect envelope: a flags byte, the length as 4 bytes big-endian, then the JSON. */
function envelope(flags: number, json: unknown): Uint8Array {
  const data = new TextEncoder().encode(JSON.stringify(json));
  const out = new Uint8Array(5 + data.length);
  out[0] = flags;
  new DataView(out.buffer).setUint32(1, data.length);
  out.set(data, 5);
  return out;
}

/**
 * The host's answer to a streamed call, as Connect's application/connect+json:
 * a 200 carrying each message in its own envelope, then the end of the stream
 * ({} or the error). Each envelope comes a turn of the event loop after the
 * last, as the host flushes them, so each message renders on its own.
 */
export function streamed(messages: unknown[], { error, openUntil }: StreamOptions = {}): Response {
  const envelopes = messages.map((m) => envelope(0, m));
  if (!openUntil) envelopes.push(envelope(2, error ? { error } : {}));
  const body = new ReadableStream<Uint8Array>({
    start(stream) {
      const abort = () => stream.error(new DOMException("The operation was aborted.", "AbortError"));
      if (openUntil?.aborted) abort();
      else openUntil?.addEventListener("abort", abort, { once: true });
    },
    async pull(stream) {
      await new Promise((r) => setTimeout(r, 0));
      const next = envelopes.shift();
      if (next) stream.enqueue(next);
      else if (openUntil) await new Promise(() => {});
      else stream.close();
    },
  });
  return new Response(body, { headers: { "Content-Type": "application/connect+json" } });
}

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
  for (let quiet = 0, turns = 0; quiet < quietTurns; turns++) {
    if (turns === 2000) throw new Error("settle: a call is still unanswered; answer one that never comes with pending()");
    await act(() => new Promise((r) => setTimeout(r, 0)));
    quiet = inFlight.all > inFlight.stuck ? 0 : quiet + 1;
  }
}

// A call's JSON body. A streamed call (application/connect+json) sends its
// request in one envelope, behind the 5 bytes of flags and length.
const decode = (init: RequestInit | undefined): unknown => {
  const body = init?.body;
  if (!body) return undefined;
  if (typeof body === "string") return JSON.parse(body);
  const bytes = body as Uint8Array;
  const enveloped = new Headers(init.headers).get("Content-Type")?.startsWith("application/connect+");
  return JSON.parse(new TextDecoder().decode(enveloped ? bytes.subarray(5) : bytes));
};

/** The Plugin scope a test renders in: its name and the Components it requires. */
export interface Scope {
  name: string;
  requires: string[];
}

export interface ScopedOptions {
  /** How the Components answer, by method name. */
  answers?: Answers;
  /** What a method without an answer gets, e.g. an Action; {} by default. */
  unanswered?: (method: string, body: unknown) => Response | Promise<Response>;
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
    const body = decode(init);
    calls.push({ method, url, body });
    return answers[method] ? answers[method](body, init) : unanswered(method, body);
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

/**
 * Renders a Widget's body (at its default size unless given) in a section
 * named "widget", settles, and returns what its useStatus reported last and
 * the calls.
 */
export async function showWidget(scope: Scope, widget: Widget, { size = widget.defaultSize, ...options }: ScopedOptions & { size?: WidgetSize } = {}) {
  const reported: { status?: Status } = {};
  function Probe() {
    reported.status = widget.useStatus();
    return null;
  }
  const calls = renderScoped(
    scope,
    <>
      <Probe />
      <section aria-label="widget">
        <widget.Body size={size} />
      </section>
    </>,
    options,
  );
  await settle();
  return { reported, calls };
}

/** A MetricTile's or a StatRow figure's value, by its label: the text after the label, or undefined while it loads. */
export function metricValue(label: string, container: HTMLElement = document.body): string | undefined {
  const tile = within(container).getByRole("group", { name: label });
  if (within(tile).queryByRole("status", { name: "Loading" })) return undefined;
  return tile.textContent?.slice(label.length);
}
