import { definePlugin } from "@ddash/plugin";
import { Containers } from "./containers";
import "./index.css";
import { containers } from "./widget";

export default definePlugin({
  // Label and icon (container) come from plugin.yaml.
  nav: { order: 50 },
  pages: [{ index: true, Component: Containers }],
  widgets: [containers],
});
