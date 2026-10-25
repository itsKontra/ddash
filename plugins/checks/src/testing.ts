/** The checks Plugin's scope in tests. */
export const checks = { name: "checks", requires: ["checks"] };

/** A Check as checks answers it in Connect JSON, a TCP one unless extra says otherwise. */
export function check(id: number, name: string, state: string, extra: Record<string, unknown> = {}) {
  return { id: String(id), spec: { name, type: "CHECK_TYPE_TCP", target: `${name}.home.example:22` }, state, ...extra };
}
