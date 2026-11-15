import type { Status, Widget } from "@ddash/plugin";
import { Row, Rows } from "@ddash/ui";
import { onlineFact, tally, useDevices } from "./devices";

/** Up with the counts and the online Devices as its fact; New Devices are news, not trouble. Warn when network can't list them. */
function useStatus(): Status | undefined {
  const { data, error } = useDevices();
  if (error) return { tone: "warn", summary: `Devices unavailable: ${error.rawMessage}` };
  if (!data) return undefined;
  const n = tally(data.devices);
  const parts = [`${n.total} Devices`, `${n.online} online`];
  if (n.new) parts.push(`${n.new} new`);
  return { tone: "up", summary: parts.join(", "), fact: onlineFact(data.devices) };
}

/** S: Devices, online and new, as rows. */
function Body() {
  const { data, error } = useDevices();
  if (error && !data) return null;
  const n = data && tally(data.devices);
  return (
    <Rows>
      <Row label="Devices" value={n?.total} />
      <Row label="Online" value={n && <span className="text-up-text">{n.online}</span>} />
      <Row label="New" value={n && <span className={n.new ? "text-warn-text" : undefined}>{n.new}</span>} />
    </Rows>
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
