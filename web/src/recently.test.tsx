import { create } from "@bufbuild/protobuf";
import { timestampFromMs, durationFromMs } from "@bufbuild/protobuf/wkt";
import { act, cleanup, render, screen, within } from "@testing-library/react";
import { CoreScope, createQueryClient, PluginHost } from "@ddash/plugin";
import { AuditChainSchema, AuditEntrySchema, type AuditEntry } from "@ddash/proto/ddash/core/core_pb";
import { MemoryRouter } from "react-router";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { ago, collapsed, listed, recent, Recently, subject, words, type Recent } from "./recently";

const now = Date.parse("2026-11-15T19:45:00Z");

// A hop, started secs before now, that took ms (undefined: still running).
const hop = (target: string, method: string, secs: number, ms: number | undefined, more: Partial<Pick<AuditEntry, "outcome" | "message" | "actorKind" | "username" | "request">> = {}) => ({
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

describe("subject", () => {
  it.each([
    ['{"action":{"restart":{"host":"media","project":"paperless"}},"plan":{"steps":[{"title":"Restart","values":[{"label":"x"}]}]}}', "restart paperless on media"],
    ['{"duration":"600s"}', "10 min"],
    ['{"duration":"3600s"}', "1 h"],
    ['{"plugin":"docker"}', "docker"],
    ['{"vmid":101,"name":"homeassist","url":"https://10.0.0.5:7000"}', "homeassist"],
    ['{"vmid":206,"code":"REDACTED"}', "206"],
    ['{"workloads":[{"kind":"KIND_LXC","host":"pve","id":"206"}]}', "206 on pve"],
    ['{"action":"ACTION_STOP","workload":{"host":"pve","id":"104"}}', "stop 104 on pve"],
    ['{"layout":{"entries":[{"widget":"apps.apps","size":"L"}]}}', ""],
    ["{}", ""],
    ["(unrecordable: oops)", ""],
  ])("%s reads %j", (request, text) => expect(subject(request)).toBe(text));

  it("reads a request the Audit Log cut short", () => {
    expect(subject('{"action":{"update":{"host":"apps","project":"media"}},"plan":{"steps":[{"title":"Pull…(truncated)')).toBe("update media on apps");
  });

  it("is never longer than a row can take", () => {
    const long = subject(`{"name":"${"x".repeat(200)}"}`);
    expect(long).toHaveLength(60);
    expect(long.endsWith("…")).toBe(true);
  });
});

describe("listed", () => {
  it("lists what an Operator or ddash did, and leaves housekeeping to the Audit Log", () => {
    expect(listed(chain("c", hop("core", "Install", 5, 1)))).toBe(true);
    expect(listed(chain("c", hop("core", "Install", 5, 1, { actorKind: "core", username: "" })))).toBe(true);
    expect(listed(chain("c", hop("core", "SaveLayout", 5, 1)))).toBe(false);
    expect(listed(chain("c", hop("core", "SignIn", 5, 0)))).toBe(false);
    expect(listed(chain("c", hop("core", "SignIn", 5, 0, { outcome: "permission_denied" })))).toBe(false);
    expect(listed(chain("c", hop("core", "SignOut", 5, 0)))).toBe(false);
    expect(listed(chain("c", hop("checks", "SetAppChecks", 5, 1, { actorKind: "component", username: "apps" })))).toBe(false);
  });
});

describe("recent", () => {
  it("says what the actor started, who and when, and how long it took to the last hop's end", () => {
    const root = hop("proxmox", "RunUpdateAll", 60, 20, { request: '{"workloads":[{"kind":"KIND_LXC","host":"pve","id":"201"}]}' });
    const r = recent(chain("c1", root, hop("host-agent-api", "CollectJob", 48, 5_000)), now)!;
    expect(r).toEqual({
      id: "c1",
      what: "Run update all",
      subject: "201 on pve",
      target: "proxmox",
      who: "alice",
      at: now - 60_000,
      outcome: "done",
      detail: "done in 17 s",
    });
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

describe("collapsed", () => {
  const item = (id: string, more: Partial<Recent> = {}): Recent => ({
    id,
    what: "Set pins",
    subject: "",
    target: "apps",
    who: "alice",
    at: now - Number(id.slice(1)) * 60_000,
    outcome: "done",
    detail: "done in 20 ms",
    ...more,
  });

  it("makes a run of the same Action one row with its count and the newest's time", () => {
    expect(collapsed([item("r1"), item("r2"), item("r3"), item("r4")])).toEqual([{ ...item("r1"), count: 4 }]);
  });

  it("shows a run's failure, the newest one's, whatever ended last", () => {
    const run = [item("r1"), item("r2", { outcome: "failed", detail: "apps is down" }), item("r3", { outcome: "failed", detail: "timed out" })];
    expect(collapsed(run)).toEqual([{ ...item("r1"), outcome: "failed", detail: "apps is down", count: 3 }]);
  });

  it("keeps apart what differs in who, what, target or subject, and the same Action that isn't in a row", () => {
    const items = [
      item("r1"),
      item("r2", { who: "bob" }),
      item("r3", { what: "Pin" }),
      item("r4", { target: "docker" }),
      item("r5", { subject: "grafana" }),
      item("r6"),
    ];
    expect(collapsed(items)).toEqual(items);
    expect(collapsed([])).toEqual([]);
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
    request: e.request,
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
    json(chain("c3", hop("core", "Install", 30, 10, { outcome: "Unavailable", message: "the Release has no such Plugin", request: '{"plugin":"weather"}' }))),
    json(chain("c2", hop("proxmox", "RunUpdateAll", 120, 20))),
  ];
  await show();
  expect(requests).toEqual([{ pageSize: 50 }]);
  expect(rows().map((r) => r.getAttribute("aria-label"))).toEqual(["Install: failed", "Run update all: done"]);
  expect(rows()[0].textContent).toContain("Install · weather");
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

it("passes over housekeeping to find the last four Actions", async () => {
  const action = (id: string, plugin: string) => json(chain(id, hop("core", "Install", 60, 10, { request: `{"plugin":"${plugin}"}` })));
  chains = [
    json(chain("s1", hop("core", "SaveLayout", 1, 5))),
    action("a1", "docker"),
    json(chain("s2", hop("checks", "SetAppChecks", 2, 5, { actorKind: "component", username: "apps" }))),
    action("a2", "network"),
    action("a3", "energy"),
    json(chain("s3", hop("core", "SaveLayout", 3, 5))),
    json(chain("s4", hop("core", "SignIn", 4, 0))),
    json(chain("s5", hop("core", "SignOut", 4, 0))),
    action("a4", "checks"),
    action("a5", "apps"),
  ];
  await show();
  expect(rows().map((r) => r.textContent?.split(" · ")[1].split("done")[0].trim())).toEqual(["docker", "network", "energy", "checks"]);
});

it("lists the Operator ordering pins over and over as one row, and still four distinct ones", async () => {
  const pins = (id: string, secs: number, more = {}) => json(chain(id, hop("apps", "SetPins", secs, 20, more)));
  const action = (id: string, plugin: string) => json(chain(id, hop("core", "Install", 600, 10, { request: `{"plugin":"${plugin}"}` })));
  chains = [
    pins("p1", 10),
    json(chain("s1", hop("core", "SaveLayout", 11, 5))),
    pins("p2", 12, { outcome: "Unavailable", message: "apps is restarting" }),
    ...Array.from({ length: 20 }, (_, i) => pins(`p${i + 3}`, 13 + i)),
    action("a1", "docker"),
    action("a2", "network"),
    action("a3", "energy"),
    action("a4", "checks"),
  ];
  await show();
  expect(rows().map((r) => r.getAttribute("aria-label"))).toEqual(["Set pins ×22: failed", "Install: done", "Install: done", "Install: done"]);
  expect(rows()[0].textContent).toContain("Set pins ×22 apps");
  expect(rows()[0].textContent).toContain("apps is restarting");
  expect(rows()[0].textContent).toContain("alice · just now");
});

it("says so when nothing has run yet", async () => {
  await show();
  expect(screen.getByText(/No Actions yet/)).toBeTruthy();
});
