import { expect, it } from "vitest";
import { toneFill, toneMark, toneStroke, type Tone } from "./tone";

const tones: Tone[] = ["up", "down", "warn", "maint", "info", "idle"];

it("a chart mark draws each tone in one colour, whether filled, stroked or a swatch", () => {
  for (const tone of tones) {
    const colour = toneFill[tone].replace(/^fill-/, "");
    expect(toneStroke[tone]).toBe(`stroke-${colour}`);
    expect(toneMark[tone]).toBe(`bg-${colour}`);
  }
  expect(toneFill.idle).toBe("fill-muted");
});
