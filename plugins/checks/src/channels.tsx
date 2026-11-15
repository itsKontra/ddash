import { useQuery } from "@ddash/plugin";
import { Category, NotifyService, type Channel } from "@ddash/proto/notify/notify_pb";
import { Notice, Panel, StatusList, type StatusItem } from "@ddash/ui";
import { Link } from "react-router";

const categoryLabels: Record<Category, string> = {
  [Category.UNSPECIFIED]: "",
  [Category.CHECKS]: "Checks",
  [Category.IMAGE_UPDATES]: "image updates",
  [Category.ACTION_OUTCOMES]: "Action outcomes",
};

/** What a channel's filter takes, e.g. "Checks, image updates", or "nothing". */
const takes = (c: Channel) => c.categories.map((k) => categoryLabels[k]).join(", ") || "nothing";

/** One channel as the list shows it: whether Checks' Notifications reach it, and where. */
function channelItem(c: Channel): StatusItem {
  if (c.problem) return { tone: "warn", title: c.title, meta: c.problem, aside: "unknown" };
  if (!c.setUp) return { tone: "idle", title: c.title, meta: `Set it up in ${c.component}'s config.`, aside: "not set up" };
  const checks = c.categories.includes(Category.CHECKS);
  return {
    tone: checks ? "up" : "idle",
    title: c.title,
    meta: `${c.destination} · gets ${takes(c)}`,
    aside: checks ? "gets Checks" : "Checks filtered out",
  };
}

/**
 * Where Checks' Notifications go: each channel notify sends to, set up or
 * not, and whether its filter takes Checks. Warns when none does.
 */
export function ChannelList() {
  const { data, error } = useQuery(NotifyService.method.listChannels, {});
  if (!data && !error) return null;
  const reached = data?.channels.some((c) => c.setUp && c.categories.includes(Category.CHECKS));
  return (
    <Panel
      title="Notifications"
      description={
        <>
          Set up ntfy-api or telegram-api, and pick which Notifications each gets in notify's config, in{" "}
          <Link to="/plugins" className="font-semibold text-primary hover:underline">
            Plugins
          </Link>
          .
        </>
      }
    >
      <div className="flex flex-col gap-3">
        {error && !data && <Notice tone="warn">Channels unknown: {error.rawMessage}</Notice>}
        {data && !reached && (
          <Notice tone="warn" title="No channel gets Checks' Notifications">
            A Check going down reaches no one until a channel is set up and takes Checks.
          </Notice>
        )}
        {data && <StatusList items={data.channels.map(channelItem)} />}
      </div>
    </Panel>
  );
}
