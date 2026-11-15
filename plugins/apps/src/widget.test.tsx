import { cleanup, fireEvent, screen, within } from "@testing-library/react";
import type { Badge, Status, WidgetSize } from "@ddash/plugin";
import { afterEach, expect, it } from "vitest";
import plugin from "./index";
import { settle, unavailable } from "@ddash/plugin/testing";
import { actions, allUp, checks, list, renderScoped, warned } from "./testing";

afterEach(cleanup);

const widget = plugin.widgets!.find((w) => w.id === "apps")!;

let seen: Status | undefined;
function Probe() {
  seen = widget.useStatus();
  return null;
}

it("is the apps Widget, M or L, L by default", () => {
  expect(widget.sizes).toEqual(["M", "L"]);
  expect(widget.defaultSize).toBe("L");
});

it("is up with the visible Apps' count when all their Checks are up", async () => {
  renderScoped(<Probe />, { ListChecks: () => Response.json(allUp) });
  await settle();

  expect(seen).toEqual({ tone: "up", summary: "4 of 4 Apps up" });
});

it("warns of the Warnings that count, naming their Apps", async () => {
  renderScoped(<Probe />, { ListApps: () => Response.json(warned), ListChecks: () => Response.json(allUp) });
  await settle();

  expect(seen).toEqual({ tone: "warn", summary: "3 Warnings: Jellyfin, Printer" });
});

it("counts no Ignored Warning and none on a Hidden App", async () => {
  // list's Warnings are all Ignored or on the Hidden Old.
  renderScoped(<Probe />, { ListChecks: () => Response.json(allUp) });
  await settle();

  expect(seen?.tone).toBe("up");
});

it("stays down while a Check is down, Warnings or not", async () => {
  renderScoped(<Probe />, { ListApps: () => Response.json(warned) });
  await settle();

  expect(seen).toEqual({ tone: "down", summary: "1 down: Jellyfin" });
});

// allUp with each named App's Check changed by its extra.
const withChecks = (changes: Record<string, object>) => ({
  checks: allUp.checks.map((c) => ({ ...c, ...(c.appId && changes[c.appId]) })),
});

it("is up while only muted Checks are down, counting the watched Apps and telling the muted ones", async () => {
  const mutedDown = withChecks({
    "proxy:jellyfin.home.example": { state: "STATE_DOWN", reason: "HTTP 502", muted: true },
    "proxy:grafana.home.example": { muted: true },
  });
  renderScoped(
    <>
      <Probe />
      <widget.Body size="L" />
    </>,
    { ListChecks: () => Response.json(mutedDown) },
  );
  await settle();

  expect(seen).toEqual({ tone: "up", summary: "2 of 2 watched Apps up · 1 muted down" });
  const jellyfin = screen.getAllByRole("link").find((l) => l.textContent === "Jellyfin")!;
  expect(within(jellyfin).getByRole("img", { name: "down: HTTP 502, muted" })).toBeTruthy();
  expect(screen.getByText("1 muted down")).toBeTruthy();
});

it("is idle, telling the muted ones, when every App's Check is muted", async () => {
  const allMuted = withChecks(
    Object.fromEntries(
      allUp.checks.flatMap((c) => (c.appId ? [[c.appId, { muted: true, ...(c.appId === "proxy:jellyfin.home.example" && { state: "STATE_DOWN" }) }]] : [])),
    ),
  );
  renderScoped(<Probe />, { ListChecks: () => Response.json(allMuted) });
  await settle();

  expect(seen).toEqual({ tone: "idle", summary: "1 muted down" });
});

it("stays down for an unmuted Check that is down, naming only its App", async () => {
  const both = withChecks({
    "proxy:jellyfin.home.example": { state: "STATE_DOWN" },
    "proxy:grafana.home.example": { state: "STATE_DOWN", muted: true },
  });
  renderScoped(<Probe />, { ListChecks: () => Response.json(both) });
  await settle();

  expect(seen).toEqual({ tone: "down", summary: "1 down: Jellyfin · 1 muted down" });
});

it("badges the Nav Entry with how many Warnings count", async () => {
  let badge: Badge | null = null;
  function Badged() {
    badge = plugin.nav!.useBadge!();
    return null;
  }
  renderScoped(<Badged />, { ListApps: () => Response.json(warned) });
  await settle();
  expect(badge).toEqual({ count: 3, tone: "warn" });
  cleanup();

  renderScoped(<Badged />);
  await settle();
  expect(badge).toBeNull();
});

it("is down, naming the Apps whose Check is down, the worst of their Checks", async () => {
  renderScoped(<Probe />);
  await settle();

  expect(seen).toEqual({ tone: "down", summary: "1 down: Jellyfin" });
});

it("warns about certificates expiring within 14 days, never about a pending Check", async () => {
  const noneDown = { checks: checks.checks.filter((c) => !("appId" in c) || c.appId !== "proxy:jellyfin.home.example") };
  renderScoped(<Probe />, { ListChecks: () => Response.json(noneDown) });
  await settle();

  expect(seen).toEqual({ tone: "warn", summary: "Printer: up, certificate expires in 5 days" });
});

it("leaves pending Checks out of the up count, and is idle while every Check is pending", async () => {
  const pending = (only?: string) => ({
    checks: allUp.checks.map((c) => (!only || c.appId === only ? { ...c, state: "STATE_PENDING" } : c)),
  });
  renderScoped(<Probe />, { ListChecks: () => Response.json(pending("proxy:grafana.home.example")) });
  await settle();
  expect(seen).toEqual({ tone: "up", summary: "3 of 4 Apps up" });
  cleanup();

  renderScoped(<Probe />, { ListChecks: () => Response.json(pending()) });
  await settle();
  expect(seen).toEqual({ tone: "idle", summary: "4 Apps, Checks pending" });
});

it("warns about a Flapping Check that is up, and is down while it is down", async () => {
  const flapping = (state: string) => ({
    checks: allUp.checks.map((c) => (c.appId === "proxy:grafana.home.example" ? { ...c, state, flapping: true } : c)),
  });
  renderScoped(<Probe />, { ListChecks: () => Response.json(flapping("STATE_UP")) });
  await settle();
  expect(seen).toEqual({ tone: "warn", summary: "Grafana: up, flapping" });
  cleanup();

  renderScoped(<Probe />, { ListChecks: () => Response.json(flapping("STATE_DOWN")) });
  await settle();
  expect(seen).toEqual({ tone: "down", summary: "1 down: Grafana" });
});

it("reports Maintenance when that is the worst", async () => {
  const maint = { checks: allUp.checks.map((c) => (c.appId === "proxy:grafana.home.example" ? { ...c, maintenance: true } : c)) };
  renderScoped(<Probe />, { ListChecks: () => Response.json(maint) });
  await settle();

  expect(seen).toEqual({ tone: "maint", summary: "1 in Maintenance" });
});

it("leaves out hidden Apps and Apps without a Check yet", async () => {
  const onlyGrafana = { checks: allUp.checks.filter((c) => c.appId === "proxy:grafana.home.example") };
  renderScoped(<Probe />, { ListChecks: () => Response.json(onlyGrafana) });
  await settle();

  expect(seen).toEqual({ tone: "up", summary: "1 of 4 Apps up" });
});

it("is idle while no App has a Check yet", async () => {
  renderScoped(<Probe />, { ListChecks: () => Response.json({}) });
  await settle();

  expect(seen).toEqual({ tone: "idle", summary: "4 Apps, no Checks yet" });
});

it("warns while checks is unavailable", async () => {
  renderScoped(<Probe />, { ListChecks: () => unavailable("checks is down") });
  await settle();

  expect(seen).toEqual({ tone: "warn", summary: "checks unavailable: checks is down" });
});

it("shows each App's Check status on its icon", async () => {
  renderScoped(<widget.Body size="L" />);
  await settle();

  const jellyfin = screen.getAllByRole("link").find((l) => l.textContent === "Jellyfin")!;
  expect(within(jellyfin).getByRole("img", { name: "down: HTTP 502" })).toBeTruthy();
});

it("draws a skeleton while apps answers", async () => {
  renderScoped(<widget.Body size="L" />);

  expect(screen.getByRole("status", { name: "Loading" })).toBeTruthy();
  await settle();
  expect(screen.queryByRole("status", { name: "Loading" })).toBeNull();
});

it("says there are no Apps yet in an empty state", async () => {
  renderScoped(<widget.Body size="M" />, { ListApps: () => Response.json({}) });
  await settle();

  expect(screen.getByText("No Apps yet").tagName).toBe("P");
  expect(screen.getByText(/Proxy Hosts show here/)).toBeTruthy();
});

it("is idle without Apps", async () => {
  renderScoped(<Probe />, { ListApps: () => Response.json({}) });
  await settle();

  expect(seen).toEqual({ tone: "idle", summary: "No Apps yet" });
});

it("warns while apps is unavailable", async () => {
  renderScoped(<Probe />, { ListApps: () => unavailable("apps is down") });
  await settle();

  expect(seen).toEqual({ tone: "warn", summary: "apps unavailable: apps is down" });
});

it.each<[WidgetSize, number]>([
  ["L", 4],
  ["M", 4],
])("shows the visible Apps as icons opening them at size %s", async (size, n) => {
  const Body = widget.Body;
  renderScoped(<Body size={size} />);
  await settle();

  const links = screen.getAllByRole("link");
  expect(links).toHaveLength(n);
  expect(links.map((l) => l.textContent)).toEqual(["Grafana", "Jellyfin", "pfSense", "Printer"]);
  expect(links[1].getAttribute("href")).toBe("https://jellyfin.home.example");
});

/** ListApps with the Apps pinned in pins' order, and SetPins changing them, as apps would. */
function pinning(pins: string[]) {
  return {
    ListApps: () => Response.json({ ...list, apps: list.apps.map((a) => ({ ...a, pin: pins.indexOf(a.id) + 1 })) }),
    SetPins: (body: unknown) => {
      pins = (body as { ids?: string[] }).ids ?? [];
      return Response.json({});
    },
  };
}

const pinnedNames = () =>
  within(screen.getByRole("list", { name: "Pinned Apps" }))
    .getAllByRole("listitem")
    .map((li) => li.textContent);

it("shows only the pinned Apps, in the Operator's order, with no line of more and a link to every App", async () => {
  // Old is pinned too, but Hidden.
  renderScoped(<widget.Body size="M" />, pinning(["proxy:printer.home.example", "proxy:old.home.example", "proxy:grafana.home.example"]));
  await settle();

  const links = within(screen.getByRole("list", { name: "Pinned Apps" })).getAllByRole("link");
  expect(links.map((l) => l.textContent)).toEqual(["Printer", "Grafana"]);
  expect(links[1].getAttribute("href")).toBe("https://grafana.home.example");
  expect(screen.queryByText(/more/)).toBeNull();
  expect(screen.getByRole("link", { name: "All 4 Apps" }).getAttribute("href")).toBe("/apps");
});

it("marks a down pinned App with a dot and a greyed icon, a muted one with the dot alone, an up one with nothing", async () => {
  const states = withChecks({
    "proxy:jellyfin.home.example": { state: "STATE_DOWN", reason: "HTTP 502" },
    "proxy:grafana.home.example": { state: "STATE_DOWN", reason: "HTTP 502", muted: true },
  });
  renderScoped(<widget.Body size="L" />, {
    ...pinning(["proxy:jellyfin.home.example", "proxy:grafana.home.example", "app:00aa11bb22cc33dd"]),
    ListChecks: () => Response.json(states),
  });
  await settle();

  const [jellyfin, grafana, pfsense] = within(screen.getByRole("list", { name: "Pinned Apps" })).getAllByRole("link");
  expect(jellyfin.querySelector("[data-greyed]")).toBeTruthy();
  expect(within(jellyfin).getByRole("img")).toBeTruthy();

  expect(within(grafana).getByRole("img", { name: "down: HTTP 502, muted" })).toBeTruthy();
  expect(grafana.querySelector("[data-greyed]")).toBeNull();

  expect(within(pfsense).queryByRole("img")).toBeNull();
  expect(pfsense.getAttribute("title")).toBe("198.18.0.1");
});

it.each([
  ["L", "48"],
  ["M", "36"],
] as const)("draws the pinned Apps' icons at %s %s px", async (size, px) => {
  renderScoped(<widget.Body size={size} />, pinning(["proxy:grafana.home.example", "app:00aa11bb22cc33dd"]));
  await settle();

  const links = within(screen.getByRole("list", { name: "Pinned Apps" })).getAllByRole("link");
  expect(links.map((l) => l.querySelector("img")?.getAttribute("width"))).toEqual([px, px]);
});

it("names a down pinned App's stopped Workload as the cause in its tooltip and dot, else the Check's reason", async () => {
  // Jellyfin's container exited; Grafana's guest runs, so its Check says why.
  const states = withChecks({
    "proxy:jellyfin.home.example": { state: "STATE_DOWN", reason: "HTTP 502" },
    "proxy:grafana.home.example": { state: "STATE_DOWN", reason: "HTTP 502" },
  });
  renderScoped(<widget.Body size="L" />, {
    ...pinning(["proxy:jellyfin.home.example", "proxy:grafana.home.example"]),
    ListChecks: () => Response.json(states),
  });
  await settle();

  const [jellyfin, grafana] = within(screen.getByRole("list", { name: "Pinned Apps" })).getAllByRole("link");
  expect(jellyfin.getAttribute("title")).toBe("jellyfin.home.example · down: Container jellyfin exited");
  expect(within(jellyfin).getByRole("img", { name: "down: Container jellyfin exited" })).toBeTruthy();
  expect(grafana.getAttribute("title")).toBe("grafana.home.example · down: HTTP 502");
  expect(within(grafana).getByRole("img", { name: "down: HTTP 502" })).toBeTruthy();
});

it("reads no Workload while no pinned App is down", async () => {
  // Jellyfin is down, its container exited, but it isn't pinned.
  const calls = renderScoped(<widget.Body size="L" />, pinning(["proxy:grafana.home.example"]));
  await settle();

  expect(screen.getByRole("list", { name: "Pinned Apps" })).toBeTruthy();
  expect(calls.filter((c) => ["ListProjects", "ListWorkloads", "ListPermissions"].includes(c.method))).toEqual([]);
});

it("pins, orders and unpins Apps in its edit state, changing at once", async () => {
  const calls = renderScoped(<widget.Body size="L" />, pinning(["proxy:grafana.home.example"]));
  await settle();
  fireEvent.click(screen.getByRole("button", { name: "Edit pins" }));

  fireEvent.change(screen.getByRole("combobox", { name: "Pin an App" }), { target: { value: "proxy:printer.home.example" } });
  await settle();
  expect(pinnedNames()).toEqual(["Grafana", "Printer"]);

  fireEvent.click(screen.getByRole("button", { name: "Move Printer earlier" }));
  await settle();
  expect(pinnedNames()).toEqual(["Printer", "Grafana"]);
  expect((screen.getByRole("button", { name: "Move Printer earlier" }) as HTMLButtonElement).disabled).toBe(true);

  fireEvent.click(screen.getByRole("button", { name: "Unpin Grafana" }));
  await settle();
  expect(pinnedNames()).toEqual(["Printer"]);
  expect(actions(calls)).toEqual([
    { method: "SetPins", body: { ids: ["proxy:grafana.home.example", "proxy:printer.home.example"] } },
    { method: "SetPins", body: { ids: ["proxy:printer.home.example", "proxy:grafana.home.example"] } },
    { method: "SetPins", body: { ids: ["proxy:printer.home.example"] } },
  ]);

  fireEvent.click(screen.getByRole("button", { name: "Done" }));
  expect(within(screen.getByRole("list", { name: "Pinned Apps" })).getAllByRole("link").map((l) => l.textContent)).toEqual(["Printer"]);
});

it("falls back to the first Apps once the last pin goes", async () => {
  renderScoped(<widget.Body size="L" />, pinning(["proxy:grafana.home.example"]));
  await settle();
  fireEvent.click(screen.getByRole("button", { name: "Edit pins" }));
  fireEvent.click(screen.getByRole("button", { name: "Unpin Grafana" }));
  await settle();
  expect(screen.getByText(/Nothing pinned/)).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: "Done" }));

  expect(screen.queryByRole("list", { name: "Pinned Apps" })).toBeNull();
  expect(screen.getAllByRole("link").map((l) => l.textContent)).toEqual(["Grafana", "Jellyfin", "pfSense", "Printer"]);
});
