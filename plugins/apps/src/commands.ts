import type { Command } from "@ddash/plugin";
import type { App } from "@ddash/proto/apps/apps_pb";
import { useApps, visible } from "./apps";
import { pinned } from "./pins";

/**
 * "Open <App>" for each App the Widget shows, opening its URL in a new tab
 * as the Widget's icon does: the pinned ones first, in the Operator's
 * order and marked pinned, then the others in list order.
 */
export function openCommands(apps: readonly App[], open: (url: string) => void): Command[] {
  const pins = pinned(apps);
  const rest = visible(apps).filter((a) => !pins.includes(a));
  return [...pins, ...rest].map((a) => ({
    id: `open:${a.id}`,
    title: `Open ${a.name}`,
    hint: URL.parse(a.url)?.hostname ?? a.url,
    icon: { app: a.name, icon: a.icon, url: a.url },
    keywords: [a.group, ...a.aliases].filter(Boolean),
    pinned: a.pin > 0,
    run: () => open(a.url),
  }));
}

const inNewTab = (url: string) => void window.open(url, "_blank", "noopener,noreferrer");

/** The apps Plugin's Commands for the command palette; undefined until ListApps answered. */
export function useCommands(): Command[] | undefined {
  const { list } = useApps();
  return list && openCommands(list.apps, inNewTab);
}
