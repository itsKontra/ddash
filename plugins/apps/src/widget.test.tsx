import { cleanup, screen } from "@testing-library/react";
import type { Status, WidgetSize } from "@ddash/plugin";
import { afterEach, expect, it } from "vitest";
import plugin from "./index";
import { renderScoped, settle } from "./testing";

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

it("counts the visible Apps", async () => {
  renderScoped(<Probe />);
  await settle();

  expect(seen).toEqual({ tone: "up", summary: "4 Apps" });
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
