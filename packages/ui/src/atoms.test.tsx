import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, expect, it } from "vitest";
import { AppIcon, Badge, StatusDot } from "./index";

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

it("Badge shows its content", () => {
  render(<Badge tone="warn">3 new</Badge>);

  expect(screen.getByText("3 new")).toBeTruthy();
});

it("AppIcon draws the dashboard-icons icon of its name", () => {
  render(<AppIcon name="Home Assistant" url="https://ha.home.example" />);

  expect(img()?.getAttribute("src")).toBe("https://cdn.jsdelivr.net/gh/homarr-labs/dashboard-icons/svg/home-assistant.svg");
});

it("AppIcon prefers the icon it is given over its name", () => {
  render(<AppIcon name="Media" icon="jellyfin" url="https://media.home.example" />);

  expect(img()?.getAttribute("src")).toBe("https://cdn.jsdelivr.net/gh/homarr-labs/dashboard-icons/svg/jellyfin.svg");
});

it("AppIcon falls back to the site's favicon, then to the name's first letter", () => {
  render(<AppIcon name="My Printer" url="http://printer.home.example:8080/ui/" />);

  fireEvent.error(img()!);
  expect(img()?.getAttribute("src")).toBe("http://printer.home.example:8080/favicon.ico");

  fireEvent.error(img()!);
  expect(img()).toBeNull();
  expect(screen.getByText("M")).toBeTruthy();
});
