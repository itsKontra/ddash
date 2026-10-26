// The overlays family: what opens over the Page.
import { useContext, useEffect, useId, useRef, useState, type KeyboardEvent, type ReactNode, type RefObject } from "react";
import { Button } from "./atoms";
import { Checkbox } from "./forms";
import { Icon } from "./icon";
import { Kicker } from "./kicker";
import { backdropMotion } from "./motion";
import { Leaving, Presence } from "./presence";
import { InFlush } from "./surface";
import { cx, toneTint } from "./tone";

// Presence lives in its own module so DataTable's strip can share its lifecycle; it is an overlay.
export { Presence } from "./presence";

/** What a confirm dialog asks. */
export interface ConfirmRequest {
  /** The question as the heading, e.g. "Uninstall Docker?"; without it, text is the heading. */
  title?: string;
  /** The question, or with a title the detail under it, e.g. "Stop container plex on media?". */
  text: string;
  /** The confirm button's label. Default "Confirm". */
  action?: string;
  /** Offers one checkbox with this label, e.g. "Also delete data". */
  checkbox?: string;
}

/** The Operator's answer to a confirm dialog; null when they cancelled. */
export type ConfirmAnswer = { checked: boolean } | null;

/**
 * The standard confirm dialog every disruptive Action shows, a small Dialog
 * with a down icon chip. Cancel has the focus, and Escape or a click
 * outside cancels.
 */
export function ConfirmDialog({ request, onAnswer }: { request: ConfirmRequest; onAnswer: (answer: ConfirmAnswer) => void }) {
  const [checked, setChecked] = useState(false);
  const cancel = () => onAnswer(null);
  return (
    <Overlay
      {...dialogLook}
      role="alertdialog"
      panelClassName={cx(dialogLook.panelClassName, "sm:max-w-md")}
      icon={
        <span className={cx("grid h-10 w-10 shrink-0 place-items-center rounded-ui", toneTint.down)}>
          <Icon name="triangle-alert" size="lg" />
        </span>
      }
      title={request.title ?? request.text}
      subtitle={request.title && request.text}
      onClose={cancel}
      footer={
        <div className="flex flex-col gap-4">
          {request.checkbox && <Checkbox label={request.checkbox} checked={checked} onChange={(e) => setChecked(e.target.checked)} />}
          <div className="flex flex-col-reverse gap-2 sm:flex-row sm:justify-end">
            <Button variant="outline" autoFocus onClick={cancel}>
              Cancel
            </Button>
            <Button variant="danger" onClick={() => onAnswer({ checked })}>
              {request.action ?? "Confirm"}
            </Button>
          </div>
        </div>
      }
    />
  );
}

/** What a Dialog and a Sheet both draw: the heading block, a scrolling body and a pinned footer. */
interface OverlayProps {
  title: string;
  onClose: () => void;
  children?: ReactNode;
  /** A small uppercase label over the title, e.g. "Configure". */
  kicker?: string;
  subtitle?: ReactNode;
  /** Pinned under the body while it scrolls, e.g. FormActions; a submit there names its form by id. */
  footer?: ReactNode;
}

const focusable = 'a[href], button:not(:disabled), input:not(:disabled), select:not(:disabled), textarea:not(:disabled), [tabindex]:not([tabindex="-1"])';

/**
 * Keeps the focus inside panel while it's open: it moves in on open (unless
 * an autoFocus child already took it), Tab wraps at either end, and the
 * focus goes back to where it was on close, or as the exit starts when
 * it's leaving.
 */
function useFocusTrap(panel: RefObject<HTMLElement | null>, leaving: boolean) {
  // Read during the first render, before an autoFocus child moves the focus.
  const [opener] = useState(() => (typeof document === "undefined" ? null : (document.activeElement as HTMLElement | null)));
  useEffect(() => {
    if (leaving) return;
    const el = panel.current;
    if (el && !el.contains(document.activeElement)) (el.querySelector<HTMLElement>(focusable) ?? el).focus();
    return () => {
      if (opener?.isConnected) opener.focus();
    };
  }, [panel, opener, leaving]);
  return (e: KeyboardEvent) => {
    const el = panel.current;
    if (e.key !== "Tab" || !el) return;
    const all = [...el.querySelectorAll<HTMLElement>(focusable)];
    if (all.length === 0) return;
    const [first, last] = [all[0], all[all.length - 1]];
    const at = document.activeElement;
    if (e.shiftKey && (at === first || at === el)) {
      e.preventDefault();
      last.focus();
    } else if (!e.shiftKey && at === last) {
      e.preventDefault();
      first.focus();
    }
  };
}

/**
 * The dimmed, blurred overlay a Dialog and a Sheet share. Escape (heard
 * while bubbling, so a SearchField's Escape only clears it) and a click
 * outside the panel close it. Its Escape goes no further, so a DataTable
 * strip or a Sheet it was opened from stays open.
 */
function Overlay({
  role = "dialog",
  placement,
  panelClassName,
  icon,
  title,
  onClose,
  children,
  kicker,
  subtitle,
  footer,
}: OverlayProps & { role?: "dialog" | "alertdialog"; placement: string; panelClassName: string; icon?: ReactNode }) {
  const id = useId();
  const panel = useRef<HTMLDivElement>(null);
  const leaving = useContext(Leaving);
  const trap = useFocusTrap(panel, leaving);
  // inert already stops a second close in a browser.
  const close = leaving ? () => {} : onClose;
  return (
    // An overlay is its own surface, even when a flush Panel opened it.
    <InFlush value={false}>
      <div
        // A confirm sits over the Sheet or Dialog whose Action asked it.
        className={cx("fixed inset-0 flex bg-foreground/40 backdrop-blur-xs", backdropMotion, role === "alertdialog" ? "z-60" : "z-50", placement)}
        onClick={close}
        onKeyDown={(e) => {
          if (e.key !== "Escape") return;
          e.stopPropagation();
          close();
        }}
      >
        <div
          ref={panel}
          tabIndex={-1}
          onKeyDown={trap}
          role={role}
          aria-modal="true"
          aria-labelledby={`${id}-title`}
          aria-describedby={subtitle ? `${id}-subtitle` : undefined}
          className={cx("flex w-full flex-col border-border bg-card shadow-elevation-raised outline-none", panelClassName)}
          onClick={(e) => e.stopPropagation()}
        >
          <header className="flex items-start gap-4 px-6 pt-6 pb-4">
            {icon}
            <div className="min-w-0 flex-1">
              {kicker && (
                <div className="mb-1.5">
                  <Kicker>{kicker}</Kicker>
                </div>
              )}
              <h2 id={`${id}-title`} className="text-lg leading-tight font-extrabold tracking-tight">
                {title}
              </h2>
              {subtitle && (
                <p id={`${id}-subtitle`} className="mt-1.5 text-sm leading-relaxed text-muted-foreground">
                  {subtitle}
                </p>
              )}
            </div>
            <button
              type="button"
              aria-label="Close"
              onClick={close}
              className="-mt-1 -mr-2 grid h-8 w-8 shrink-0 cursor-pointer place-items-center rounded-ui-sm text-muted-foreground hover:bg-muted hover:text-foreground"
            >
              <Icon name="x" />
            </button>
          </header>
          {children && (
            <div data-scrolls className="min-h-0 flex-1 overflow-y-auto px-6 pb-5">
              {children}
            </div>
          )}
          {footer && <footer className="border-t border-border px-6 py-4">{footer}</footer>}
        </div>
      </div>
    </InFlush>
  );
}

/** A centred dialog, e.g. re-attaching an Apps setting; a bottom sheet on phones. */
export function Dialog(props: OverlayProps) {
  return <Overlay {...dialogLook} {...props} />;
}

/*
 * The overlays' motion (tokens.css): each enters from (starting:) and leaves
 * to (in-data-[state=closed]:, set by Presence) the same values, so an exit
 * is the exact reverse and reopening mid-exit turns it around. The
 * backdrop's is in motion.ts, shared with the shell's Drawer.
 */
const dialogMotion =
  "starting:opacity-(--motion-dialog-opacity) starting:[transform:var(--motion-dialog-from)] in-data-[state=closed]:opacity-(--motion-dialog-opacity) in-data-[state=closed]:[transform:var(--motion-dialog-from)] motion-safe:transition-[opacity,transform] motion-safe:duration-(--motion-fade)";
const sheetMotion =
  "starting:[transform:var(--motion-sheet-from)] in-data-[state=closed]:[transform:var(--motion-sheet-from)] motion-safe:transition-transform motion-safe:duration-(--motion-slide) motion-safe:ease-(--motion-slide-ease)";
const menuMotion =
  "origin-top-right starting:opacity-0 starting:[transform:var(--motion-menu-from)] in-data-[state=closed]:opacity-0 in-data-[state=closed]:[transform:var(--motion-menu-from)] motion-safe:transition-[opacity,transform] motion-safe:duration-(--motion-pop)";

const dialogLook = {
  placement: "items-end justify-center sm:items-center sm:p-6",
  panelClassName: cx("max-h-[92vh] rounded-t-ui-lg border sm:max-h-[86vh] sm:max-w-xl sm:rounded-ui-lg", dialogMotion),
};

/**
 * A drawer from the right, e.g. the Overview's Add Widget picker; a bottom
 * sheet on phones. wide gives it room for a timeline or a table.
 */
export function Sheet({ wide, ...props }: OverlayProps & { children: ReactNode; wide?: boolean }) {
  return (
    <Overlay
      {...props}
      placement="items-end justify-end sm:items-stretch"
      panelClassName={cx(
        "max-h-[92vh] rounded-t-ui-lg border-t sm:h-full sm:max-h-none sm:rounded-none sm:border-t-0 sm:border-l",
        wide ? "sm:max-w-2xl" : "sm:max-w-lg",
        sheetMotion,
      )}
    />
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
 * A menu of Actions, e.g. a Plugin card's Uninstall: a "⋯" button named by
 * label that opens the items under it. Picking one, Escape or a click
 * outside closes it. It wraps its own Presence, so it animates out too.
 */
export function Menu({ label, items, disabled }: { label: string; items: readonly MenuItem[]; disabled?: boolean }) {
  const [open, setOpen] = useState(false);
  return (
    <div className="relative inline-block">
      <Button variant="ghost" size="sm" aria-label={label} aria-haspopup="menu" aria-expanded={open} disabled={disabled} onClick={() => setOpen(!open)}>
        <Icon name="ellipsis" />
      </Button>
      <Presence>
        {open && (
          <>
            <div className="fixed inset-0 z-30" onClick={() => setOpen(false)} />
            <div
              role="menu"
              aria-label={label}
              className={cx(
                "absolute right-0 z-40 mt-1 flex min-w-40 flex-col rounded-ui-lg border border-border bg-card p-1.5 shadow-elevation-raised",
                menuMotion,
              )}
              onKeyDown={(e) => {
                if (e.key !== "Escape") return;
                e.stopPropagation();
                setOpen(false);
              }}
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
                    "cursor-pointer rounded-ui-sm px-2.5 py-1.5 text-left text-sm font-medium focus-visible:outline-offset-0 disabled:pointer-events-none disabled:opacity-40",
                    item.danger ? "text-down-text hover:bg-down-tint" : "hover:bg-muted",
                  )}
                >
                  {item.label}
                </button>
              ))}
            </div>
          </>
        )}
      </Presence>
    </div>
  );
}
