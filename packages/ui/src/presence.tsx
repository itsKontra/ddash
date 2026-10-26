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
  const { present, wrapper, content } = usePresence<HTMLDivElement>(children);
  if (!present) return null;
  return (
    <div {...wrapper} className="contents">
      {content}
    </div>
  );
}

/**
 * Presence's lifecycle for a wrapper the caller draws itself, where a
 * <div> can't go, e.g. DataTable's strip <tr> in a <tbody>. The caller
 * draws nothing while present is false, spreads wrapper (its ref,
 * data-state and inert) on its wrapper and puts content inside it, as
 * Presence does.
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
  return {
    /** Whether anything is mounted: open, or still leaving. */
    present: open || leaving,
    leaving,
    /** The wrapper's props: the ref whose animations an exit waits for, its state and inert while leaving. */
    wrapper: { ref: box, "data-state": leaving ? "closed" : "open", inert: leaving },
    /** What the wrapper holds, told whether it is leaving. */
    content: <Leaving value={leaving}>{open ? children : kept}</Leaving>,
  };
}

const shows = (node: ReactNode) => node !== null && node !== undefined && node !== false && node !== "";

/** True inside a Presence whose child is leaving: its Overlay hands the focus back and ignores closes. */
export const Leaving = createContext(false);
