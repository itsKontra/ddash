import { definePlugin } from "@ddash/plugin";
import { ReverseProxy } from "./page";
import "./index.css";
import { certificates, useBadge } from "./widget";

export default definePlugin({
  // Label and icon (waypoints) come from plugin.yaml.
  nav: { order: 60, useBadge },
  pages: [{ index: true, Component: ReverseProxy }],
  widgets: [certificates],
});
