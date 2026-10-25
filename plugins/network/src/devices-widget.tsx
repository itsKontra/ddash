import type { Status, Widget } from "@ddash/plugin";
import { MetricTile } from "@ddash/ui";
import { tally, useDevices } from "./devices";

/** Up with the counts; New Devices are news, not trouble. Warn when network can't list them. */
function useStatus(): Status | undefined {
  const { data, error } = useDevices();
  if (error) return { tone: "warn", summary: `Devices unavailable: ${error.rawMessage}` };
  if (!data) return undefined;
  const n = tally(data.devices);
  const parts = [`${n.total} Devices`, `${n.online} online`];
  if (n.new) parts.push(`${n.new} new`);
  return { tone: "up", summary: parts.join(", ") };
}

/** S: Devices, online and new. */
function Body() {
  const { data, error } = useDevices();
  if (error && !data) return null;
  const n = data && tally(data.devices);
  return (
    <div className="grid grid-cols-3 gap-3">
      <MetricTile label="Devices" value={n?.total} />
      <MetricTile label="online" value={n?.online} tone="up" />
      <MetricTile label="new" value={n?.new} tone={n?.new ? "warn" : "idle"} />
    </div>
  );
}

/** The Overview Widget: how many Devices, online and New. */
export const devices: Widget = {
  id: "devices",
  title: "Devices",
  sizes: ["S"],
  defaultSize: "S",
  Body,
  useStatus,
};
