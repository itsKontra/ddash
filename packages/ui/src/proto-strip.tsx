// PROTOTYPE (wayfinder: "What does a row's inline action strip look like and
// hold, for Devices and Workloads?"): three variants of the row arrow and
// its inline action strip, switchable with ?variant=1|2|3 and the floating
// bar. Throwaway; lives on prototype/row-action-strip only.
//
//   1 Toolbar   several strips open at once; Buttons and Switches; Rename and
//               Adopt keep their Sheets; a disruptive Action confirms in the
//               ConfirmDialog; busy shows as a pill beside the arrow.
//   2 Inline    one strip at a time; Rename and Adopt edit in the strip; a
//               disruptive Action asks in the strip itself; busy shows in the
//               Status column; the arrow scrolls away on a phone.
//   3 Command   one strip at a time; a summary line plus compact Segmented
//               controls; Rename edits in the strip, Adopt keeps its Sheet;
//               a disruptive Action is a two-step button; busy spins the arrow.
//   4 Chosen    the Operator's pick: 3, but with 1's arrow, one toggle
//               button per setting ("Tunnelled", "Ads blocked"), 1's
//               ConfirmDialog and 1's busy pill beside the arrow.
import { useEffect, useRef, useState, useSyncExternalStore, type ReactNode } from "react";
import { Button } from "./atoms";
import { Icon } from "./icon";
import { cx } from "./tone";

export type StripVariant = "1" | "2" | "3" | "4";
export const stripVariants: Record<StripVariant, string> = { "1": "Toolbar", "2": "Inline", "3": "Command", "4": "Chosen" };
const order = Object.keys(stripVariants) as StripVariant[];

const key = "ddash.prototype.strip-variant";
const listeners = new Set<() => void>();
const valid = (v: string | null): v is StripVariant => v === "1" || v === "2" || v === "3" || v === "4";
function read(): StripVariant {
  const q = new URLSearchParams(window.location.search).get("variant");
  if (valid(q)) return q;
  try {
    const s = localStorage.getItem(key);
    if (valid(s)) return s;
  } catch {}
  return "1";
}
let current: StripVariant = typeof window === "undefined" ? "1" : read();

function setVariant(v: StripVariant) {
  current = v;
  try {
    localStorage.setItem(key, v);
  } catch {}
  const url = new URL(window.location.href);
  url.searchParams.set("variant", v);
  window.history.replaceState(window.history.state, "", url);
  listeners.forEach((l) => l());
}

/** The strip variant in use, shared by the shell and every Plugin. */
export function useStripVariant(): StripVariant {
  return useSyncExternalStore(
    (l) => {
      listeners.add(l);
      return () => listeners.delete(l);
    },
    () => current,
  );
}

/** The floating bar cycling the variants; ← and → too, outside inputs and radio or tab groups. */
export function StripSwitcher() {
  const v = useStripVariant();
  const step = (d: number) => setVariant(order[(order.indexOf(v) + d + order.length) % order.length]);
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key !== "ArrowLeft" && e.key !== "ArrowRight") return;
      const t = e.target as HTMLElement | null;
      if (t?.closest("input, textarea, select, [contenteditable], [role=radio], [role=tab], [role=menu]")) return;
      step(e.key === "ArrowLeft" ? -1 : 1);
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  });
  return (
    <div className="fixed bottom-4 left-1/2 z-50 flex -translate-x-1/2 items-center gap-1 rounded-full bg-foreground px-2 py-1.5 text-sm text-background shadow-lg">
      <button type="button" aria-label="Previous variant" onClick={() => step(-1)} className="cursor-pointer rounded-full p-1 hover:opacity-70">
        <Icon name="chevron-left" />
      </button>
      <span className="min-w-36 text-center font-semibold tabular-nums">
        {v} · {stripVariants[v]}
      </span>
      <button type="button" aria-label="Next variant" onClick={() => step(1)} className="cursor-pointer rounded-full p-1 hover:opacity-70">
        <Icon name="chevron-right" />
      </button>
    </div>
  );
}

/** The arrow at a row's end that opens and closes its strip, in the variant's look. */
export function RowArrow({ open, onToggle, label, busy }: { open: boolean; onToggle: () => void; label: string; busy?: boolean }) {
  const v = useStripVariant();
  return (
    <button
      type="button"
      aria-expanded={open}
      aria-label={label}
      onClick={onToggle}
      className={cx(
        "inline-grid cursor-pointer place-items-center transition-colors",
        (v === "1" || v === "4") && "size-7 rounded-ui-sm text-muted-foreground hover:bg-muted hover:text-foreground",
        v === "2" && "size-7 rounded-full text-muted-foreground hover:text-foreground",
        v === "3" && cx("size-7 rounded-ui-sm border", open ? "border-primary bg-primary-tint text-primary" : "border-border bg-card hover:bg-muted"),
      )}
    >
      <span
        className={cx(
          "flex motion-safe:transition-transform motion-safe:duration-[260ms]",
          v === "2" ? open && "rotate-90" : open && "rotate-180",
          busy && "motion-safe:animate-spin",
        )}
      >
        <Icon name={busy ? "loader-circle" : v === "2" ? "chevron-right" : "chevron-down"} size="sm" />
      </span>
    </button>
  );
}

/**
 * The strip's row: a full-width cell under its row whose content stays put
 * (sticky, the scroller's width) while the table scrolls sideways. Opening
 * and closing slide its height, .26s ease, off under reduced motion.
 */
export function StripRow({ open, span, width, onEscape, children }: { open: boolean; span: number; width?: number; onEscape: () => void; children: ReactNode }) {
  const v = useStripVariant();
  const [shown, setShown] = useState(open);
  const [expanded, setExpanded] = useState(false);
  useEffect(() => {
    if (open) {
      setShown(true);
      const id = requestAnimationFrame(() => requestAnimationFrame(() => setExpanded(true)));
      return () => cancelAnimationFrame(id);
    }
    setExpanded(false);
    const reduced = window.matchMedia("(prefers-reduced-motion: reduce)").matches;
    const t = setTimeout(() => setShown(false), reduced ? 0 : 260);
    return () => clearTimeout(t);
  }, [open]);
  if (!shown) return null;
  return (
    <tr className={cx("border-b border-border last:border-b-0", v === "1" && "bg-muted/40", v === "2" && "bg-card", (v === "3" || v === "4") && "bg-primary-tint/50")}>
      <td colSpan={span} className="p-0">
        <div
          className={cx("grid motion-safe:transition-[grid-template-rows] motion-safe:duration-[260ms] motion-safe:ease-[ease]", expanded ? "grid-rows-[1fr]" : "grid-rows-[0fr]")}
        >
          <div className="min-h-0 overflow-hidden">
            <div
              style={width ? { width } : undefined}
              onKeyDown={(e) => {
                if (e.key !== "Escape") return;
                e.stopPropagation();
                onEscape();
              }}
              className={cx(
                "sticky left-0",
                v === "1" && "border-l-2 border-primary px-4 py-3",
                v === "2" && "px-4 pt-2 pb-5",
                (v === "3" || v === "4") && "px-4 py-2",
              )}
            >
              {children}
            </div>
          </div>
        </div>
      </td>
    </tr>
  );
}

/** Variant 4's setting: one button that is pressed while the setting is on, e.g. "Tunnelled". */
export function ToggleButton({ pressed, onToggle, disabled, children }: { pressed: boolean; onToggle: () => void; disabled?: boolean; children: ReactNode }) {
  return (
    <button
      type="button"
      aria-pressed={pressed}
      disabled={disabled}
      onClick={onToggle}
      className={cx(
        "inline-flex h-7 cursor-pointer items-center gap-1.5 rounded-ui-sm border px-2.5 text-xs font-semibold transition-colors disabled:pointer-events-none disabled:opacity-40",
        pressed ? "border-primary bg-primary-tint text-primary" : "border-border bg-card text-muted-foreground hover:bg-muted hover:text-foreground",
      )}
    >
      <Icon name={pressed ? "check" : "x"} size="sm" />
      {children}
    </button>
  );
}

/** Variant 3's disruptive Action: the first click arms it for 3 s, the second runs it. */
export function TwoStepButton({ label, armed: armedLabel, disabled, onRun }: { label: string; armed: string; disabled?: boolean; onRun: () => void }) {
  const [armed, setArmed] = useState(false);
  const timer = useRef<ReturnType<typeof setTimeout>>(undefined);
  useEffect(() => () => clearTimeout(timer.current), []);
  return (
    <Button
      size="sm"
      variant={armed ? "danger" : "outline"}
      disabled={disabled}
      onClick={() => {
        clearTimeout(timer.current);
        if (armed) {
          setArmed(false);
          onRun();
          return;
        }
        setArmed(true);
        timer.current = setTimeout(() => setArmed(false), 3000);
      }}
    >
      {armed ? armedLabel : label}
    </Button>
  );
}

/** Variant 2's in-strip confirm: the question in the down tint, Cancel focused. */
export function InlineConfirm({ text, action, onCancel, onConfirm }: { text: string; action: string; onCancel: () => void; onConfirm: () => void }) {
  return (
    <div role="alertdialog" aria-label={text} className="flex flex-wrap items-center gap-3 rounded-ui border border-down/40 bg-down-tint px-3 py-2">
      <span className="text-down-text">
        <Icon name="triangle-alert" />
      </span>
      <span className="min-w-0 flex-1 text-sm">{text}</span>
      <div className="flex gap-2">
        <Button size="sm" variant="outline" autoFocus onClick={onCancel}>
          Cancel
        </Button>
        <Button size="sm" variant="danger" onClick={onConfirm}>
          {action}
        </Button>
      </div>
    </div>
  );
}
