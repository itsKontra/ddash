import { definePlugin } from "@ddash/plugin";
import "./index.css";
import { Network } from "./network";
import { vpn } from "./vpn";

export default definePlugin({
  // Label and icon (network) come from plugin.yaml.
  nav: { order: 30 },
  pages: [{ index: true, Component: Network }],
  widgets: [vpn],
});
