import { definePlugin } from "@ddash/plugin";
import { AgendaPage } from "./agenda";
import "./index.css";
import { nextEvents } from "./widget";

export default definePlugin({
  // Label (Calendar) and icon (calendar) come from plugin.yaml.
  nav: { order: 70 },
  pages: [{ index: true, Component: AgendaPage }],
  widgets: [nextEvents],
});
