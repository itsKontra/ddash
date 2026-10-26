// Motion the shell shares with @ddash/ui's overlays. The shell imports it
// as @ddash/ui/motion; a Plugin can't (the style lint rejects every
// @ddash/ui/ subpath), so it is no way around Sheet and Dialog.

/**
 * A backdrop's fade and blur (tokens.css), the overlays' and the shell's
 * phone Drawer's: it enters from (starting:) and leaves to
 * (in-data-[state=closed]:, set by Presence) none of either.
 */
export const backdropMotion =
  "starting:bg-foreground/0 starting:backdrop-blur-none in-data-[state=closed]:bg-foreground/0 in-data-[state=closed]:backdrop-blur-none motion-safe:transition-[background-color,backdrop-filter] motion-safe:duration-(--motion-fade)";
