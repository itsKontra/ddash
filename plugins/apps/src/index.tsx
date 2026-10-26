import { definePlugin } from "@ddash/plugin";
import { useWarningsBadge } from "./apps";
import "./index.css";
import { AppsPage } from "./page";
import { apps } from "./widget";

export default definePlugin({
  // Label and icon (layout-grid) come from plugin.yaml.
  nav: { order: 10, useBadge: useWarningsBadge },
  pages: [{ index: true, Component: AppsPage }],
  widgets: [apps],
});
