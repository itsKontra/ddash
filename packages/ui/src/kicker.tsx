// The small uppercase label over a heading. Internal: index.ts doesn't export it.
import type { ReactNode } from "react";
import { useOnHero } from "./surface";
import { cx } from "./tone";

/**
 * A kicker: primary, muted when it names a Panel, the hero's accent on the
 * hero. `rule` draws the accent rule before it, as on a PageTitle.
 */
export function Kicker({ id, muted, rule, children }: { id?: string; muted?: boolean; rule?: boolean; children: ReactNode }) {
  const onHero = useOnHero();
  return (
    <div
      id={id}
      className={cx(
        "text-[10px] font-extrabold tracking-[0.15em] uppercase",
        onHero ? "text-nav-accent" : muted ? "text-muted-foreground" : "text-primary",
        rule && "flex items-center gap-2.5",
      )}
    >
      {rule && <span aria-hidden="true" className="block h-0.5 w-5 bg-primary" />}
      {children}
    </div>
  );
}
