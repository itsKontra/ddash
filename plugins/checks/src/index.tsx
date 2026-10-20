import { definePlugin } from "@ddash/plugin";
import { useDownBadge } from "./checks";
import { ChecksPage } from "./page";
import "./index.css";
import { summary } from "./widget";

export default definePlugin({
  // Label and icon (activity) come from plugin.yaml.
  nav: { order: 20, useBadge: useDownBadge },
  // ":id" is where a Check's Notification links: the Page with its detail open.
  pages: [
    { index: true, Component: ChecksPage },
    { path: ":id", Component: ChecksPage },
  ],
  widgets: [summary],
});
