import { definePlugin, type Badge } from "@ddash/plugin";
import { TunnelStatus } from "@ddash/proto/cloudflare-api/cloudflare_api_pb";
import "./index.css";
import { TunnelPage } from "./page";
import { useTunnel } from "./tunnel";
import { tunnel } from "./widget";

/** "!" while the Tunnel is down or degraded. */
function useBadge(): Badge | null {
  const { tunnel } = useTunnel();
  switch (tunnel?.status) {
    case TunnelStatus.DOWN:
    case TunnelStatus.INACTIVE:
      return { tone: "down", text: "!" };
    case TunnelStatus.DEGRADED:
      return { tone: "warn", text: "!" };
  }
  return null;
}

export default definePlugin({
  // Label and icon (cloud) come from plugin.yaml.
  nav: { order: 35, useBadge },
  pages: [{ index: true, Component: TunnelPage }],
  widgets: [tunnel],
});
