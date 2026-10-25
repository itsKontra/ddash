import { act, cleanup, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { Icon } from "./index";

// The core's icons (core/icons), as GET /api/icons serves them.
const served: Record<string, unknown> = {
  plus: [
    ["path", { d: "M5 12h14" }],
    ["path", { d: "M12 5v14" }],
  ],
  minus: [["path", { d: "M5 12h14" }]],
  server: [["rect", { width: "20", height: "8", x: "2", y: "2", rx: "2", ry: "2" }]],
  puzzle: [["path", { d: "M15.39 4.39z" }]],
};

let requests: string[];

beforeEach(() => {
  requests = [];
  document.head.innerHTML = `<meta name="ddash-release" content="v1.2.3">`;
  vi.stubGlobal("fetch", async (url: string) => {
    requests.push(url);
    const names = new URL(url, "http://ddash").searchParams.get("names")!.split(",");
    return Response.json(Object.fromEntries(names.map((n) => [n, served[n] ?? served.puzzle])));
  });
});

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

/** Lets the batched request go out and its response arrive. */
const settle = () => act(() => new Promise((r) => setTimeout(r, 0)));

const svg = () => document.querySelector("svg")!;

it("Icon draws the lucide icon the core serves as an inline svg in currentColor", async () => {
  render(<Icon name="plus" />);
  await settle();

  expect(svg().getAttribute("stroke")).toBe("currentColor");
  expect([...svg().querySelectorAll("path")].map((p) => p.getAttribute("d"))).toEqual(["M5 12h14", "M12 5v14"]);
});

it("Icon asks the core for the running Release's icons", async () => {
  render(<Icon name="server" />);
  await settle();

  expect(requests).toEqual(["/api/icons?names=server&release=v1.2.3"]);
});

it("Icon is 16px by default, 14px small and 20px large", async () => {
  const { unmount } = render(<Icon name="plus" />);
  expect(svg().getAttribute("width")).toBe("16");
  unmount();
  render(
    <>
      <Icon name="plus" size="sm" />
      <Icon name="plus" size="lg" />
    </>,
  );

  expect([...document.querySelectorAll("svg")].map((s) => [s.getAttribute("width"), s.getAttribute("height")])).toEqual([
    ["14", "14"],
    ["20", "20"],
  ]);
});

it("Icon with a label is an image of that name; without one it is hidden from screen readers", () => {
  render(
    <>
      <Icon name="plus" label="Add" />
      <Icon name="minus" />
    </>,
  );

  expect(screen.getByRole("img", { name: "Add" })).toBeTruthy();
  expect(document.querySelectorAll("svg")[1]!.getAttribute("aria-hidden")).toBe("true");
});

it("icons drawn in one render go out as one request, and each is fetched once per page load", async () => {
  render(
    <>
      <Icon name="a-arrow-down" />
      <Icon name="a-arrow-up" />
      <Icon name="a-arrow-up" />
    </>,
  );
  await settle();
  render(<Icon name="a-arrow-down" />);
  await settle();

  expect(requests).toEqual(["/api/icons?names=a-arrow-down,a-arrow-up&release=v1.2.3"]);
});

it("a loading Icon reserves its box", () => {
  render(<Icon name="activity" size="lg" />);

  expect(requests).toEqual([]);
  expect([svg().getAttribute("width"), svg().getAttribute("height")]).toEqual(["20", "20"]);
});

it("an unknown name draws the puzzle", async () => {
  render(<Icon name={"no-such-icon" as "plus"} />);
  await settle();

  expect(svg().querySelector("path")?.getAttribute("d")).toBe("M15.39 4.39z");
});

it("Icon's name is a lucide name", () => {
  // @ts-expect-error a typo is a type error
  render(<Icon name="plsu" />);
});
