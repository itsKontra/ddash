import { timestampFromMs } from "@bufbuild/protobuf/wkt";
import { ago } from "@ddash/display";
import { useConfirm, type Concern } from "@ddash/plugin";
import { InternetPath } from "@ddash/proto/network/network_pb";
import { useState } from "react";
import { AdoptSheet } from "./adopt";
import { nameOf, useDevices } from "./devices";
import { useSetInternetPath } from "./internet-path";

/**
 * When a lease started, from pfSense's "2026/11/15 08:30:00" in the
 * firewall's time (taken as the browser's); undefined when it doesn't say.
 */
export function leaseStart(starts: string | undefined): number | undefined {
  const m = /^(\d{4})\/(\d{2})\/(\d{2}) (\d{2}):(\d{2}):(\d{2})$/.exec(starts ?? "");
  if (!m) return undefined;
  const [y, mo, d, h, mi, s] = m.slice(1).map(Number);
  return new Date(y, mo - 1, d, h, mi, s).getTime();
}

/**
 * What needs the Operator in network: each New Device, with its MAC,
 * address, the hostname it sent and when its lease started, and the
 * Page's Adopt (its Sheet, asking for a name) and No Internet as Block,
 * which isn't drawn as a fix. Block is there only with a block alias. One
 * click beside Adopt both Adopts the Device and cuts its Internet, so
 * unlike the Page's picker it asks first, naming the Device.
 */
export function useConcerns(): Concern[] | undefined {
  const { data, error, dataUpdatedAt } = useDevices();
  const block = useSetInternetPath();
  const ask = useConfirm();
  // The Device Block last ran for, whose row shows its pending and error.
  const [blocking, setBlocking] = useState<string>();
  if (!data) return error ? [] : undefined;
  return data.devices
    .filter((d) => !d.adopted)
    .map((d): Concern => {
      const started = leaseStart(d.lease?.starts);
      const mine = blocking === d.mac;
      return {
        id: d.mac,
        tone: "info",
        subject: nameOf(d),
        title: "New device on the network",
        cause: [d.mac, d.ip, d.name, started !== undefined && `leased ${ago(timestampFromMs(started), dataUpdatedAt)}`].filter(Boolean).join(" · "),
        icon: "circle-question-mark",
        href: `/network/devices/${d.mac}`,
        actions: [
          { label: "Adopt", icon: "badge-check", sheet: (close) => <AdoptSheet device={d} onClose={close} /> },
          ...(data.blockAlias
            ? [
                {
                  label: "Block",
                  icon: "ban" as const,
                  secondary: true,
                  pending: mine && block.pending,
                  error: mine ? block.error?.rawMessage : undefined,
                  run: async () => {
                    const which = [d.ip, d.name].filter(Boolean).join(", ");
                    const yes = await ask({
                      title: `Block ${nameOf(d)}?`,
                      text: `network Adopts ${d.mac} (${which}) at the lowest free reserved address and adds it to ${data.blockAlias}: it loses Internet access.`,
                      action: "Block",
                    });
                    if (!yes) return;
                    setBlocking(d.mac);
                    return block.run({ mac: d.mac, path: InternetPath.NO_INTERNET });
                  },
                },
              ]
            : []),
        ],
      };
    });
}
