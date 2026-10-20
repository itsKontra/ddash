import { useQuery, type Badge, type Tone } from "@ddash/plugin";
import { ChecksService, State, type Check } from "@ddash/proto/checks/checks_pb";

/** Every Check with its state, refreshed on the getter's FAST tier. */
export function useChecks() {
  return useQuery(ChecksService.method.listChecks, {});
}

/** A Check's name, or its target when it has none. */
export const shownName = (c: Check) => c.spec?.name || c.spec?.target || `Check ${c.id}`;

/**
 * How a Check reads at a glance: disabled and Maintenance before its state;
 * a Flapping Check that is up is warn.
 */
export function toneOf(c: Check): Tone {
  if (c.disabled) return "idle";
  if (c.maintenance) return "maint";
  switch (c.state) {
    case State.UP:
      return c.flapping ? "warn" : "up";
    case State.DOWN:
      return "down";
    default:
      return "warn";
  }
}

/** The word for a Check's tone. */
export function stateLabel(c: Check): string {
  if (c.disabled) return "disabled";
  if (c.maintenance) return "Maintenance";
  switch (c.state) {
    case State.UP:
      return c.flapping ? "up, flapping" : "up";
    case State.DOWN:
      return c.flapping ? "down, flapping" : "down";
    default:
      return "pending";
  }
}

/**
 * The enabled Checks, split by tone. Pending ones are only in `enabled`.
 * `up` holds every up Check outside Maintenance; `warn` those of them that
 * are Flapping or whose certificate is expiring.
 */
export function tally(checks: readonly Check[]) {
  const enabled = checks.filter((c) => !c.disabled);
  const by = (tone: Tone) => enabled.filter((c) => toneOf(c) === tone);
  const up = enabled.filter((c) => !c.maintenance && c.state === State.UP);
  return { enabled, up, warn: up.filter((c) => c.flapping || c.certExpiring), down: by("down"), maint: by("maint") };
}

/** Why an up Check warns: "nas flapping", "Jellyfin certificate expiring". */
export const warning = (c: Check) => `${shownName(c)} ${c.flapping ? "flapping" : "certificate expiring"}`;

/** The Nav Entry's badge: how many Checks are down, or none. */
export function useDownBadge(): Badge | null {
  const { data } = useChecks();
  const down = data ? tally(data.checks).down.length : 0;
  return down ? { count: down, tone: "down" } : null;
}

/** A latency in ms as people read it. */
export function latency(ms: number): string {
  return ms < 1 ? "<1 ms" : `${Math.round(ms)} ms`;
}
