import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, expect, it } from "vitest";
import { Switch } from "./index";

afterEach(cleanup);

it("Switch is a named switch reporting its state and asking for the other", () => {
  const changes: boolean[] = [];
  render(<Switch checked={false} label="Direct TV" onChange={(v) => changes.push(v)} />);

  const s = screen.getByRole("switch", { name: "Direct TV" });
  expect(s.getAttribute("aria-checked")).toBe("false");
  s.click();
  expect(changes).toEqual([true]);
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
