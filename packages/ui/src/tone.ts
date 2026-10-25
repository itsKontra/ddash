// Tone, the status every component draws, and the class helpers the families share.

/** A status: up, down, warn (attention), maint (Maintenance), info (neutral news) or idle (unknown). */
export type Tone = "up" | "down" | "warn" | "maint" | "info" | "idle";

/** Joins class names, skipping falsy ones. */
export const cx = (...c: (string | false | null | undefined)[]) => c.filter(Boolean).join(" ");

export const toneBg: Record<Tone, string> = {
  up: "bg-up",
  down: "bg-down",
  warn: "bg-warn",
  maint: "bg-maint",
  info: "bg-info",
  idle: "bg-muted-foreground",
};

export const toneText: Record<Tone, string> = {
  up: "text-up-text",
  down: "text-down-text",
  warn: "text-warn-text",
  maint: "text-maint-text",
  info: "text-info-text",
  idle: "text-muted-foreground",
};

/** A tone as an SVG fill. */
export const toneFill: Record<Tone, string> = {
  up: "fill-up",
  down: "fill-down",
  warn: "fill-warn",
  maint: "fill-maint",
  info: "fill-info",
  idle: "fill-muted",
};

/** A tone as a surface under text: its 12% tint with its text colour. Idle is muted. */
export const toneTint: Record<Tone, string> = {
  up: "bg-up-tint text-up-text",
  down: "bg-down-tint text-down-text",
  warn: "bg-warn-tint text-warn-text",
  maint: "bg-maint-tint text-maint-text",
  info: "bg-info-tint text-info-text",
  idle: "bg-muted text-muted-foreground",
};
