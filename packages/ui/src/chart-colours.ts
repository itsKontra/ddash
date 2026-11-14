// The colours charts draw in, shared by the charts family; not exported
// from @ddash/ui itself.
import { toneFill, toneMark, toneStroke, type Tone } from "./tone";

export const chartColours = ["chart-1", "chart-2", "chart-3", "chart-4", "chart-5", "chart-6"] as const;
export type Colour = (typeof chartColours)[number] | Tone | "muted";

// Literal class names, so the shell's Tailwind build finds them. A tone
// draws as its chart mark (tone.ts); "Other" is muted, like idle.
export const colourStroke: Record<Colour, string> = {
  "chart-1": "stroke-chart-1",
  "chart-2": "stroke-chart-2",
  "chart-3": "stroke-chart-3",
  "chart-4": "stroke-chart-4",
  "chart-5": "stroke-chart-5",
  "chart-6": "stroke-chart-6",
  ...toneStroke,
  muted: toneStroke.idle,
};
export const colourFill: Record<Colour, string> = {
  "chart-1": "fill-chart-1",
  "chart-2": "fill-chart-2",
  "chart-3": "fill-chart-3",
  "chart-4": "fill-chart-4",
  "chart-5": "fill-chart-5",
  "chart-6": "fill-chart-6",
  ...toneFill,
  muted: toneFill.idle,
};
export const colourBg: Record<Colour, string> = {
  "chart-1": "bg-chart-1",
  "chart-2": "bg-chart-2",
  "chart-3": "bg-chart-3",
  "chart-4": "bg-chart-4",
  "chart-5": "bg-chart-5",
  "chart-6": "bg-chart-6",
  ...toneMark,
  muted: toneMark.idle,
};
