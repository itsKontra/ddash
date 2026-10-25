// The forms family: the controls an Operator sets.
import { createContext, useContext, useId, type InputHTMLAttributes, type ReactNode, type SelectHTMLAttributes, type TextareaHTMLAttributes } from "react";
import { cx } from "./tone";

type Wiring = { id: string; "aria-describedby"?: string; "aria-invalid"?: true };

const FieldContext = createContext<Wiring | null>(null);

/** The id, descriptions and error state a Field gives the control inside it. */
const useWiring = (): Partial<Wiring> => useContext(FieldContext) ?? {};

/** A labelled control with its hint, or its error in place of the hint; the control inside takes its id and descriptions. */
export function Field({ label, hint, error, children }: { label: string; hint?: ReactNode; error?: string; children: ReactNode }) {
  const id = useId();
  const note = error ? `${id}-error` : hint ? `${id}-hint` : undefined;
  return (
    <FieldContext.Provider value={{ id, "aria-describedby": note, "aria-invalid": error ? true : undefined }}>
      <div className="flex flex-col gap-1.5">
        <label htmlFor={id} className="text-xs font-semibold text-foreground">
          {label}
        </label>
        {children}
        {error ? (
          <p id={note} className="text-xs font-medium text-down-text">
            {error}
          </p>
        ) : (
          hint && (
            <p id={note} className="text-xs text-muted-foreground">
              {hint}
            </p>
          )
        )}
      </div>
    </FieldContext.Provider>
  );
}

const control =
  "w-full rounded-ui-sm border border-border px-3 text-sm text-foreground transition-colors placeholder:text-muted-foreground hover:border-muted-foreground focus:border-primary disabled:cursor-not-allowed disabled:opacity-50 aria-invalid:border-down";

/** A one-line text box; inside a Field it takes the Field's label. */
export function TextInput(props: Omit<InputHTMLAttributes<HTMLInputElement>, "className">) {
  return <input type="text" {...useWiring()} {...props} className={cx(control, "h-9 bg-card")} />;
}

/** A multi-line text box; inside a Field it takes the Field's label. */
export function TextArea(props: Omit<TextareaHTMLAttributes<HTMLTextAreaElement>, "className">) {
  return <textarea {...useWiring()} {...props} className={cx(control, "min-h-24 bg-card py-2")} />;
}

/** A native checkbox with its label beside it, e.g. a table toolbar's "Blocked only". */
export function Checkbox({ label, ...props }: Omit<InputHTMLAttributes<HTMLInputElement>, "className" | "type"> & { label: ReactNode }) {
  return (
    <label className="inline-flex cursor-pointer items-center gap-2 text-sm text-foreground has-disabled:cursor-not-allowed has-disabled:opacity-50">
      <span className="relative inline-flex shrink-0">
        <input
          type="checkbox"
          {...useWiring()}
          {...props}
          className="peer h-4 w-4 cursor-pointer appearance-none rounded-ui-sm border border-muted-foreground bg-card transition-colors checked:border-primary checked:bg-primary disabled:cursor-not-allowed aria-invalid:border-down"
        />
        <svg
          viewBox="0 0 24 24"
          aria-hidden="true"
          className="pointer-events-none absolute inset-0.5 hidden fill-none stroke-primary-foreground stroke-3 peer-checked:block"
        >
          <path d="M20 6 9 17l-5-5" strokeLinecap="round" strokeLinejoin="round" />
        </svg>
      </span>
      {label}
    </label>
  );
}

/**
 * A search box with its icon, e.g. an explorer's filter. Escape clears it,
 * without also closing the Sheet or Dialog around it.
 */
export function SearchField({ value, onChange, label, placeholder }: { value: string; onChange: (value: string) => void; label: string; placeholder?: string }) {
  return (
    <span className="relative block min-w-0 flex-1">
      <svg
        viewBox="0 0 24 24"
        aria-hidden="true"
        className="pointer-events-none absolute top-1/2 left-3 h-4 w-4 -translate-y-1/2 fill-none stroke-muted-foreground stroke-2"
      >
        <circle cx="11" cy="11" r="7" />
        <path d="m20 20-3.5-3.5" strokeLinecap="round" />
      </svg>
      <input
        type="search"
        aria-label={label}
        value={value}
        placeholder={placeholder}
        onChange={(e) => onChange(e.target.value)}
        onKeyDown={(e) => {
          if (e.key !== "Escape" || value === "") return;
          e.preventDefault();
          e.stopPropagation();
          onChange("");
        }}
        className={cx(control, "h-9 bg-background pl-9 focus:bg-card")}
      />
    </span>
  );
}

/** An up/down chevron pair drawn at the end of a Select; Icon arrives with the icon family. */
function Chevrons() {
  return (
    <svg
      viewBox="0 0 24 24"
      aria-hidden="true"
      className="pointer-events-none absolute top-1/2 right-2.5 h-3.5 w-3.5 -translate-y-1/2 fill-none stroke-muted-foreground stroke-2"
    >
      <path d="m7 15 5 5 5-5M7 9l5-5 5 5" strokeLinecap="round" strokeLinejoin="round" />
    </svg>
  );
}

/**
 * A native select. Inside a Field it takes the Field's label; given a label
 * and no Field it is the compact toolbar select, named by that label.
 */
export function Select({ label, ...props }: Omit<SelectHTMLAttributes<HTMLSelectElement>, "className"> & { label?: string }) {
  const field = useContext(FieldContext);
  const compact = field === null && label !== undefined;
  return (
    <span className={cx("relative min-w-0", compact ? "inline-block" : "block")}>
      <select
        {...field}
        aria-label={compact ? label : undefined}
        {...props}
        className={cx(
          control,
          "cursor-pointer appearance-none bg-card pr-8",
          compact ? "h-8 w-auto text-xs font-semibold text-muted-foreground hover:text-foreground" : "h-9",
        )}
      />
      <Chevrons />
    </span>
  );
}

/**
 * The foot of a form: its buttons right-aligned (Cancel, then Save), after
 * an optional status line. In a Dialog or Sheet it goes in the footer, its
 * submit tied to the form by the form's id.
 */
export function FormActions({ status, children }: { status?: ReactNode; children: ReactNode }) {
  return (
    <div className="flex flex-wrap items-center gap-2">
      {status && <div className="min-w-0 flex-1 text-xs text-muted-foreground">{status}</div>}
      <div className="ml-auto flex flex-wrap items-center justify-end gap-2">{children}</div>
    </div>
  );
}

const arrowStep: Record<string, number> = { ArrowRight: 1, ArrowDown: 1, ArrowLeft: -1, ArrowUp: -1 };

/**
 * Picks one of a few options, e.g. a Widget's size: a radio group drawn like
 * Tabs. It takes one tab stop; the arrow keys move along it and pick.
 */
export function Segmented<T extends string>({
  label,
  value,
  options,
  onChange,
}: {
  label: string;
  value: T;
  options: readonly T[];
  onChange: (value: T) => void;
}) {
  return (
    <div role="radiogroup" aria-label={label} className="inline-flex flex-wrap gap-1">
      {options.map((o, i) => (
        <button
          key={o}
          type="button"
          role="radio"
          aria-checked={o === value}
          tabIndex={o === value ? 0 : -1}
          onClick={() => onChange(o)}
          onKeyDown={(e) => {
            const step = arrowStep[e.key];
            if (step === undefined) return;
            e.preventDefault();
            const next = (i + step + options.length) % options.length;
            (e.currentTarget.parentElement?.children[next] as HTMLElement | undefined)?.focus();
            onChange(options[next]);
          }}
          className={cx(
            "cursor-pointer rounded-ui-sm px-2.5 py-1 text-xs font-semibold transition-colors",
            o === value ? "bg-primary-tint text-primary" : "text-muted-foreground hover:bg-muted hover:text-foreground",
          )}
        >
          {o}
        </button>
      ))}
    </div>
  );
}

/** An on/off switch that acts at once (no confirm), e.g. a Device's Direct toggle; label names it for screen readers. */
export function Switch({ id, checked, onChange, label, disabled }: { id?: string; checked: boolean; onChange: (checked: boolean) => void; label: string; disabled?: boolean }) {
  return (
    <button
      id={id}
      type="button"
      role="switch"
      aria-checked={checked}
      aria-label={label}
      disabled={disabled}
      onClick={() => onChange(!checked)}
      className={cx(
        "relative inline-flex h-5 w-9 shrink-0 cursor-pointer items-center rounded-full transition-colors disabled:cursor-not-allowed disabled:opacity-50",
        checked ? "bg-primary" : "bg-muted-foreground",
      )}
    >
      <span
        className={cx(
          "inline-block h-4 w-4 rounded-full bg-card shadow-elevation transition-transform motion-reduce:transition-none",
          checked ? "translate-x-4.5" : "translate-x-0.5",
        )}
      />
    </button>
  );
}
