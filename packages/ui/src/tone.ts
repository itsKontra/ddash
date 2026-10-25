// Tone, the status every component draws, and the class helpers the families share.

/** A status: up, down, warn (attention), maint (Maintenance) or idle (unknown). */
export type Tone = "up" | "down" | "warn" | "maint" | "idle";

/** Joins class names, skipping falsy ones. */
export const cx = (...c: (string | false | null | undefined)[]) => c.filter(Boolean).join(" ");

export const toneBg: Record<Tone, string> = {
  up: "bg-up",
  down: "bg-down",
  warn: "bg-warn",
  maint: "bg-maint",
  idle: "bg-muted-foreground",
};

export const toneText: Record<Tone, string> = {
  up: "text-up",
  down: "text-down",
  warn: "text-warn",
  maint: "text-maint",
  idle: "text-muted-foreground",
};

/** A tone as an SVG fill. */
export const toneFill: Record<Tone, string> = {
  up: "fill-up",
  down: "fill-down",
  warn: "fill-warn",
  maint: "fill-maint",
  idle: "fill-muted",
};
