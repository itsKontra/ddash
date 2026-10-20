import { definePlugin } from "@ddash/plugin";
import "./index.css";
import { adguard } from "./adguard";
import { DeviceDetail } from "./device";
import { useNewBadge } from "./devices";
import { devices } from "./devices-widget";
import { Network } from "./network";
import { throughput } from "./throughput";
import { vpn } from "./vpn";

export default definePlugin({
  // Label and icon (network) come from plugin.yaml.
  nav: { order: 30, useBadge: useNewBadge },
  pages: [
    { index: true, Component: Network },
    { path: "devices/:mac", Component: DeviceDetail },
  ],
  widgets: [vpn, throughput, devices, adguard],
});
