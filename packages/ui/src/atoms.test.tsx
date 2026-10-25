import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, expect, it } from "vitest";
import { AppIcon, Button, Pill, Skeleton, StatusDot } from "./index";

afterEach(cleanup);

const img = () => document.querySelector("img");

it("StatusDot names its tone for screen readers", () => {
  render(<StatusDot tone="down" />);

  expect(screen.getByRole("img", { name: "down" })).toBeTruthy();
});

it("StatusDot takes a label in place of the tone", () => {
  render(<StatusDot tone="up" label="running" />);

  expect(screen.getByRole("img", { name: "running" })).toBeTruthy();
});

it("StatusDot draws info in the info fill", () => {
  render(<StatusDot tone="info" label="updating" />);

  const dot = screen.getByRole("img", { name: "updating" });
  expect(dot.querySelector(".bg-info")).toBeTruthy();
});

it("Pill draws its tone as a tint behind the tone's text colour", () => {
  render(<Pill tone="down">failing</Pill>);

  const pill = screen.getByText("failing");
  expect(pill.className.split(" ")).toEqual(expect.arrayContaining(["bg-down-tint", "text-down-text"]));
  expect(pill.className.split(" ")).not.toContain("bg-down");
});

it("Pill without a tone is idle: muted, never a status colour", () => {
  render(<Pill>draft</Pill>);

  expect(screen.getByText("draft").className.split(" ")).toEqual(expect.arrayContaining(["bg-muted", "text-muted-foreground"]));
});

it("Pill can lead with a dot in its own colour, hidden from screen readers", () => {
  render(
    <Pill tone="info" dot>
      updating
    </Pill>,
  );

  const pill = screen.getByText("updating");
  expect(pill.className).toContain("text-info-text");
  const dot = pill.querySelector("[aria-hidden]");
  expect(dot?.className).toContain("bg-current");
  expect(pill.textContent).toBe("updating");
});

it("Button with an icon leads with it as decoration, named by its text", () => {
  render(<Button icon="plus">Add Widget</Button>);

  const button = screen.getByRole("button", { name: "Add Widget" });
  expect(button.querySelector("svg")?.getAttribute("aria-hidden")).toBe("true");
});

it("Button danger is the down tint with down text, never a solid fill", () => {
  render(<Button variant="danger">Uninstall</Button>);

  const classes = screen.getByRole("button", { name: "Uninstall" }).className.split(" ");
  expect(classes).toEqual(expect.arrayContaining(["bg-down-tint", "text-down-text"]));
  expect(classes).not.toContain("bg-down");
  expect(classes).not.toContain("text-primary-foreground");
});

it("Skeleton tells screen readers something is loading and draws the lines it is asked for", () => {
  render(<Skeleton lines={3} />);

  const skeleton = screen.getByRole("status", { name: "Loading" });
  expect(skeleton.getAttribute("aria-busy")).toBe("true");
  expect(skeleton.querySelectorAll("[data-skeleton-line]")).toHaveLength(3);
});

it("Skeleton with a height draws one block from the fixed scale", () => {
  render(<Skeleton height="lg" />);

  const skeleton = screen.getByRole("status", { name: "Loading" });
  expect(skeleton.querySelectorAll("[data-skeleton-line]")).toHaveLength(0);
  expect(skeleton.getAttribute("data-height")).toBe("lg");
});

it("AppIcon draws the dashboard-icons icon of its name", () => {
  render(<AppIcon name="Home Assistant" url="https://ha.home.example" />);

  expect(img()?.getAttribute("src")).toBe("https://cdn.jsdelivr.net/gh/homarr-labs/dashboard-icons/svg/home-assistant.svg");
});

it("AppIcon prefers the icon it is given over its name", () => {
  render(<AppIcon name="Media" icon="jellyfin" url="https://media.home.example" />);

  expect(img()?.getAttribute("src")).toBe("https://cdn.jsdelivr.net/gh/homarr-labs/dashboard-icons/svg/jellyfin.svg");
});

it("AppIcon falls back to the PNG, for the icons upstream has no SVG of", () => {
  render(<AppIcon name="3x-ui" icon="3xui" />);

  fireEvent.error(img()!);
  expect(img()?.getAttribute("src")).toBe("https://cdn.jsdelivr.net/gh/homarr-labs/dashboard-icons/png/3xui.png");
});

it("AppIcon falls back to the site's favicon, then to the name's first letter", () => {
  render(<AppIcon name="My Printer" url="http://printer.home.example:8080/ui/" />);

  fireEvent.error(img()!); // no SVG
  fireEvent.error(img()!); // no PNG
  expect(img()?.getAttribute("src")).toBe("http://printer.home.example:8080/favicon.ico");

  fireEvent.error(img()!);
  expect(img()).toBeNull();
  expect(screen.getByText("M")).toBeTruthy();
});
