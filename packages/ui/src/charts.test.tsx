import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, expect, it } from "vitest";
import { Bars, Donut, Meter, Ring, Sparkline, UptimeBars } from "./index";

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

it("Ring is a meter named by its label, with the ratio as a percentage and its value as text", () => {
  render(<Ring ratio={0.75} value="9 / 12" label="Widgets up" />);

  const ring = screen.getByRole("meter", { name: "Widgets up" });
  expect(ring.getAttribute("aria-valuenow")).toBe("75");
  expect(ring.getAttribute("aria-valuemin")).toBe("0");
  expect(ring.getAttribute("aria-valuemax")).toBe("100");
  expect(ring.getAttribute("aria-valuetext")).toBe("9 / 12");
  expect(ring.textContent).toContain("9 / 12");
});

const legendOf = (label: string) =>
  [...screen.getByRole("list", { name: `${label} legend` }).querySelectorAll("li")].map((li) => ({
    text: li.textContent,
    colour: li.querySelector("[data-colour]")?.getAttribute("data-colour"),
  }));

it("Donut is an image named by its label, with a legend carrying each segment's value in chart colours", () => {
  render(
    <Donut
      label="Containers by host"
      total="7"
      segments={[
        { label: "nas", value: 4 },
        { label: "pi", value: 3 },
      ]}
    />,
  );

  const img = screen.getByRole("img", { name: "Containers by host" });
  expect([...img.querySelectorAll("circle[data-colour]")].map((c) => c.getAttribute("class"))).toEqual(["stroke-chart-1", "stroke-chart-2"]);
  expect(legendOf("Containers by host")).toEqual([
    { text: "nas4", colour: "chart-1" },
    { text: "pi3", colour: "chart-2" },
  ]);
});

it("Donut folds the 7th and later categories into Other, in muted, after the six chart colours", () => {
  const hosts = ["a", "b", "c", "d", "e", "f", "g", "h"].map((label) => ({ label, value: 1 }));
  render(<Donut label="By host" total="8" segments={hosts} />);

  expect(legendOf("By host")).toEqual([
    { text: "a1", colour: "chart-1" },
    { text: "b1", colour: "chart-2" },
    { text: "c1", colour: "chart-3" },
    { text: "d1", colour: "chart-4" },
    { text: "e1", colour: "chart-5" },
    { text: "f1", colour: "chart-6" },
    { text: "Other2", colour: "muted" },
  ]);
});

it("Donut draws segments with a tone as status, in tone fills, and only the rest as categories", () => {
  render(
    <Donut
      label="Checks"
      total="9"
      segments={[
        { label: "Up", value: 6, tone: "up" },
        { label: "Down", value: 1, tone: "down" },
        { label: "Other checks", value: 2 },
      ]}
    />,
  );

  expect(legendOf("Checks")).toEqual([
    { text: "Up6", colour: "up" },
    { text: "Down1", colour: "down" },
    { text: "Other checks2", colour: "chart-1" },
  ]);
  const slices = screen.getByRole("img", { name: "Checks" }).querySelectorAll("circle[data-colour]");
  expect([...slices].map((c) => c.getAttribute("class"))).toEqual(["stroke-up", "stroke-down", "stroke-chart-1"]);
});

const barsOf = (label: string) =>
  [...screen.getByRole("img", { name: label }).querySelectorAll("rect[data-colour]")].map((r) => ({
    x: r.getAttribute("x"),
    width: r.getAttribute("width"),
    colour: r.getAttribute("data-colour"),
  }));

it("Bars draws one horizontal bar per item against the largest, with a legend carrying the values", () => {
  render(
    <Bars
      label="Top blocked domains"
      items={[
        { label: "ads.example", value: 40 },
        { label: "track.example", value: 10 },
      ]}
    />,
  );

  expect(barsOf("Top blocked domains")).toEqual([
    { x: "0", width: "100", colour: "chart-1" },
    { x: "0", width: "25", colour: "chart-2" },
  ]);
  expect(legendOf("Top blocked domains")).toEqual([
    { text: "ads.example40", colour: "chart-1" },
    { text: "track.example10", colour: "chart-2" },
  ]);
});

it("stacked Bars draws the items end to end in one bar, as shares of their sum, folding past six into Other", () => {
  const items = [{ label: "Allowed", value: 6, tone: "up" as const }, ...["a", "b", "c", "d", "e", "f", "g"].map((label) => ({ label, value: 2 }))];
  render(<Bars stacked label="Queries" items={items} />);

  const bars = barsOf("Queries");
  expect(bars.map((b) => b.colour)).toEqual(["up", "chart-1", "chart-2", "chart-3", "chart-4", "chart-5", "chart-6", "muted"]);
  expect(bars.map((b) => [b.x, b.width])).toEqual([
    ["0", "30"],
    ["30", "10"],
    ["40", "10"],
    ["50", "10"],
    ["60", "10"],
    ["70", "10"],
    ["80", "10"],
    ["90", "10"],
  ]);
  expect(legendOf("Queries").at(-1)).toEqual({ text: "Other2", colour: "muted" });
});

it("Meter is a meter named by its label, filled to its ratio in its tone and clamped to 0..100", () => {
  render(
    <>
      <Meter label="Disk" ratio={0.42} tone="warn" />
      <Meter label="Over" ratio={1.7} tone="down" />
    </>,
  );

  const disk = screen.getByRole("meter", { name: "Disk" });
  expect(disk.getAttribute("aria-valuenow")).toBe("42");
  expect(disk.querySelector("[data-colour]")?.getAttribute("data-colour")).toBe("warn");
  expect(screen.getByRole("meter", { name: "Over" }).getAttribute("aria-valuenow")).toBe("100");
});
