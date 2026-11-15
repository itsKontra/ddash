import { Icon, Skeleton, cx, type IconName } from "@ddash/ui";
import { useId, type ReactNode } from "react";

/**
 * A small Widget's figures, one per row, divided: the label (after its
 * icon) left, the value right. Nothing is cut: the label and the value
 * each keep one line in full. Under 16rem (an S Widget in the Overview's
 * grid of four) the icons drop out, under 18rem the Units; a value that
 * still doesn't fit moves under its label, and only a free-form name in a
 * value truncates (truncate). The rows are a size container for both.
 */
export function Rows({ children }: { children: ReactNode }) {
  return <div className="@container flex flex-col divide-y divide-border">{children}</div>;
}

/** One figure in Rows, a group named by its label; an undefined value is still loading. */
export function Row({ icon, label, value, truncate }: { icon?: IconName; label: string; value: ReactNode; truncate?: boolean }) {
  const id = useId();
  return (
    <div role="group" aria-labelledby={id} className="flex min-h-10 flex-wrap items-center gap-x-2 gap-y-1 py-1.5 text-sm">
      <span className="flex shrink-0 items-center gap-2 whitespace-nowrap text-muted-foreground">
        {icon && (
          <span className="leading-none @max-3xs:hidden">
            <Icon name={icon} size="sm" />
          </span>
        )}
        <span id={id}>{label}</span>
      </span>
      {value === undefined ? (
        <span className="ml-auto w-16">
          <Skeleton />
        </span>
      ) : (
        <span className={cx("ml-auto flex items-center justify-end gap-1.5 font-semibold whitespace-nowrap tabular-nums", truncate && "min-w-0")}>{value}</span>
      )}
    </div>
  );
}

/**
 * The part of a value a narrow Widget drops, e.g. " of 61,122" after "4%"
 * or " online" after "12 of 14": hidden while its size container (Rows,
 * or an @container of the Widget's own) is under 18rem, as an S Widget
 * is in the Overview's grid of four.
 */
export function Unit({ children }: { children: ReactNode }) {
  return <span className="@max-2xs:hidden">{children}</span>;
}
