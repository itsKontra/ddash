import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, expect, it } from "vitest";
import { Sparkline, UptimeBars } from "./index";

afterEach(cleanup);

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
