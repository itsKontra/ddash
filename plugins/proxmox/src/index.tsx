import { definePlugin } from "@ddash/plugin";
import "./index.css";
import { useCommands } from "./commands";
import { useConcerns } from "./concerns";
import { WorkloadDetail } from "./detail";
import { SSHKeys } from "./ssh-keys";
import { guests, hosts } from "./widget";
import { Workloads } from "./workloads";

export default definePlugin({
  // Label and icon (server) come from plugin.yaml.
  nav: { order: 40 },
  pages: [
    { index: true, Component: Workloads },
    { path: "workloads/:vmid", Component: WorkloadDetail },
    { path: "ssh-keys", Component: SSHKeys },
  ],
  widgets: [hosts, guests],
  useConcerns,
  useCommands,
});
