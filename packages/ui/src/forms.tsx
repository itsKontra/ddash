// The forms family: the controls an Operator sets.
import { cx } from "./tone";

/** Picks one of a few options, e.g. a Widget's size: a radio group drawn as joined buttons. */
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
    <div role="radiogroup" aria-label={label} className="inline-flex rounded-ui border border-border bg-muted p-0.5">
      {options.map((o) => (
        <button
          key={o}
          type="button"
          role="radio"
          aria-checked={o === value}
          onClick={() => onChange(o)}
          className={cx(
            "cursor-pointer rounded-ui px-2 py-0.5 text-xs font-medium",
            o === value ? "bg-card text-foreground shadow-sm" : "text-muted-foreground",
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
        "relative inline-flex h-5 w-9 shrink-0 cursor-pointer items-center rounded-full transition-colors disabled:pointer-events-none disabled:opacity-40",
        checked ? "bg-primary" : "bg-muted",
      )}
    >
      <span className={cx("inline-block h-4 w-4 rounded-full bg-card shadow transition-transform", checked ? "translate-x-4.5" : "translate-x-0.5")} />
    </button>
  );
}
