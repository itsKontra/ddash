import { definePlugin } from "@ddash/plugin";
import "./index.css";
import { HelperPage } from "./page";

export default definePlugin({
  // Label (AI App helper) and icon (wand-sparkles) come from plugin.yaml.
  nav: { order: 80 },
  pages: [{ index: true, Component: HelperPage }],
});
