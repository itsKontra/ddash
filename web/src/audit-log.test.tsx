import { act, cleanup, fireEvent, render, screen, within } from "@testing-library/react";
import { createQueryClient } from "@ddash/plugin";
import { MemoryRouter, useLocation, useNavigate, type NavigateFunction } from "react-router";
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
type Answer = (request: Record<string, unknown>) => Record<string, unknown> | Response;
let requests: Record<string, unknown>[];

// Where the router is, and a way to move it, as the browser's Back would.
let location: { pathname: string; search: string };
let navigate: NavigateFunction;
function Probe() {
  location = useLocation();
  navigate = useNavigate();
  return null;
}

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
      const answered = answer(request);
      return answered instanceof Response ? answered : Response.json(answered);
    }
    return Response.json({ plugins: [] });
  });
  render(
    <MemoryRouter initialEntries={[path]}>
      <App client={createQueryClient()} fetch={fetch} />
      <Probe />
    </MemoryRouter>,
  );
  await settle();
}

const settle = () => act(() => new Promise((r) => setTimeout(r, 20)));

// The table's body rows, each led by its Action as the row header.
const rows = () => screen.queryAllByRole("row").filter((r) => within(r).queryByRole("rowheader"));

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
const installed = {
  id: "c1",
  entries: [entry("core", "Install", { time: "2026-10-18T08:30:00Z", request: '{"plugin":"docker"}' })],
};
const failed = {
  id: "c3",
  entries: [entry("core", "Uninstall", { outcome: "failed", message: "docker is still running", duration: "2s" })],
};

it("is a Page under Settings, a table of Operator-rooted chains, newest first", async () => {
  await boot("/settings", () => ({ chains: [relayed, installed], targets: ["core", "greeter", "relay"] }));

  fireEvent.click(screen.getByRole("link", { name: /Audit Log/ }));
  await settle();

  expect(screen.getByRole("heading", { level: 1, name: "Audit Log" })).toBeTruthy();
  expect(within(screen.getByRole("main")).getByRole("link", { name: "Settings" }).getAttribute("href")).toBe("/settings");
  expect(requests).toEqual([{ actorKind: "operator" }]);
  expect(screen.getAllByRole("columnheader").map((h) => h.textContent)).toEqual(["Action", "When", "Actor", "Outcome", "Hops", "Took"]);
  expect(rows()).toHaveLength(2);
  const [first, second] = rows();
  expect(within(first).getByRole("rowheader").textContent).toMatch(/relay.*Act.*disruptive/);
  expect(first.textContent).toMatch(/operator:alice.*ok.*1 hop not ok.*2.*1\.5 s/);
  expect(second.textContent).toMatch(/core.*Install.*operator:alice.*ok.*1.*12 ms/);
  expect(second.textContent).not.toMatch(/disruptive|not ok/);
  expect(within(first).getByText((_, el) => el?.tagName === "TIME").getAttribute("datetime")).toBe("2026-10-19T08:30:00.000Z");
});

it("opens a chain in a Sheet: the failure, its facts, and each hop with its redacted request", async () => {
  await boot("/settings/audit-log", () => ({ chains: [failed, relayed], targets: [] }));

  fireEvent.click(rows()[0]);
  let sheet = within(screen.getByRole("dialog", { name: "core · Uninstall" }));
  expect(sheet.getByRole("alert").textContent).toMatch(/docker is still running/);
  fireEvent.click(sheet.getByRole("button", { name: "Close" }));

  fireEvent.click(rows()[1]);
  sheet = within(screen.getByRole("dialog", { name: "relay · Act" }));
  const facts = sheet.getAllByRole("term").map((t) => [t.textContent, t.nextElementSibling?.textContent]);
  expect(facts).toEqual([
    ["Actor", "operator:alice"],
    ["Outcome", "ok1 hop not ok"],
    ["Took", "1.5 s"],
    ["Hops", "2"],
  ]);
  const hops = sheet.getAllByRole("listitem").filter((li) => li.closest("ol[data-timeline]"));
  expect(hops).toHaveLength(2);
  expect(hops[0].textContent).toMatch(/relay.*Act.*ok.*1\.5 s/);
  expect(within(hops[0]).getByText('{"note":"relay","password":"REDACTED"}').tagName).toBe("PRE");
  expect(hops[1].textContent).toMatch(/greeter.*Act.*PermissionDenied.*3 ms.*protected in Proxmox/);
  expect(within(hops[1]).getByText('{"note":"onward"}')).toBeTruthy();
});

it("filters by actor kind, target and outcome, listing the targets the core gave", async () => {
  await boot("/settings/audit-log", () => ({ chains: [installed], targets: ["core", "greeter", "relay"] }));

  const actors = within(screen.getByRole("tablist", { name: "Actor" }));
  expect(actors.getAllByRole("tab").map((t) => [t.textContent, t.getAttribute("aria-selected")])).toEqual([
    ["Operators", "true"],
    ["Components", "false"],
    ["Core", "false"],
    ["Anyone", "false"],
  ]);
  const target = () => screen.getByRole("combobox", { name: "Target" });
  expect(within(target()).getAllByRole("option").map((o) => o.textContent)).toEqual(["Any target", "core", "greeter", "relay"]);
  fireEvent.click(actors.getByRole("tab", { name: "Components" }));
  await settle();
  fireEvent.change(target(), { target: { value: "greeter" } });
  await settle();
  fireEvent.change(screen.getByRole("combobox", { name: "Outcome" }), { target: { value: "failed" } });
  await settle();
  expect(requests.at(-1)).toEqual({ actorKind: "component", target: "greeter", outcome: "failed" });

  fireEvent.click(actors.getByRole("tab", { name: "Anyone" }));
  await settle();
  expect(requests.at(-1)).toEqual({ target: "greeter", outcome: "failed" });
});

it("keeps the filters and the sort in the URL, so they survive a reload and Back", async () => {
  await boot("/settings/audit-log", () => ({ chains: [installed, relayed], targets: ["greeter"] }));

  fireEvent.click(screen.getByRole("tab", { name: "Anyone" }));
  await settle();
  fireEvent.change(screen.getByRole("combobox", { name: "Target" }), { target: { value: "greeter" } });
  await settle();
  fireEvent.click(screen.getByRole("button", { name: /When/ }));
  await settle();
  expect(rows().map((r) => within(r).getByRole("rowheader").textContent)).toEqual([expect.stringMatching(/Install/), expect.stringMatching(/relay/)]);
  const url = location.pathname + location.search;

  act(() => navigate("/settings"));
  await settle();
  act(() => navigate(-1));
  await settle();
  expect(location.pathname + location.search).toBe(url);

  cleanup();
  requests = [];
  await boot(url, () => ({ chains: [relayed, installed], targets: ["greeter"] }));
  expect(requests).toEqual([{ target: "greeter" }]);
  expect(screen.getByRole("tab", { name: "Anyone" }).getAttribute("aria-selected")).toBe("true");
  expect((screen.getByRole("combobox", { name: "Target" }) as HTMLSelectElement).value).toBe("greeter");
  expect(screen.getByRole("columnheader", { name: /When/ }).getAttribute("aria-sort")).toBe("ascending");
  expect(rows().map((r) => within(r).getByRole("rowheader").textContent)).toEqual([expect.stringMatching(/Install/), expect.stringMatching(/relay/)]);
});

it("loads older chains a page at a time", async () => {
  await boot("/settings/audit-log", (req) =>
    req.pageToken === "older" ? { chains: [installed], targets: [] } : { chains: [relayed], nextPageToken: "older", targets: [] },
  );

  fireEvent.click(screen.getByRole("button", { name: "Load older Actions" }));
  await settle();

  expect(requests.at(-1)).toEqual({ actorKind: "operator", pageToken: "older" });
  expect(rows().map((r) => r.textContent)).toEqual([expect.stringMatching(/relay/), expect.stringMatching(/Install/)]);
  expect(screen.queryByRole("button", { name: "Load older Actions" })).toBeNull();
});

it("says so when no chain matches", async () => {
  await boot("/settings/audit-log", () => ({ chains: [], targets: [] }));

  expect(screen.getByText("No Actions match")).toBeTruthy();
});

it("says so when the Audit Log can't be read", async () => {
  await boot("/settings/audit-log", () => Response.json({ code: "failed_precondition", message: "disk full" }, { status: 400 }));

  expect(screen.getByRole("alert").textContent).toMatch(/The Audit Log can't be read/);
});
