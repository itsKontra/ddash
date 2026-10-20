import { cleanup, screen, within } from "@testing-library/react";
import type { Status, WidgetSize } from "@ddash/plugin";
import { afterEach, expect, it } from "vitest";
import plugin from "./index";
import { allUp, checks, renderScoped, settle } from "./testing";

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

it("is down, naming the Apps whose Check is down, the worst of their Checks", async () => {
  renderScoped(<Probe />);
  await settle();

  expect(seen).toEqual({ tone: "down", summary: "1 down: Jellyfin" });
});

it("warns about pending Checks and certificates expiring within 14 days", async () => {
  const noneDown = { checks: checks.checks.filter((c) => !("appId" in c) || c.appId !== "proxy:jellyfin.home.example") };
  renderScoped(<Probe />, { ListChecks: () => Response.json(noneDown) });
  await settle();

  expect(seen).toEqual({ tone: "warn", summary: "pfSense: pending: timeout; Printer: up, certificate expires in 5 days" });
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
  renderScoped(<Probe />, { ListChecks: () => Response.json({ code: "unavailable", message: "checks is down" }, { status: 503 }) });
  await settle();

  expect(seen).toEqual({ tone: "warn", summary: "checks unavailable: checks is down" });
});

it("shows each App's Check status on its icon", async () => {
  renderScoped(<widget.Body size="L" />);
  await settle();

  const jellyfin = screen.getAllByRole("link").find((l) => l.textContent === "Jellyfin")!;
  expect(within(jellyfin).getByRole("img", { name: "down: HTTP 502" })).toBeTruthy();
});

it("is idle without Apps", async () => {
  renderScoped(<Probe />, { ListApps: () => Response.json({}) });
  await settle();

  expect(seen).toEqual({ tone: "idle", summary: "No Apps yet" });
});

it("warns while apps is unavailable", async () => {
  renderScoped(<Probe />, { ListApps: () => Response.json({ code: "unavailable", message: "apps is down" }, { status: 503 }) });
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
