import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, expect, it } from "vitest";
import { Menu } from "./index";

afterEach(cleanup);

it("Menu opens its items from a labelled button and closes once one is picked", () => {
  const picked: string[] = [];
  render(
    <Menu
      label="Actions for media-stack"
      items={[
        { label: "Stop", onSelect: () => picked.push("Stop") },
        { label: "Start", onSelect: () => picked.push("Start"), disabled: true },
      ]}
    />,
  );

  expect(screen.queryByRole("menu")).toBeNull();
  const button = screen.getByRole("button", { name: "Actions for media-stack" });
  expect(button.getAttribute("aria-haspopup")).toBe("menu");
  fireEvent.click(button);
  expect(button.getAttribute("aria-expanded")).toBe("true");
  expect((screen.getByRole("menuitem", { name: "Start" }) as HTMLButtonElement).disabled).toBe(true);
  fireEvent.click(screen.getByRole("menuitem", { name: "Stop" }));

  expect(picked).toEqual(["Stop"]);
  expect(screen.queryByRole("menu")).toBeNull();
});

it("Menu closes on Escape without picking", () => {
  render(<Menu label="Actions" items={[{ label: "Stop", onSelect: () => {} }]} />);

  fireEvent.click(screen.getByRole("button", { name: "Actions" }));
  fireEvent.keyDown(screen.getByRole("menu"), { key: "Escape" });

  expect(screen.queryByRole("menu")).toBeNull();
});
