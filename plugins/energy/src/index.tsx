import { definePlugin } from "@ddash/plugin";
import "./index.css";
import { EnergyPage } from "./page";
import { energyNow } from "./widget";

export default definePlugin({
  // Label (Energy) and icon (sun) come from plugin.yaml.
  nav: { order: 65 },
  pages: [{ index: true, Component: EnergyPage }],
  widgets: [energyNow],
});
