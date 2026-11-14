import type { Status, Widget, WidgetSize } from "@ddash/plugin";
import { Facts, MetricTile } from "@ddash/ui";
import { tunnelLook, usePublicHostnames, useTunnel } from "./tunnel";

const plural = (n: number, one: string) => `${n} ${one}${n === 1 ? "" : "s"}`;

/** The Tunnel's state with its counts; idle while not set up, warn while cloudflare-api can't read it. */
function useStatus(): Status | undefined {
  const { tunnel, error, notSetUp } = useTunnel();
  const list = usePublicHostnames();
  if (notSetUp) return { tone: "idle", summary: "Not set up" };
  if (error && !tunnel) return { tone: "warn", summary: `cloudflare-api unavailable: ${error.rawMessage}` };
  if (!tunnel) return undefined;
  const look = tunnelLook[tunnel.status];
  const parts = [look.label, plural(tunnel.connectors.length, "connector")];
  if (list.data) parts.push(plural(list.data.publicHostnames.filter((p) => p.enabled).length, "Public Hostname"));
  return { tone: look.tone, summary: parts.join(" · ") };
}

/** S: the Public Hostnames and connectors. M: the Tunnel's name and state, and the disabled ones, too. */
function Body({ size }: { size: WidgetSize }) {
  const { tunnel, error, notSetUp } = useTunnel();
  const list = usePublicHostnames();
  // Not set up or unavailable: the Widget's status says so.
  if (notSetUp || (error && !tunnel)) return null;
  const hostnames = list.data?.publicHostnames;
  const enabled = hostnames?.filter((p) => p.enabled).length;
  return (
    <div className="flex flex-col gap-3">
      <div className="grid grid-cols-2 gap-3">
        <MetricTile label="Public Hostnames" value={enabled} tone="up" />
        <MetricTile label="connectors" value={tunnel?.connectors.length} tone={tunnel ? tunnelLook[tunnel.status].tone : undefined} />
      </div>
      {size === "M" && tunnel && (
        <Facts
          items={[
            { label: "Tunnel", value: `${tunnel.name}: ${tunnelLook[tunnel.status].label}` },
            { label: "Disabled", value: hostnames ? String(hostnames.length - (enabled ?? 0)) : "–" },
          ]}
        />
      )}
    </div>
  );
}

/** The Overview Widget: the Cloudflare Tunnel's state and how many Public Hostnames it serves. */
export const tunnel: Widget = {
  id: "tunnel",
  title: "Cloudflare Tunnel",
  sizes: ["S", "M"],
  defaultSize: "S",
  Body,
  useStatus,
};
