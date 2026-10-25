// The forms family: the controls an Operator sets.
import {
  createContext,
  useContext,
  useEffect,
  useId,
  useRef,
  useState,
  type InputHTMLAttributes,
  type ReactNode,
  type SelectHTMLAttributes,
  type TextareaHTMLAttributes,
} from "react";
import { Icon, type IconName } from "./icon";
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
        <span className="pointer-events-none absolute inset-0 hidden items-center justify-center text-primary-foreground peer-checked:flex">
          <Icon name="check" size="sm" />
        </span>
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
      <Magnifier />
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

/** The search icon at the start of a search box. */
function Magnifier() {
  return (
    <span className="pointer-events-none absolute top-1/2 left-3 flex -translate-y-1/2 text-muted-foreground">
      <Icon name="search" />
    </span>
  );
}

/** An up/down chevron pair drawn at the end of a Select. */
function Chevrons() {
  return (
    <span className="pointer-events-none absolute top-1/2 right-2.5 flex -translate-y-1/2 text-muted-foreground">
      <Icon name="chevrons-up-down" size="sm" />
    </span>
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

/** The icon grid's columns, which ArrowUp and ArrowDown step by. */
const pickerColumns = 8;

/** How long typing pauses before IconPicker searches the core. */
const searchDelay = 250;

/** What IconPicker's search found for a query: the matching names, or a failure. */
type Found = { q: string; names: string[] } | { q: string; failed: true };

/**
 * Picks a lucide icon by name, e.g. a record's icon. A button showing the
 * icon opens a popover: typing searches the core's icons by name and tag,
 * the arrow keys move through the grid and Enter picks. Inside a Field it
 * takes the Field's label.
 */
export function IconPicker({ value, onChange, label }: { value: string | null; onChange: (name: string) => void; label: string }) {
  const wiring = useWiring();
  const inField = useContext(FieldContext) !== null;
  const [open, setOpen] = useState(false);
  const trigger = useRef<HTMLButtonElement>(null);
  const close = () => {
    setOpen(false);
    trigger.current?.focus();
  };
  return (
    <span className="relative inline-block">
      <button
        ref={trigger}
        type="button"
        {...wiring}
        aria-label={inField ? undefined : label}
        aria-haspopup="dialog"
        aria-expanded={open}
        onClick={() => setOpen(!open)}
        className={cx(control, "inline-flex h-9 w-auto cursor-pointer items-center gap-2 bg-card")}
      >
        {value ? (
          <>
            <Icon name={value as IconName} />
            <span className="font-mono text-xs">{value}</span>
          </>
        ) : (
          <span className="text-muted-foreground">Choose an icon</span>
        )}
      </button>
      {open && (
        <>
          <div className="fixed inset-0 z-30" onClick={() => setOpen(false)} />
          <IconSearch
            label={label}
            onPick={(name) => {
              onChange(name);
              close();
            }}
            onClose={close}
          />
        </>
      )}
    </span>
  );
}

const gridStep: Record<string, number> = { ArrowRight: 1, ArrowLeft: -1, ArrowDown: pickerColumns, ArrowUp: -pickerColumns };

/** IconPicker's popover: the search box over the grid of what it finds. */
function IconSearch({ label, onPick, onClose }: { label: string; onPick: (name: string) => void; onClose: () => void }) {
  const id = useId();
  const [q, setQ] = useState("");
  const [found, setFound] = useState<Found | null>(null);
  const [active, setActive] = useState(0);
  const query = q.trim();

  useEffect(() => {
    if (query === "") return;
    let stale = false;
    const timer = setTimeout(async () => {
      let next: Found;
      try {
        const res = await globalThis.fetch(`/api/icons/search?q=${encodeURIComponent(query)}`);
        if (!res.ok) throw new Error(`GET /api/icons/search: ${res.status}`);
        next = { q: query, names: (await res.json()) as string[] };
      } catch {
        next = { q: query, failed: true };
      }
      if (stale) return;
      setFound(next);
      setActive(0);
    }, searchDelay);
    return () => {
      stale = true;
      clearTimeout(timer);
    };
  }, [query]);

  const shown = query !== "" && found?.q === query ? found : null;
  const names = shown && "names" in shown ? shown.names : [];

  return (
    <div
      role="dialog"
      aria-label={label}
      className="absolute left-0 z-40 mt-1 flex w-80 max-w-[calc(100vw-2rem)] flex-col gap-2 rounded-ui-lg border border-border bg-card p-2 shadow-elevation-raised"
    >
      <span className="relative block">
        <Magnifier />
        <input
          type="text"
          role="combobox"
          aria-label="Search icons"
          aria-expanded={names.length > 0}
          aria-controls={`${id}-grid`}
          aria-activedescendant={names.length > 0 ? `${id}-${active}` : undefined}
          aria-autocomplete="list"
          autoFocus
          value={q}
          placeholder="Search icons"
          onChange={(e) => setQ(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Escape") {
              e.preventDefault();
              e.stopPropagation();
              onClose();
            } else if (e.key === "Enter") {
              e.preventDefault();
              if (names[active]) onPick(names[active]);
            } else if (gridStep[e.key] !== undefined) {
              e.preventDefault();
              const next = active + gridStep[e.key];
              if (next >= 0 && next < names.length) setActive(next);
            }
          }}
          className={cx(control, "h-9 bg-background pl-9 focus:bg-card")}
        />
      </span>
      <div id={`${id}-grid`} role="listbox" aria-label={`${label} results`} className="grid max-h-64 grid-cols-8 gap-1 overflow-y-auto">
        {names.map((n, i) => (
          <div
            key={n}
            id={`${id}-${i}`}
            role="option"
            aria-label={n}
            aria-selected={i === active}
            title={n}
            onMouseDown={(e) => e.preventDefault()}
            onClick={() => onPick(n)}
            className={cx(
              "flex aspect-square cursor-pointer items-center justify-center rounded-ui-sm transition-colors",
              i === active ? "bg-primary-tint text-primary" : "text-foreground hover:bg-muted",
            )}
          >
            <Icon name={n as IconName} />
          </div>
        ))}
      </div>
      {shown && names.length === 0 && (
        <p className="px-1 py-2 text-xs text-muted-foreground">
          {"failed" in shown ? "Couldn't search the icons. Try again." : `No icons match “${shown.q}”.`}
        </p>
      )}
      {query === "" && <p className="px-1 py-2 text-xs text-muted-foreground">Type a name or a word, e.g. server.</p>}
    </div>
  );
}
