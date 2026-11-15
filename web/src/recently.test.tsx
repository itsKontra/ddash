import { create } from "@bufbuild/protobuf";
import { timestampFromMs, durationFromMs } from "@bufbuild/protobuf/wkt";
import { act, cleanup, render, screen, within } from "@testing-library/react";
import { CoreScope, createQueryClient, PluginHost } from "@ddash/plugin";
import { AuditChainSchema, AuditEntrySchema, type AuditEntry } from "@ddash/proto/ddash/core/core_pb";
import { MemoryRouter } from "react-router";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { ago, recent, Recently, words } from "./recently";

const now = Date.parse("2026-11-15T19:45:00Z");

// A hop, started secs before now, that took ms (undefined: still running).
const hop = (target: string, method: string, secs: number, ms: number | undefined, more: Partial<Pick<AuditEntry, "outcome" | "message" | "actorKind" | "username">> = {}) => ({
  time: timestampFromMs(now - secs * 1000),
  actorKind: "operator",
  username: "alice",
  target,
  method,
  outcome: ms === undefined ? "" : "ok",
  duration: ms === undefined ? undefined : durationFromMs(ms),
  ...more,
});
const chain = (id: string, ...entries: ReturnType<typeof hop>[]) => create(AuditChainSchema, { id, entries: entries.map((e) => create(AuditEntrySchema, e)) });

describe("words", () => {
  it.each([
    ["RunUpdateAll", "Run update all"],
    ["AddSSHKey", "Add SSH key"],
    ["Install", "Install"],
    ["PermissionDenied", "Permission denied"],
  ])("%s reads %s", (method, text) => expect(words(method)).toBe(text));
});

describe("recent", () => {
  it("says what the actor started, who and when, and how long it took to the last hop's end", () => {
    const r = recent(chain("c1", hop("proxmox", "RunUpdateAll", 60, 20), hop("host-agent-api", "CollectJob", 48, 5_000)), now)!;
    expect(r).toEqual({ id: "c1", what: "Run update all", target: "proxmox", who: "alice", at: now - 60_000, outcome: "done", detail: "done in 17 s" });
  });

  it("runs while any hop runs, and names the step it is at", () => {
    expect(recent(chain("c", hop("proxmox", "RunUpdateAll", 12, undefined)), now)).toMatchObject({ outcome: "running", detail: "running for 12 s" });
    expect(recent(chain("c", hop("proxmox", "RunUpdateAll", 12, 30), hop("proxmox-api", "CreateSnapshot", 2, undefined)), now)).toMatchObject({
      outcome: "running",
      detail: "running for 12 s · Create snapshot",
    });
  });

  it("fails with the root's error, or with a job's collected after the Action returned", () => {
    expect(recent(chain("c", hop("core", "Install", 5, 10, { outcome: "PermissionDenied", message: "not allowed" })), now)).toMatchObject({ outcome: "failed", detail: "not allowed" });
    expect(recent(chain("c", hop("core", "Install", 5, 10, { outcome: "Unavailable" })), now)).toMatchObject({ outcome: "failed", detail: "unavailable" });
    const job = chain("c", hop("proxmox", "RunUpdateAll", 60, 20), hop("host-agent-api", "CollectJob", 30, 5, { outcome: "Aborted", message: "apt-get failed" }));
    expect(recent(job, now)).toMatchObject({ outcome: "failed", detail: "apt-get failed" });
  });

  it("ended partly when it ended well but another hop failed", () => {
    const sent = chain("c", hop("proxmox", "RunUpdateAll", 60, 20), hop("ntfy-api", "Send", 30, 5, { outcome: "Unavailable", message: "ntfy is down" }));
    expect(recent(sent, now)).toMatchObject({ outcome: "partly", detail: "done in 30 s, but Send on ntfy-api failed: ntfy is down" });
  });

  it("was interrupted when ddash stopped mid-way", () => {
    expect(recent(chain("c", hop("docker", "Restart", 60, 20, { outcome: "interrupted" })), now)).toMatchObject({ outcome: "interrupted", detail: "ddash stopped before it ended" });
  });

  it("names ddash for the core and the Component for a Component", () => {
    expect(recent(chain("c", hop("core", "Install", 5, 1, { actorKind: "core", username: "" })), now)?.who).toBe("ddash");
    expect(recent(chain("c", hop("checks", "SetAppChecks", 5, 1, { actorKind: "component", username: "apps" })), now)?.who).toBe("apps");
  });
});

it.each([
  [10_000, "just now"],
  [5 * 60_000, "5 min ago"],
  [3 * 3_600_000, "3 h ago"],
  [72 * 3_600_000, "3 d ago"],
])("%d ms ago reads %s", (ms, text) => expect(ago(now - ms, now)).toBe(text));

// The Recently Panel against a fake core whose Audit Log is chains.
let chains: object[];
let requests: unknown[];

beforeEach(() => {
  vi.useFakeTimers({ now });
  chains = [];
  requests = [];
});

afterEach(() => {
  cleanup();
  vi.useRealTimers();
});

const json = (c: ReturnType<typeof chain>) => ({
  id: c.id,
  entries: c.entries.map((e) => ({
    time: new Date(Number(e.time!.seconds) * 1000 + e.time!.nanos / 1e6).toISOString(),
    actorKind: e.actorKind,
    username: e.username,
    target: e.target,
    method: e.method,
    outcome: e.outcome,
    message: e.message,
    ...(e.duration ? { duration: `${Number(e.duration.seconds) + e.duration.nanos / 1e9}s` } : {}),
  })),
});

async function flush() {
  for (let i = 0; i < 5; i++) await act(() => vi.advanceTimersByTimeAsync(1));
}

async function show() {
  const fetch = vi.fn(async (_input: RequestInfo | URL, init?: RequestInit) => {
    requests.push(JSON.parse(new TextDecoder().decode(init?.body as Uint8Array)));
    return Response.json({ chains });
  });
  render(
    <MemoryRouter>
      <PluginHost client={createQueryClient()}>
        <CoreScope fetch={fetch}>
          <Recently />
        </CoreScope>
      </PluginHost>
    </MemoryRouter>,
  );
  await flush();
}

const rows = () => within(screen.getByRole("list", { name: "Recent Actions" })).getAllByRole("listitem");

it("lists the last four Actions, telling a failed one from a finished one by its icon", async () => {
  chains = [
    json(chain("c3", hop("core", "Install", 30, 10, { outcome: "Unavailable", message: "the Release has no such Plugin" }))),
    json(chain("c2", hop("proxmox", "RunUpdateAll", 120, 20))),
  ];
  await show();
  expect(requests).toEqual([{ pageSize: 4 }]);
  expect(rows().map((r) => r.getAttribute("aria-label"))).toEqual(["Install: failed", "Run update all: done"]);
  expect(within(rows()[0]).getByRole("img", { name: "failed" })).toBeTruthy();
  expect(rows()[0].textContent).toContain("the Release has no such Plugin");
  expect(within(rows()[1]).getByRole("img", { name: "done" })).toBeTruthy();
  expect(rows()[1].textContent).toContain("alice · 2 min ago");
  expect(screen.getByRole("link", { name: "Audit Log" }).getAttribute("href")).toBe("/settings/audit-log?actor=anyone");
});

it("shows an Action started elsewhere within 5 s, with its progress, without a reload", async () => {
  chains = [json(chain("c1", hop("core", "Install", 600, 10)))];
  await show();
  expect(rows()).toHaveLength(1);

  chains = [json(chain("c2", hop("proxmox", "RunUpdateAll", 0, 20), hop("proxmox-api", "CreateSnapshot", 0, undefined))), ...chains];
  await act(() => vi.advanceTimersByTimeAsync(5_000));
  await flush();
  expect(rows().map((r) => r.getAttribute("aria-label"))).toEqual(["Run update all: running", "Install: done"]);
  expect(within(rows()[0]).getByRole("img", { name: "running" })).toBeTruthy();
  expect(rows()[0].textContent).toContain("Create snapshot");
});

it("says so when nothing has run yet", async () => {
  await show();
  expect(screen.getByText(/No Actions yet/)).toBeTruthy();
});
