import type { Device } from "@ddash/proto/network/network_pb";
import { Badge, StatusDot } from "@ddash/ui";
import { Link } from "react-router";
import { DeviceAction } from "./adopt";
import { detailPath, nameOf, tally, useDevices } from "./devices";

/** The Devices section's header aside: how many are New. */
export function NewCount() {
  const { data } = useDevices();
  const n = data ? tally(data.devices).new : 0;
  return n ? <Badge tone="warn">{`${n} new`}</Badge> : null;
}

/** Adopted or New, as a pill. */
export function Adoption({ device: d }: { device: Device }) {
  return d.adopted ? <Badge tone="idle">Adopted</Badge> : <Badge tone="warn">New</Badge>;
}

/** Tunnelled or Direct, as a pill. */
export function Routing({ device: d }: { device: Device }) {
  return d.direct ? <Badge tone="idle">Direct</Badge> : <Badge tone="up">Tunnelled</Badge>;
}

const cell = "px-2 py-2";

/** Every Device with its routing and status; each name opens its detail, and a New one has Adopt. */
export function DevicesTable() {
  const { data, error } = useDevices();
  if (error && !data) return <p className="px-4 py-3 text-sm text-down">{error.rawMessage}</p>;
  if (!data) return null;
  if (data.devices.length === 0) return <p className="px-4 py-3 text-sm text-muted-foreground">The firewall's DHCP knows no Devices.</p>;
  return (
    <table aria-label="Devices" className="w-full text-sm">
      <thead>
        <tr className="border-b border-border text-left text-xs text-muted-foreground">
          <th scope="col" className="w-6 py-2 pl-4">
            <span className="sr-only">Online</span>
          </th>
          <th scope="col" className={`${cell} font-medium`}>
            Name
          </th>
          <th scope="col" className={`${cell} font-medium`}>
            IP
          </th>
          <th scope="col" className={`${cell} font-medium`}>
            MAC
          </th>
          <th scope="col" className={`${cell} font-medium`}>
            <span className="sr-only">Adopted or New</span>
          </th>
          <th scope="col" className={`${cell} pr-4 font-medium`}>
            Routing
          </th>
        </tr>
      </thead>
      <tbody>
        {data.devices.map((d) => (
          <tr key={d.mac} className="border-b border-border last:border-b-0">
            <td className="w-6 py-2 pl-4">
              <StatusDot tone={d.online ? "up" : "idle"} label={d.online ? "online" : "offline"} />
            </td>
            <th scope="row" className={`${cell} text-left font-medium`}>
              <Link to={detailPath(d)} className="hover:underline">
                {nameOf(d)}
              </Link>
            </th>
            <td className={`${cell} text-muted-foreground tabular-nums`}>{d.ip}</td>
            <td className={`${cell} text-xs text-muted-foreground tabular-nums`}>{d.mac}</td>
            <td className={cell}>
              <span className="inline-flex items-center gap-2">
                <Adoption device={d} />
                {!d.adopted && <DeviceAction device={d} />}
              </span>
            </td>
            <td className={`${cell} pr-4`}>
              <Routing device={d} />
            </td>
          </tr>
        ))}
      </tbody>
    </table>
  );
}
