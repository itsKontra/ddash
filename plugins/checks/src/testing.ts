/** The checks Plugin's scope in tests. */
export const checks = { name: "checks", requires: ["checks"] };

/** The installed channels as checks answers ListChannels in Connect JSON: ntfy with every category, Telegram without image updates. */
export const channels = {
  channels: [
    {
      component: "ntfy-api",
      title: "ntfy",
      destination: "ntfy.sh/ddash-alerts",
      categories: ["CATEGORY_CHECKS", "CATEGORY_IMAGE_UPDATES", "CATEGORY_ACTION_OUTCOMES"],
    },
    { component: "telegram-api", title: "Telegram", destination: "chat 123456789", categories: ["CATEGORY_CHECKS", "CATEGORY_ACTION_OUTCOMES"] },
  ],
};

/** A Check as checks answers it in Connect JSON, a TCP one unless extra says otherwise. */
export function check(id: number, name: string, state: string, extra: Record<string, unknown> = {}) {
  return { id: String(id), spec: { name, type: "CHECK_TYPE_TCP", target: `${name}.home.example:22` }, state, ...extra };
}
