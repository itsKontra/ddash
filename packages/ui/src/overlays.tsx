// The overlays family: what opens over the Page.
import { useId, useState, type ReactNode } from "react";
import { Button } from "./atoms";
import { cx } from "./tone";

/** What a confirm dialog asks. */
export interface ConfirmRequest {
  /** The question, e.g. "Stop container plex on media?". */
  text: string;
  /** The confirm button's label. Default "Confirm". */
  action?: string;
  /** Offers one checkbox with this label, e.g. "Also delete data". */
  checkbox?: string;
}

/** The Operator's answer to a confirm dialog; null when they cancelled. */
export type ConfirmAnswer = { checked: boolean } | null;

/**
 * The standard confirm dialog every disruptive Action shows. Cancel has the
 * focus, and Escape or a click outside cancels.
 */
export function ConfirmDialog({ request, onAnswer }: { request: ConfirmRequest; onAnswer: (answer: ConfirmAnswer) => void }) {
  const [checked, setChecked] = useState(false);
  const id = useId();
  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-foreground/40 p-4"
      onClick={() => onAnswer(null)}
      onKeyDown={(e) => e.key === "Escape" && onAnswer(null)}
    >
      <div
        role="alertdialog"
        aria-modal="true"
        aria-labelledby={`${id}-text`}
        className="w-full max-w-sm rounded-ui border border-border bg-card p-5 shadow-lg"
        onClick={(e) => e.stopPropagation()}
      >
        <p id={`${id}-text`} className="text-sm">
          {request.text}
        </p>
        {request.checkbox && (
          <label className="mt-4 flex items-center gap-2 text-sm">
            <input type="checkbox" checked={checked} onChange={(e) => setChecked(e.target.checked)} />
            {request.checkbox}
          </label>
        )}
        <div className="mt-5 flex justify-end gap-2">
          <Button variant="outline" autoFocus onClick={() => onAnswer(null)}>
            Cancel
          </Button>
          <Button variant="danger" onClick={() => onAnswer({ checked })}>
            {request.action ?? "Confirm"}
          </Button>
        </div>
      </div>
    </div>
  );
}

/** A drawer from the right, e.g. the Overview's Add Widget picker. Escape or a click outside closes it. */
export function Sheet({ title, onClose, children }: { title: string; onClose: () => void; children: ReactNode }) {
  const id = useId();
  return (
    <div
      className="fixed inset-0 z-40 flex justify-end bg-foreground/40"
      onClick={onClose}
      onKeyDown={(e) => e.key === "Escape" && onClose()}
    >
      <div
        role="dialog"
        aria-modal="true"
        aria-labelledby={id}
        className="h-full w-full max-w-md overflow-y-auto border-l border-border bg-background p-5"
        onClick={(e) => e.stopPropagation()}
      >
        <div className="mb-4 flex items-center">
          <h2 id={id} className="text-lg font-semibold">
            {title}
          </h2>
          <Button variant="ghost" size="sm" className="ml-auto" autoFocus onClick={onClose}>
            Close
          </Button>
        </div>
        {children}
      </div>
    </div>
  );
}

/** One entry of a Menu; danger marks a disruptive Action. */
export interface MenuItem {
  label: string;
  onSelect: () => void;
  disabled?: boolean;
  danger?: boolean;
}

/**
 * A row's menu, e.g. a Workload's power Actions: a "⋯" button named by
 * label that opens the items under it. Picking one, Escape or a click
 * outside closes it.
 */
export function Menu({ label, items, disabled }: { label: string; items: readonly MenuItem[]; disabled?: boolean }) {
  const [open, setOpen] = useState(false);
  return (
    <div className="relative inline-block">
      <Button variant="ghost" size="sm" aria-label={label} aria-haspopup="menu" aria-expanded={open} disabled={disabled} onClick={() => setOpen(!open)}>
        ⋯
      </Button>
      {open && (
        <>
          <div className="fixed inset-0 z-30" onClick={() => setOpen(false)} />
          <div
            role="menu"
            aria-label={label}
            className="absolute right-0 z-40 mt-1 flex min-w-32 flex-col rounded-ui border border-border bg-card p-1 shadow-lg"
            onKeyDown={(e) => e.key === "Escape" && setOpen(false)}
          >
            {items.map((item, i) => (
              <button
                key={item.label}
                type="button"
                role="menuitem"
                autoFocus={i === 0}
                disabled={item.disabled}
                onClick={() => {
                  setOpen(false);
                  item.onSelect();
                }}
                className={cx(
                  "cursor-pointer rounded-ui px-2.5 py-1.5 text-left text-sm hover:bg-muted disabled:pointer-events-none disabled:opacity-40",
                  item.danger && "text-down-text",
                )}
              >
                {item.label}
              </button>
            ))}
          </div>
        </>
      )}
    </div>
  );
}
