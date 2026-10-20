import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, expect, it } from "vitest";
import { Badge, PageHeader, StatusDot, UptimeBars } from "./index";

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
