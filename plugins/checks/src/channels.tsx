import { useQuery } from "@ddash/plugin";
import { ChecksService } from "@ddash/proto/checks/checks_pb";
import { Category, type Channel } from "@ddash/proto/ddash/notification/notification_pb";
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

/** Where a channel delivers to and what it gets, as far as it can say. */
const reach = (c: Channel) => [c.destination, c.destination && `gets ${takes(c)}`].filter(Boolean).join(" · ");

/** One channel as the list shows it: whether Checks' Notifications reach it, where, and its last problem. */
function channelItem(c: Channel): StatusItem {
  const checks = c.categories.includes(Category.CHECKS);
  if (c.problem) return { tone: "warn", title: c.title, meta: [reach(c), c.problem].filter(Boolean).join(" · "), aside: "problem" };
  return {
    tone: checks ? "up" : "idle",
    title: c.title,
    meta: reach(c),
    aside: checks ? "gets Checks" : "Checks filtered out",
  };
}

const pluginsLink = (
  <Link to="/plugins" className="font-semibold text-primary hover:underline">
    Plugins
  </Link>
);

/**
 * Where Checks' Notifications go: each channel the core delivers to (an
 * installed channel Plugin), its destination, the categories it takes and
 * its last problem. Says so when none is installed, and warns when none
 * takes Checks.
 */
export function ChannelList() {
  const { data, error } = useQuery(ChecksService.method.listChannels, {});
  if (!data && !error) return null;
  const none = data?.channels.length === 0;
  const reached = data?.channels.some((c) => c.categories.includes(Category.CHECKS));
  return (
    <Panel
      title="Notifications"
      description={<>Install ntfy or Telegram in {pluginsLink}, and pick which Notifications each gets in its Component's config there.</>}
    >
      <div className="flex flex-col gap-3">
        {error && !data && <Notice tone="warn">Channels unknown: {error.rawMessage}</Notice>}
        {none && (
          <Notice tone="warn" title="No channel installed">
            A Check going down reaches no one. Install a channel, such as ntfy or Telegram, in {pluginsLink}.
          </Notice>
        )}
        {data && !none && !reached && (
          <Notice tone="warn" title="No channel gets Checks' Notifications">
            A Check going down reaches no one until a channel takes Checks.
          </Notice>
        )}
        {data && !none && <StatusList items={data.channels.map(channelItem)} />}
      </div>
    </Panel>
  );
}
