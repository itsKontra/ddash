import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, expect, it } from "vitest";
import { Badge, PageHeader, Sparkline, StatusDot, Switch, UptimeBars } from "./index";

afterEach(cleanup);

it("StatusDot names its tone for screen readers", () => {
  render(<StatusDot tone="down" />);

  expect(screen.getByRole("img", { name: "down" })).toBeTruthy();
});

it("StatusDot takes a label in place of the tone", () => {
  render(<StatusDot tone="up" label="running" />);

  expect(screen.getByRole("img", { name: "running" })).toBeTruthy();
});

it("Badge shows its content", () => {
  render(<Badge tone="warn">3 new</Badge>);

  expect(screen.getByText("3 new")).toBeTruthy();
});

it("Sparkline draws each series on one scale across the full width, named for screen readers", () => {
  render(
    <Sparkline
      label="WAN over the last hour"
      series={[
        [0, 5, 10],
        [2, 2],
      ]}
    />,
  );

  const img = screen.getByRole("img", { name: "WAN over the last hour" });
  const lines = img.querySelectorAll("polyline");
  expect([...lines].map((l) => l.getAttribute("points"))).toEqual(["0,24 50,12 100,0", "0,19.2 100,19.2"]);
});

it("Sparkline draws a flat line along the bottom when everything is zero, and nothing without values", () => {
  render(<Sparkline label="idle" series={[[0, 0], []]} />);

  const lines = screen.getByRole("img", { name: "idle" }).querySelectorAll("polyline");
  expect([...lines].map((l) => l.getAttribute("points"))).toEqual(["0,24 100,24"]);
});

it("PageHeader shows the title as the Page's heading, with subtitle and actions", () => {
  render(<PageHeader title="Docker" subtitle="Containers per host" actions={<button>Refresh</button>} />);

  expect(screen.getByRole("heading", { level: 1, name: "Docker" })).toBeTruthy();
  expect(screen.getByText("Containers per host")).toBeTruthy();
  expect(screen.getByRole("button", { name: "Refresh" })).toBeTruthy();
});

it("UptimeBars draws one bar per bucket in its tone, oldest first, named by its label", () => {
  render(
    <UptimeBars
      label="Uptime, last 24 h"
      bars={[{ tone: "up", title: "08:00 · 100 %" }, { tone: "down" }, { tone: "maint" }, { tone: "idle", title: "no beats" }]}
    />,
  );

  const bars = screen.getByRole("img", { name: "Uptime, last 24 h" });
  expect([...bars.querySelectorAll("[data-tone]")].map((b) => b.getAttribute("data-tone"))).toEqual(["up", "down", "maint", "idle"]);
  expect(screen.getByText("08:00 · 100 %")).toBeTruthy();
});

it("Switch is a named switch that reports the flipped value, and does nothing while disabled", () => {
  const changes: boolean[] = [];
  const { rerender } = render(<Switch label="Ad-blocking" checked onChange={(v) => changes.push(v)} />);

  const sw = screen.getByRole("switch", { name: "Ad-blocking" });
  expect(sw.getAttribute("aria-checked")).toBe("true");
  fireEvent.click(sw);
  expect(changes).toEqual([false]);

  rerender(<Switch label="Ad-blocking" checked={false} disabled onChange={(v) => changes.push(v)} />);
  fireEvent.click(screen.getByRole("switch", { name: "Ad-blocking" }));
  expect(changes).toEqual([false]);
});
