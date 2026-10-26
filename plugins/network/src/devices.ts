import { useQuery, type Badge } from "@ddash/plugin";
import { NetworkService, type Device } from "@ddash/proto/network/network_pb";

/** network's Device join, shared by the Page, the detail, the badge and the Widget. */
export function useDevices() {
  return useQuery(NetworkService.method.listDevices, {});
}

/** How many Devices there are, are online and are New. */
export function tally(devices: readonly Device[]) {
  return {
    total: devices.length,
    online: devices.filter((d) => d.online).length,
    new: devices.filter((d) => !d.adopted).length,
  };
}

/** The Nav Entry's "N new" while there are New Devices. */
export function useNewBadge(): Badge | null {
  const { data } = useDevices();
  const n = data ? tally(data.devices).new : 0;
  return n ? { tone: "warn", text: `${n} new` } : null;
}

/** A Device's name, or its MAC when it has none. */
export const nameOf = (d: Device) => d.name || d.mac;

/** The detail page's path, relative to the Plugin's root. */
export const detailPath = (d: Device) => `devices/${d.mac}`;
