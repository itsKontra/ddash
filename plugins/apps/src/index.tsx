import { definePlugin } from "@ddash/plugin";
import "./index.css";
import { AppsPage } from "./page";
import { apps } from "./widget";

export default definePlugin({
  // Label and icon (layout-grid) come from plugin.yaml.
  nav: { order: 10 },
  pages: [{ index: true, Component: AppsPage }],
  widgets: [apps],
});
