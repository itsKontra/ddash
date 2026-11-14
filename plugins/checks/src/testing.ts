/** The checks Plugin's scope in tests. */
export const checks = { name: "checks", requires: ["checks", "notify"] };

/** notify's channels in Connect JSON: ntfy set up with every category, Telegram not set up. */
export const channels = {
  channels: [
    {
      name: "ntfy",
      title: "ntfy",
      component: "ntfy-api",
      setUp: true,
      destination: "ntfy.sh/ddash-alerts",
      categories: ["CATEGORY_CHECKS", "CATEGORY_IMAGE_UPDATES", "CATEGORY_ACTION_OUTCOMES"],
    },
    { name: "telegram", title: "Telegram", component: "telegram-api", categories: ["CATEGORY_CHECKS", "CATEGORY_IMAGE_UPDATES", "CATEGORY_ACTION_OUTCOMES"] },
  ],
};

/** A Check as checks answers it in Connect JSON, a TCP one unless extra says otherwise. */
export function check(id: number, name: string, state: string, extra: Record<string, unknown> = {}) {
  return { id: String(id), spec: { name, type: "CHECK_TYPE_TCP", target: `${name}.home.example:22` }, state, ...extra };
}
