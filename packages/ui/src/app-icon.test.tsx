import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, expect, it } from "vitest";
import { AppIcon } from "./index";

afterEach(cleanup);

const img = () => document.querySelector("img");

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
