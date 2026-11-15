import type { Command } from "@ddash/plugin";
import type { App } from "@ddash/proto/apps/apps_pb";
import { useApps, visible } from "./apps";

/** "Open <App>" for each App the Widget shows, opening its URL in a new tab as the Widget's icon does. */
export function openCommands(apps: readonly App[], open: (url: string) => void): Command[] {
  return visible(apps).map((a) => ({
    id: `open:${a.id}`,
    title: `Open ${a.name}`,
    hint: URL.parse(a.url)?.hostname ?? a.url,
    icon: { app: a.name, icon: a.icon, url: a.url },
    keywords: [a.group, ...a.aliases].filter(Boolean),
    run: () => open(a.url),
  }));
}

const inNewTab = (url: string) => void window.open(url, "_blank", "noopener,noreferrer");

/** The apps Plugin's Commands for the command palette; undefined until ListApps answered. */
export function useCommands(): Command[] | undefined {
  const { list } = useApps();
  return list && openCommands(list.apps, inNewTab);
}
