import { definePlugin } from "@ddash/plugin";
import "./index.css";
import { WorkloadDetail } from "./detail";
import { guests } from "./widget";
import { Workloads } from "./workloads";

export default definePlugin({
  // Label and icon (server) come from plugin.yaml.
  nav: { order: 40 },
  pages: [
    { index: true, Component: Workloads },
    { path: "workloads/:vmid", Component: WorkloadDetail },
  ],
  widgets: [guests],
});
