import { definePlugin } from "@ddash/plugin";
import "./index.css";
// PROTOTYPE: the split's Apps Page variants (wayfinder #139) in place of AppsPage.
import { ProtoApps, ProtoDetail } from "./proto-split";
import { apps } from "./widget";

export default definePlugin({
  // Label and icon (layout-grid) come from plugin.yaml.
  nav: { order: 10 },
  pages: [
    { index: true, Component: ProtoApps },
    { path: ":id", Component: ProtoDetail },
  ],
  widgets: [apps],
});
