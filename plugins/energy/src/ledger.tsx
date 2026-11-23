import type { Prices, Totals } from "@ddash/proto/energy-api/energy_api_pb";
import { cx, Icon, type IconName, type Tone } from "@ddash/ui";
import { eur, kwh, pct } from "./energy";

/** A price per kWh in cents: "31.24 ct". */
const cents = (v: number) => `${new Intl.NumberFormat(undefined, { maximumFractionDigits: 3 }).format(v * 100)} ct`;

/** What the period's money adds up to. */
export interface Money {
  /** Grid cost less feed-in revenue: the bill, or below zero what the feed-in earned over it. */
  bill: number;
  /** Feed-in paid more than the grid cost. */
  earned: boolean;
  /** The share of the grid cost the feed-in paid, up to 1; none without a cost. */
  covered?: number;
  /** The savings not spent on the grid, by using solar and battery energy. */
  notBought: number;
  /** The kWh of its own the house used, as the savings count them. */
  ownUse: number;
  /** What buying all the use from the grid would have cost. */
  withoutSolar: number;
}

/** The period's money, from its totals and the prices: the savings split into not bought and feed-in, which they include. */
export function money(t: Totals, prices: Prices): Money {
  const bill = t.cost - t.revenue;
  const notBought = Math.max(0, t.savings - t.revenue);
  return {
    bill,
    earned: bill < 0,
    covered: t.cost > 0 ? Math.min(1, t.revenue / t.cost) : undefined,
    notBought,
    ownUse: prices.buy > 0 ? notBought / prices.buy : 0,
    withoutSolar: t.consumption * prices.buy,
  };
}

/** One line of the ledger: what, how it was worked out, and the sum. */
function Line({ icon, tone, label, detail, value }: { icon: IconName; tone: Tone; label: string; detail: string; value: string }) {
  return (
    <div className="flex items-center gap-3 py-2.5">
      <Icon name={icon} size="sm" tone={tone} />
      <div className="min-w-0 flex-1">
        <div className="text-sm">{label}</div>
        <div className="text-xs text-muted-foreground tabular-nums">{detail}</div>
      </div>
      <div className={cx("text-sm font-bold tabular-nums", tone === "up" && "text-up-text")}>{value}</div>
    </div>
  );
}

/**
 * The period's money as a ledger: bought less sold makes the grid bill
 * (or, below zero, what the feed-in earned net), a bar of how much of the
 * grid cost the feed-in covered, and the savings split into what wasn't
 * bought and what the feed-in paid.
 */
export function MoneyLedger({ t, prices }: { t: Totals; prices: Prices }) {
  const m = money(t, prices);
  const whole = Math.max(t.cost, t.revenue);
  const paid = Math.min(t.cost, t.revenue);
  return (
    <div className="flex flex-col gap-5">
      <div>
        <div className="divide-y divide-border">
          <Line icon="arrow-down-left" tone="idle" label="Bought from the grid" detail={`${kwh(t.gridImport)} × ${cents(prices.buy)}`} value={`− ${eur(t.cost)}`} />
          <Line icon="arrow-up-right" tone="up" label="Sold to the grid" detail={`${kwh(t.gridExport)} × ${cents(prices.sell)}`} value={`+ ${eur(t.revenue)}`} />
        </div>
        <div className="flex items-baseline justify-between gap-3 border-t-2 border-foreground/15 pt-3">
          <span className="text-sm font-bold">{m.earned ? "Earned net" : "Grid bill"}</span>
          <span className={cx("text-2xl font-extrabold tracking-tight tabular-nums", m.earned && "text-up-text")}>
            {eur(Math.abs(m.bill))}
          </span>
        </div>
      </div>
      {whole > 0 && (
        <div className="flex flex-col gap-2">
          <div
            role="img"
            aria-label={m.earned ? "The feed-in paid the whole grid cost, and more" : `The feed-in covered ${m.covered === undefined ? "none" : pct(m.covered)} of the grid cost`}
            className="flex h-2.5 gap-0.5 overflow-hidden rounded-full bg-muted"
          >
            {paid > 0 && <div className="rounded-l-full bg-up" style={{ width: `${(paid / whole) * 100}%` }} />}
            {m.bill !== 0 && <div className={cx("rounded-r-full", m.earned ? "bg-up/40" : "bg-warn", paid <= 0 && "rounded-l-full")} style={{ width: `${(Math.abs(m.bill) / whole) * 100}%` }} />}
          </div>
          <div className="flex flex-wrap justify-between gap-x-4 gap-y-1 text-xs text-muted-foreground">
            <span className="flex items-center gap-1.5">
              <span aria-hidden="true" className="size-2.5 rounded-ui-sm bg-up" />
              Covered by feed-in <b className="text-foreground tabular-nums">{m.covered === undefined ? "–" : pct(m.covered)}</b>
            </span>
            <span className="flex items-center gap-1.5">
              <span aria-hidden="true" className={cx("size-2.5 rounded-ui-sm", m.earned ? "bg-up/40" : "bg-warn")} />
              {m.earned ? "Earned over the cost" : "Left to pay"} <b className="text-foreground tabular-nums">{eur(Math.abs(m.bill))}</b>
            </span>
          </div>
        </div>
      )}
      <div className="flex flex-col gap-2">
        <div className="flex items-center gap-3 rounded-ui-sm bg-up-tint px-4 py-3">
          <Icon name="piggy-bank" tone="up" />
          <div className="min-w-0 flex-1 text-xs text-muted-foreground">
            <b className="text-foreground tabular-nums">{eur(m.notBought)}</b> not bought by using {kwh(m.ownUse)} of your own, and <b className="text-foreground tabular-nums">{eur(t.revenue)}</b> from feed-in
          </div>
          <div className="text-right">
            <div className="text-lg leading-tight font-extrabold text-up-text tabular-nums">{eur(t.savings)}</div>
            <div className="text-xs text-muted-foreground">saved</div>
          </div>
        </div>
        <p className="text-xs text-muted-foreground">
          Buying all {kwh(t.consumption)} from the grid would have cost <b className="text-foreground tabular-nums">{eur(m.withoutSolar)}</b>.
        </p>
      </div>
    </div>
  );
}
