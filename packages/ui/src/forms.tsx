// The forms family: the controls an Operator sets.
import {
  createContext,
  useContext,
  useEffect,
  useId,
  useLayoutEffect,
  useRef,
  useState,
  type InputHTMLAttributes,
  type ReactNode,
  type SelectHTMLAttributes,
  type TextareaHTMLAttributes,
} from "react";
import { AppIcon, iconName } from "./atoms";
import { noMatch } from "./data";
import { Icon, type IconName } from "./icon";
import { cx } from "./tone";

type Wiring = { id: string; "aria-describedby"?: string; "aria-invalid"?: true };

const FieldContext = createContext<Wiring | null>(null);

/** The id, descriptions and error state a Field gives the control inside it. */
const useWiring = (): Partial<Wiring> => useContext(FieldContext) ?? {};

/**
 * A group control (a radio group) can't be a `<label for>`'s target: it
 * takes the Field's label by id instead, and tells the Field to drop `for`.
 */
const FieldGroup = createContext<{ labelId: string; asGroup: () => void } | null>(null);

/** A labelled control with its hint, or its error in place of the hint; the control inside takes its id and descriptions. */
export function Field({ label, hint, error, children }: { label: string; hint?: ReactNode; error?: string; children: ReactNode }) {
  const id = useId();
  const note = error ? `${id}-error` : hint ? `${id}-hint` : undefined;
  const [group, setGroup] = useState(false);
  const [labelling] = useState(() => ({ labelId: `${id}-label`, asGroup: () => setGroup(true) }));
  return (
    <FieldContext.Provider value={{ id, "aria-describedby": note, "aria-invalid": error ? true : undefined }}>
      <div className="flex flex-col gap-1.5">
        <label id={labelling.labelId} htmlFor={group ? undefined : id} className="text-xs font-semibold text-foreground">
          {label}
        </label>
        <FieldGroup.Provider value={labelling}>{children}</FieldGroup.Provider>
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
 * Inside a Field it takes the Field's label and hint.
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
  const field = useContext(FieldGroup);
  const { "aria-describedby": describedBy } = useWiring();
  useLayoutEffect(() => field?.asGroup(), [field]);
  return (
    <div
      role="radiogroup"
      aria-label={field ? undefined : label}
      aria-labelledby={field?.labelId}
      aria-describedby={describedBy}
      className="inline-flex flex-wrap gap-1"
    >
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

/**
 * An on/off switch that acts at once (no confirm), e.g. a Device's Direct
 * toggle; label names it for screen readers. Inside a Field it takes the
 * Field's id and hint.
 */
export function Switch({ id, checked, onChange, label, disabled }: { id?: string; checked: boolean; onChange: (checked: boolean) => void; label: string; disabled?: boolean }) {
  const wiring = useWiring();
  return (
    <button
      {...wiring}
      id={id ?? wiring.id}
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

/** How long typing pauses before a picker searches the core. */
const searchDelay = 250;

/** What a picker's search found for a query: the matching names, or a failure. */
type Found = { q: string; names: string[] } | { q: string; failed: true };

/**
 * Picks a lucide icon by name, e.g. a record's icon. A button showing the
 * icon opens a popover: typing searches the core's icons by name and tag,
 * the arrow keys move through the grid and Enter picks. Inside a Field it
 * takes the Field's label.
 */
export function IconPicker({ value, onChange, label }: { value: string | null; onChange: (name: string) => void; label: string }) {
  return (
    <Picker
      label={label}
      trigger={
        value ? (
          <>
            <Icon name={value as IconName} />
            <span className="font-mono text-xs">{value}</span>
          </>
        ) : (
          <span className="text-muted-foreground">Choose an icon</span>
        )
      }
      search={{ path: "/api/icons/search", columns: 8, draw: (n) => <Icon name={n as IconName} />, hint: "Type a name or a word, e.g. server." }}
      onPick={onChange}
    />
  );
}

/**
 * Picks an App's icon: a homarr-labs/dashboard-icons name, or none
 * (Automatic: the icon matched by the App's name, else its favicon). The
 * button shows the icon the App gets. The popover offers Automatic first,
 * then the core's dashboard-icons names and aliases that match what's
 * typed, then what's typed itself, saved as it is: its logo draws once
 * ddash's pinned icons hold it. Inside a Field it takes the Field's label.
 */
export function AppIconPicker({
  value,
  onChange,
  label,
  name,
  url,
}: {
  value: string;
  onChange: (icon: string) => void;
  label: string;
  /** The App's name and URL, for what Automatic draws. */
  name: string;
  url?: string;
}) {
  const app = name || "?";
  const automatic: Entry = {
    value: "",
    label: "Automatic",
    row: true,
    content: (
      <>
        <AppIcon name={app} url={url} size={24} />
        <span className="font-semibold">Automatic</span>
        <span className="truncate text-xs text-muted-foreground">By the App's name, else its favicon</span>
      </>
    ),
  };
  const typed = (query: string, names: readonly string[]): Entry | null => {
    const slug = iconName(query);
    if (!slug || names.includes(slug)) return null;
    return {
      value: slug,
      label: `Use ${slug}`,
      row: true,
      content: (
        <>
          <AppIcon name={slug} icon={slug} size={24} />
          <span>
            Use <span className="font-mono text-xs">{slug}</span>
          </span>
          <span className="truncate text-xs text-muted-foreground">Not among this Release's icons</span>
        </>
      ),
    };
  };
  return (
    <Picker
      label={label}
      trigger={
        <>
          <AppIcon name={app} icon={value} url={url} size={20} />
          {value ? <span className="font-mono text-xs">{value}</span> : <span className="text-muted-foreground">Automatic</span>}
        </>
      }
      search={{
        path: "/api/icons/apps/search",
        columns: 6,
        draw: (n) => <AppIcon name={n} icon={n} size={28} />,
        hint: "Type an App's name, e.g. jellyfin.",
        first: automatic,
        typed,
      }}
      onPick={onChange}
    />
  );
}

/** One entry of a picker's popover: a found icon, a cell of the grid, or a whole row of the picker's own (Automatic, what was typed). */
interface Entry {
  value: string;
  label: string;
  content: ReactNode;
  row?: true;
}

/** What a picker's popover searches, and how it draws what it finds. */
interface SearchSpec {
  /** The core's search, answering GET path?q= with names, best first. */
  path: string;
  /** The grid's columns, which ArrowUp and ArrowDown step by. */
  columns: 6 | 8;
  draw: (name: string) => ReactNode;
  /** Shown while nothing is typed. */
  hint: string;
  /** Offered first, typed or not. */
  first?: Entry;
  /** Offered after what a query found, or alone when the search failed. */
  typed?: (query: string, names: readonly string[]) => Entry | null;
}

/** A button that opens a SearchSpec's popover and reports the pick. */
function Picker({ label, trigger, search, onPick }: { label: string; trigger: ReactNode; search: SearchSpec; onPick: (value: string) => void }) {
  const wiring = useWiring();
  const inField = useContext(FieldContext) !== null;
  const [open, setOpen] = useState(false);
  const button = useRef<HTMLButtonElement>(null);
  const close = () => {
    setOpen(false);
    button.current?.focus();
  };
  return (
    <span className="relative inline-block">
      <button
        ref={button}
        type="button"
        {...wiring}
        aria-label={inField ? undefined : label}
        aria-haspopup="dialog"
        aria-expanded={open}
        onClick={() => setOpen(!open)}
        className={cx(control, "inline-flex h-9 w-auto cursor-pointer items-center gap-2 bg-card")}
      >
        {trigger}
      </button>
      {open && (
        <>
          <div className="fixed inset-0 z-30" onClick={() => setOpen(false)} />
          <IconSearch
            label={label}
            spec={search}
            onPick={(value) => {
              onPick(value);
              close();
            }}
            onClose={close}
          />
        </>
      )}
    </span>
  );
}

const gridCols = { 6: "grid-cols-6", 8: "grid-cols-8" };

/**
 * Where an arrow key moves from entry i, or i where it can't: through the
 * grid's cells (gridStart to gridEnd) by one or a row, and from a whole row
 * to the entry beside it.
 */
function moveFrom(i: number, key: string, gridStart: number, gridEnd: number, columns: number): number {
  const inGrid = i >= gridStart && i < gridEnd;
  const row = (j: number) => Math.floor((j - gridStart) / columns);
  switch (key) {
    case "ArrowRight":
      return i + 1;
    case "ArrowLeft":
      return i - 1;
    case "ArrowDown":
      if (!inGrid) return i + 1;
      if (i + columns < gridEnd) return i + columns;
      return row(i) === row(gridEnd - 1) ? gridEnd : i;
    case "ArrowUp":
      if (!inGrid) return i === gridEnd && gridEnd > gridStart ? gridEnd - 1 : i - 1;
      return i - columns >= gridStart ? i - columns : gridStart > 0 ? gridStart - 1 : i;
    default:
      return i;
  }
}

/** A picker's popover: the search box over the grid of what it finds. */
function IconSearch({ label, spec, onPick, onClose }: { label: string; spec: SearchSpec; onPick: (value: string) => void; onClose: () => void }) {
  const id = useId();
  const [q, setQ] = useState("");
  const [found, setFound] = useState<Found | null>(null);
  // The entry the arrow keys moved to; -1 until they do, for the default below.
  const [moved, setMoved] = useState(-1);
  const query = q.trim();
  const { path } = spec;

  useEffect(() => {
    if (query === "") return;
    let stale = false;
    const timer = setTimeout(async () => {
      let next: Found;
      try {
        const res = await globalThis.fetch(`${path}?q=${encodeURIComponent(query)}`);
        if (!res.ok) throw new Error(`GET ${path}: ${res.status}`);
        const names: unknown = await res.json();
        if (!Array.isArray(names)) throw new Error(`GET ${path}: not a list`);
        next = { q: query, names: names as string[] };
      } catch {
        next = { q: query, failed: true };
      }
      if (stale) return;
      setFound(next);
      setMoved(-1);
    }, searchDelay);
    return () => {
      stale = true;
      clearTimeout(timer);
    };
  }, [path, query]);

  const shown = query !== "" && found?.q === query ? found : null;
  const names = shown && "names" in shown ? shown.names : [];
  // What was typed is offered even when the search failed, so an icon can still be set.
  const typed = shown ? spec.typed?.(query, names) : null;
  const gridStart = spec.first ? 1 : 0;
  const gridEnd = gridStart + names.length;
  const entries: Entry[] = [
    ...(spec.first ? [spec.first] : []),
    ...names.map((n) => ({ value: n, label: n, content: spec.draw(n) })),
    ...(typed ? [typed] : []),
  ];
  // Unmoved, the first icon found is active, so Enter picks it; else the first entry.
  const active = moved >= 0 && moved < entries.length ? moved : names.length > 0 ? gridStart : 0;

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
          aria-expanded={entries.length > 0}
          aria-controls={`${id}-grid`}
          aria-activedescendant={entries.length > 0 ? `${id}-${active}` : undefined}
          aria-autocomplete="list"
          autoFocus
          value={q}
          placeholder="Search icons"
          onChange={(e) => {
            setQ(e.target.value);
            setMoved(-1);
          }}
          onKeyDown={(e) => {
            if (e.key === "Escape") {
              e.preventDefault();
              e.stopPropagation();
              onClose();
            } else if (e.key === "Enter") {
              e.preventDefault();
              if (entries[active]) onPick(entries[active].value);
            } else if (e.key.startsWith("Arrow")) {
              e.preventDefault();
              const next = moveFrom(active, e.key, gridStart, gridEnd, spec.columns);
              if (next >= 0 && next < entries.length) setMoved(next);
            }
          }}
          className={cx(control, "h-9 bg-background pl-9 focus:bg-card")}
        />
      </span>
      <div id={`${id}-grid`} role="listbox" aria-label={`${label} results`} className={cx("grid max-h-64 gap-1 overflow-y-auto", gridCols[spec.columns])}>
        {entries.map((e, i) => (
          <div
            key={`${e.row ? "row:" : ""}${e.value}`}
            id={`${id}-${i}`}
            role="option"
            aria-label={e.label}
            aria-selected={i === active}
            title={e.row ? undefined : e.label}
            onMouseDown={(ev) => ev.preventDefault()}
            onClick={() => onPick(e.value)}
            className={cx(
              "flex cursor-pointer items-center rounded-ui-sm transition-colors",
              e.row ? "col-span-full gap-2 px-2 py-1.5 text-sm" : "aspect-square justify-center",
              i === active ? "bg-primary-tint text-primary" : "text-foreground hover:bg-muted",
            )}
          >
            {e.content}
          </div>
        ))}
      </div>
      {shown && names.length === 0 && (
        <p className="px-1 py-2 text-xs text-muted-foreground">
          {"failed" in shown ? "Couldn't search the icons. Try again." : `${noMatch("icons", shown.q)}.`}
        </p>
      )}
      {query === "" && <p className="px-1 py-2 text-xs text-muted-foreground">{spec.hint}</p>}
    </div>
  );
}
