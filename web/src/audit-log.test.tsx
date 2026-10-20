import { act, cleanup, fireEvent, render, screen, within } from "@testing-library/react";
import { createQueryClient } from "@ddash/plugin";
import { MemoryRouter } from "react-router";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { App } from "./app";

// An Audit Log entry as ListAuditLog answers it in JSON.
const entry = (target: string, method: string, more: Record<string, unknown> = {}) => ({
  time: "2026-10-19T08:30:00.000000001Z",
  actorKind: "operator",
  username: "alice",
  sub: "f3a1",
  target,
  method,
  request: "{}",
  outcome: "ok",
  duration: "0.012s",
  ...more,
});

// What ListAuditLog answers, by request; requests holds every request sent.
type Answer = (request: Record<string, unknown>) => Record<string, unknown>;
let requests: Record<string, unknown>[];

beforeEach(() => {
  requests = [];
});

afterEach(cleanup);

async function boot(path: string, answer: Answer) {
  const fetch = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = String(input);
    if (url.endsWith("/ListAuditLog")) {
      const body = init?.body;
      const request = JSON.parse(typeof body === "string" ? body : new TextDecoder().decode(body as Uint8Array));
      requests.push(request);
      return Response.json(answer(request));
    }
    return Response.json({ plugins: [] });
  });
  render(
    <MemoryRouter initialEntries={[path]}>
      <App client={createQueryClient()} fetch={fetch} />
    </MemoryRouter>,
  );
  await settle();
}

const settle = () => act(() => new Promise((r) => setTimeout(r, 20)));

const chains = () => within(screen.getByRole("list", { name: "Chains" })).queryAllByRole("button", { expanded: false });

const relayed = {
  id: "c2",
  entries: [
    entry("relay", "Act", { disruptive: true, request: '{"note":"relay","password":"REDACTED"}', duration: "1.5s" }),
    entry("greeter", "Act", {
      disruptive: true,
      request: '{"note":"onward"}',
      outcome: "PermissionDenied",
      message: "protected in Proxmox",
      duration: "0.003s",
    }),
  ],
};
const installed = { id: "c1", entries: [entry("core", "Install", { request: '{"plugin":"docker"}' })] };

it("is a Page under Settings, showing Operator-rooted chains, newest first", async () => {
  await boot("/settings", () => ({ chains: [relayed, installed], targets: ["core", "greeter", "relay"] }));

  fireEvent.click(screen.getByRole("link", { name: /Audit Log/ }));
  await settle();

  expect(screen.getByRole("heading", { name: "Audit Log" })).toBeTruthy();
  expect(requests).toEqual([{ actorKind: "operator" }]);
  const rows = chains();
  expect(rows).toHaveLength(2);
  expect(rows[0].textContent).toMatch(/alice.*relay.*Act.*disruptive.*ok.*1\.5 s.*2 hops/);
  expect(rows[1].textContent).toMatch(/alice.*core.*Install.*ok.*12 ms/);
  expect(rows[1].textContent).not.toMatch(/disruptive|hops/);
  expect(within(rows[0]).getByText((_, el) => el?.tagName === "TIME").getAttribute("datetime")).toBe("2026-10-19T08:30:00.000Z");
});

it("expands a root to its hops, each with its outcome, duration and redacted request", async () => {
  await boot("/settings/audit-log", () => ({ chains: [relayed, installed], targets: [] }));

  fireEvent.click(chains()[0]);

  const hops = within(screen.getByRole("list", { name: "Hops" })).getAllByRole("listitem");
  expect(hops).toHaveLength(2);
  expect(hops[0].textContent).toMatch(/operator:alice.*relay.*Act.*disruptive.*ok.*1\.5 s/);
  expect(within(hops[0]).getByText('{"note":"relay","password":"REDACTED"}')).toBeTruthy();
  expect(hops[1].textContent).toMatch(/greeter.*Act.*PermissionDenied.*protected in Proxmox.*3 ms/);
  expect(within(hops[1]).getByText('{"note":"onward"}')).toBeTruthy();
  expect(screen.getByRole("button", { expanded: true }).textContent).toMatch(/relay/);
});

it("filters by actor kind, target and outcome, listing the targets the core gave", async () => {
  await boot("/settings/audit-log", () => ({ chains: [installed], targets: ["core", "greeter", "relay"] }));

  const target = screen.getByRole("combobox", { name: "Target" });
  expect(within(target).getAllByRole("option").map((o) => o.textContent)).toEqual(["Any target", "core", "greeter", "relay"]);
  fireEvent.change(screen.getByRole("combobox", { name: "Actor" }), { target: { value: "component" } });
  await settle();
  fireEvent.change(target, { target: { value: "greeter" } });
  await settle();
  fireEvent.change(screen.getByRole("combobox", { name: "Outcome" }), { target: { value: "failed" } });
  await settle();
  expect(requests.at(-1)).toEqual({ actorKind: "component", target: "greeter", outcome: "failed" });

  fireEvent.change(screen.getByRole("combobox", { name: "Actor" }), { target: { value: "" } });
  await settle();
  expect(requests.at(-1)).toEqual({ target: "greeter", outcome: "failed" });
});

it("loads older chains a page at a time", async () => {
  await boot("/settings/audit-log", (req) =>
    req.pageToken === "older" ? { chains: [installed], targets: [] } : { chains: [relayed], nextPageToken: "older", targets: [] },
  );

  fireEvent.click(screen.getByRole("button", { name: "Older" }));
  await settle();

  expect(requests.at(-1)).toEqual({ actorKind: "operator", pageToken: "older" });
  expect(chains().map((c) => c.textContent)).toEqual([expect.stringMatching(/relay/), expect.stringMatching(/Install/)]);
  expect(screen.queryByRole("button", { name: "Older" })).toBeNull();
});

it("says so when no chain matches", async () => {
  await boot("/settings/audit-log", () => ({ chains: [], targets: [] }));

  expect(screen.getByText("No Actions match.")).toBeTruthy();
});
