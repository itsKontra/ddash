import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, expect, it } from "vitest";
import { Bars, ColumnChart, Donut, LineChart, Meter, meterTone, Ring, Sparkline, Summary, UptimeBars, type LineSeries } from "./index";

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

it("meterTone is up below 75 %, warn from 75 % and down from 90 %", () => {
  expect([0, 0.749, 0.75, 0.899, 0.9, 1].map(meterTone)).toEqual(["up", "up", "warn", "warn", "down", "down"]);
});

it("Summary draws its headline, its line's parts, its meters toned by use and its rows", () => {
  render(
    <Summary
      headline="12 of 17 guests running"
      line={[{ text: "21 containers" }, { text: "1 exited", tone: "down" }]}
      meters={[
        { label: "CPU", ratio: 0.02, value: "2.2 % of 16" },
        { label: "Memory", ratio: 0.8, value: "51 GiB of 64 GiB" },
      ]}
      rows={[{ tone: "down", label: "grafana", value: "disk 91 %" }]}
    />,
  );

  expect(screen.getByText("12 of 17 guests running")).toBeTruthy();
  expect(screen.getByText("1 exited").className).toContain("text-down-text");
  expect(screen.getByText("21 containers").parentElement?.textContent).toBe("21 containers · 1 exited");
  const memory = screen.getByRole("meter", { name: "Memory" });
  expect(memory.querySelector("[data-colour]")?.getAttribute("data-colour")).toBe("warn");
  expect(memory.parentElement?.parentElement?.textContent).toBe("Memory51 GiB of 64 GiB");
  expect(screen.getByRole("listitem").textContent).toBe("grafana" + "disk 91 %");
});

it("Summary leaves out the parts it isn't given", () => {
  render(<Summary headline="5 of 6 containers running" />);

  expect(screen.queryByRole("meter")).toBeNull();
  expect(screen.queryByRole("list")).toBeNull();
});

const at = (h: number, m = 0) => new Date(2026, 10, 14, h, m);

it("LineChart draws each series between from and to, breaking a line at a gap, with a legend", () => {
  render(
    <LineChart
      label="Today's power"
      from={at(0)}
      to={at(24)}
      times={[at(10), at(10, 5), at(10, 10), at(12), at(12, 5)]}
      series={[
        { label: "Solar", values: [1, 2, 3, 4, 5], colour: "chart-4", shape: "area" },
        { label: "Grid", values: [-1, null, -1, -1, -1], colour: "chart-1" },
      ]}
      format={(v) => `${v} kW`}
    />,
  );

  const img = screen.getByRole("img", { name: "Today's power" });
  const runs = (name: string) => img.querySelectorAll(`[data-series="${name}"] polyline`).length;
  // Solar breaks only at the gap from 10:10 to 12:00, Grid also at its missing value.
  expect([runs("Solar"), runs("Grid")]).toEqual([2, 3]);
  expect(img.querySelectorAll('[data-series="Solar"] polygon')).toHaveLength(2);
  expect(legendOf("Today's power")).toEqual([
    { text: "Solar", colour: "chart-4" },
    { text: "Grid", colour: "chart-1" },
  ]);
  expect(img.textContent).toContain("-2 kW");
});

it("ColumnChart stacks positive values up and negative ones down, and a column selects", () => {
  const picked: number[] = [];
  render(
    <ColumnChart
      label="Energy by day"
      categories={["1", "2"]}
      series={[
        { label: "Solar", values: [4, 0], colour: "chart-4" },
        { label: "Grid", values: [2, 6], colour: "chart-1" },
        { label: "Export", values: [-3, 0], colour: "chart-3" },
      ]}
      format={(v) => `${v} kWh`}
      onSelect={(i) => picked.push(i)}
      selectLabel={(i) => `Show day ${i + 1}`}
    />,
  );

  const img = screen.getByRole("img", { name: "Energy by day" });
  const first = [...img.querySelectorAll('[data-column="1"] rect[data-colour]')].map((r) => r.getAttribute("data-colour"));
  expect(first).toEqual(["chart-4", "chart-1", "chart-3"]);
  // A zero draws nothing.
  expect(img.querySelectorAll('[data-column="2"] rect[data-colour]')).toHaveLength(1);
  screen.getByRole("button", { name: "Show day 2" }).click();
  expect(picked).toEqual([1]);
});

it("LineChart reads out the point under the pointer with valueFormat", () => {
  render(
    <LineChart
      label="State of charge"
      from={at(0)}
      to={at(24)}
      times={[at(12), at(12, 5)]}
      series={[{ label: "State of charge", values: [65.23333333, 66], colour: "chart-3" }]}
      format={(v) => `${v} %`}
      valueFormat={(v) => `${Math.round(v)} %`}
    />,
  );

  // The box is 640 px wide before layout: 12:00 lies half-way along the plot.
  fireEvent.pointerMove(screen.getByRole("img", { name: "State of charge" }), { clientX: 48 + (640 - 58) / 2 });

  expect(screen.getByRole("status").textContent).toContain("State of charge65 %");
});

const power = [
  { label: "Solar", values: [1, 2], colour: "chart-4" as const, shape: "area" as const },
  { label: "Used", values: [1, 1], colour: "chart-2" as const },
  { label: "Battery", values: [-8, 8], colour: "chart-3" as const },
];
const powerChart = (series: LineSeries[] = power, hidden?: Pick<Parameters<typeof LineChart>[0], "hidden" | "onHiddenChange">) => (
  <LineChart label="Power" from={at(0)} to={at(24)} times={[at(12), at(12, 5)]} series={series} format={(v) => `${v} kW`} {...hidden} />
);
const entry = (name: string) => screen.getByRole("button", { name });
const pressed = (...names: string[]) => names.map((n) => entry(n).getAttribute("aria-pressed"));
/** The y-axis's labels, bottom to top. */
const yAxis = (img: HTMLElement) => [...img.querySelectorAll('text[text-anchor="end"]')].map((t) => t.textContent);

it("LineChart hides a series when its legend entry is pressed, and shows it again on the next", () => {
  render(powerChart());
  const img = screen.getByRole("img", { name: "Power" });

  expect(pressed("Used")).toEqual(["true"]);
  fireEvent.click(entry("Used"));

  expect(pressed("Used")).toEqual(["false"]);
  expect(img.querySelector('[data-series="Used"]')).toBeNull();
  // It stays in the legend, in its place, struck through and hollow.
  expect(legendOf("Power").map((l) => l.text)).toEqual(["Solar", "Used", "Battery"]);
  expect(entry("Used").className).toContain("line-through");
  expect(entry("Used").querySelector("[data-colour]")?.className).toContain("bg-transparent");
  fireEvent.pointerMove(img, { clientX: 48 + (640 - 58) / 2 });
  expect(screen.getByRole("status").textContent).not.toContain("Used");
  expect(img.querySelectorAll("circle")).toHaveLength(2);

  fireEvent.click(entry("Used"));
  expect(pressed("Used")).toEqual(["true"]);
  expect(img.querySelector('[data-series="Used"]')).not.toBeNull();
  expect(screen.getByRole("status").textContent).toContain("Used");
});

it("LineChart scales its y-axis to the series shown", () => {
  render(powerChart());
  const img = screen.getByRole("img", { name: "Power" });
  expect(yAxis(img)).toEqual(["-10 kW", "-5 kW", "0 kW", "5 kW", "10 kW"]);

  fireEvent.click(entry("Battery"));

  expect(yAxis(img)).toEqual(["0 kW", "0.5 kW", "1 kW", "1.5 kW", "2 kW"]);
});

it("ColumnChart stacks and scales only the series shown, and reads out only those", () => {
  render(
    <ColumnChart
      label="Energy by day"
      categories={["1", "2"]}
      series={[
        { label: "Solar", values: [4, 1], colour: "chart-4" },
        { label: "Grid", values: [20, 6], colour: "chart-1" },
        { label: "Export", values: [-3, 0], colour: "chart-3" },
      ]}
      format={(v) => `${v} kWh`}
    />,
  );
  const img = screen.getByRole("img", { name: "Energy by day" });
  expect(yAxis(img)).toEqual(["-10 kWh", "0 kWh", "10 kWh", "20 kWh", "30 kWh"]);

  fireEvent.click(entry("Grid"));

  const first = [...img.querySelectorAll('[data-column="1"] rect[data-colour]')].map((r) => r.getAttribute("data-colour"));
  expect(first).toEqual(["chart-4", "chart-3"]);
  expect(yAxis(img)).toEqual(["-4 kWh", "-2 kWh", "0 kWh", "2 kWh", "4 kWh"]);
  fireEvent.pointerEnter(img.querySelector('[data-column="1"]')!);
  expect(screen.getByRole("status").textContent).not.toContain("Grid");
});

it("keeps the last series shown: its legend entry does nothing", () => {
  render(powerChart());

  fireEvent.click(entry("Solar"));
  fireEvent.click(entry("Battery"));
  expect(entry("Used").getAttribute("aria-disabled")).toBe("true");
  fireEvent.click(entry("Used"));

  expect(pressed("Used")).toEqual(["true"]);
  expect(screen.getByRole("img", { name: "Power" }).querySelector('[data-series="Used"]')).not.toBeNull();
  // Showing another lets it go again.
  fireEvent.click(entry("Solar"));
  expect(entry("Used").getAttribute("aria-disabled")).toBeNull();
});

it("makes legend entries native buttons, reached with Tab and pressed with Enter or Space", () => {
  render(powerChart());

  const used = entry("Used");
  // A native button in the tab order: the browser presses it on Enter and Space.
  expect([used.tagName, used.getAttribute("type"), used.tabIndex]).toEqual(["BUTTON", "button", 0]);
  used.focus();
  expect(document.activeElement).toBe(used);
});

it("doesn't make a single series' legend clickable", () => {
  render(powerChart([power[0]]));

  expect(screen.queryByRole("button")).toBeNull();
  expect(legendOf("Power")).toEqual([{ text: "Solar", colour: "chart-4" }]);
});

it("keeps the series hidden by label when the data changes, and hides none when it would hide all", () => {
  const { rerender } = render(powerChart());
  fireEvent.click(entry("Battery"));

  rerender(powerChart(power.map((s) => ({ ...s, values: [...s.values].reverse() }))));
  expect(pressed("Battery")).toEqual(["false"]);

  // Data without Battery, then with it again: still hidden.
  rerender(powerChart(power.slice(0, 2)));
  fireEvent.click(entry("Used"));
  rerender(powerChart());
  expect(pressed("Solar", "Used", "Battery")).toEqual(["true", "false", "false"]);

  // Data with only the hidden ones shows them all.
  rerender(powerChart(power.slice(1)));
  expect(pressed("Used", "Battery")).toEqual(["true", "true"]);
  expect(screen.getByRole("img", { name: "Power" }).querySelectorAll("[data-series]")).toHaveLength(2);
});

it("lets a Page hold the hidden series, keeping labels the series don't have", () => {
  const changes: string[][] = [];
  render(powerChart(power, { hidden: ["Gone", "Used"], onHiddenChange: (h) => changes.push(h) }));

  expect(pressed("Solar", "Used")).toEqual(["true", "false"]);
  fireEvent.click(entry("Solar"));

  expect(changes).toEqual([["Gone", "Solar", "Used"]]);
});
