// Presence: how @ddash/ui's overlays, and DataTable's strip, animate out as well as in.
import { createContext, useLayoutEffect, useRef, useState, type ReactNode } from "react";

/**
 * Lets an overlay animate out as well as in: wrap the condition that
 * mounts it, <Presence>{x && <Sheet …/>}</Presence>. When the child turns
 * falsy, Presence keeps the last one mounted with its old props, closed
 * (data-state="closed", which the overlays' exit transitions key on) and
 * inert, and unmounts it once its animations finish: at once with none
 * (reduced motion). A child that turns truthy again mid-exit cancels the
 * exit, and one that changes while open replaces it in place.
 */
export function Presence({ children }: { children?: ReactNode }) {
  const { shown, leaving, box } = usePresence<HTMLDivElement>(children);
  if (!shows(shown)) return null;
  return (
    <div ref={box} data-state={leaving ? "closed" : "open"} inert={leaving} className="contents">
      <Leaving value={leaving}>{shown}</Leaving>
    </div>
  );
}

/**
 * Presence's lifecycle for a wrapper the caller draws itself, where a
 * <div> can't go, e.g. DataTable's strip <tr> in a <tbody>: what to show
 * (nothing once it's empty), whether it's leaving, and the ref of the
 * wrapper whose animations it waits for. The caller sets data-state and
 * inert on the wrapper and wraps what it shows in Leaving, as Presence does.
 */
export function usePresence<E extends Element>(children: ReactNode) {
  const open = shows(children);
  const [kept, setKept] = useState<ReactNode>(open ? children : null);
  if (open && children !== kept) setKept(children);
  const leaving = !open && shows(kept);
  const box = useRef<E>(null);
  useLayoutEffect(() => {
    if (!leaving) return;
    // An infinite one (a pulsing Skeleton inside) never finishes.
    const running = (box.current?.getAnimations?.({ subtree: true }) ?? []).filter((a) => a.effect?.getComputedTiming().endTime !== Infinity);
    if (running.length === 0) return setKept(null);
    let cancelled = false;
    void Promise.allSettled(running.map((a) => a.finished)).then(() => cancelled || setKept(null));
    return () => {
      cancelled = true;
    };
  }, [leaving]);
  return { shown: open ? children : kept, leaving, box };
}

export const shows = (node: ReactNode) => node !== null && node !== undefined && node !== false && node !== "";

/** True inside a Presence whose child is leaving: its Overlay hands the focus back and ignores closes. */
export const Leaving = createContext(false);
