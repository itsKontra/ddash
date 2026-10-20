// @vitest-environment node
/// <reference types="node" />
import { readFileSync } from "node:fs";
import { expect, it } from "vitest";

const css = readFileSync(new URL("./theme.css", import.meta.url), "utf8");

// The custom properties a block of css sets, by name.
function block(selector: string): Map<string, string> {
  const start = css.indexOf(`${selector} {`);
  expect(start, `no ${selector} block`).toBeGreaterThanOrEqual(0);
  const body = css.slice(start, css.indexOf("}", start));
  return new Map([...body.matchAll(/--([\w-]+):\s*([^;]+);/g)].map((m) => [m[1], m[2].trim()]));
}

// The spec's Theme tokens.
const colours = ["background", "foreground", "card", "muted", "muted-foreground", "border", "primary", "primary-foreground", "up", "down", "warn", "maint"];

it("defines every Theme token for light", () => {
  const light = block(":root");
  for (const token of [...colours, "radius", "font"]) expect(light.get(token), token).toBeTruthy();
});

it("defines every colour token again for dark", () => {
  const light = block(":root");
  const dark = block(":root.dark");
  for (const token of colours) {
    expect(dark.get(token), token).toBeTruthy();
  }
  expect(dark.get("background")).not.toBe(light.get("background"));
});
