import type { Device } from "@ddash/proto/network/network_pb";
import { DataTable, EmptyState, Notice, Panel, Pill, StatusDot, type Column } from "@ddash/ui";
import { useState, type ReactNode } from "react";
import { Link } from "react-router";
import { AdBlocking } from "./adblocking";
import { DeviceSheet } from "./adopt";
import { detailPath, nameOf, tally, useDevices } from "./devices";
import { DeviceStrip } from "./proto-strip";

/** The Devices section's header aside: how many are New. */
export function NewCount() {
  const { data } = useDevices();
  const n = data ? tally(data.devices).new : 0;
  return n ? <Pill tone="warn">{`${n} new`}</Pill> : null;
}

/** Adopted or New, as a pill. */
export function Adoption({ device: d }: { device: Device }) {
  return d.adopted ? <Pill tone="idle">Adopted</Pill> : <Pill tone="warn">New</Pill>;
}

/** Tunnelled or Direct, as a pill. */
export function Routing({ device: d }: { device: Device }) {
  return d.direct ? <Pill tone="idle">Direct</Pill> : <Pill tone="up">Tunnelled</Pill>;
}

const columns: Column<Device>[] = [
  { id: "name", header: "Name", rowHeader: true, cell: (d) => <Link to={detailPath(d)}>{nameOf(d)}</Link> },
  { id: "online", header: "Online", cell: (d) => <StatusDot tone={d.online ? "up" : "idle"} label={d.online ? "online" : "offline"} /> },
  { id: "ip", header: "IP", mono: true, cell: (d) => d.ip },
  { id: "mac", header: "MAC", mono: true, cell: (d) => d.mac },
  { id: "adoption", header: "Address", cell: (d) => <Adoption device={d} /> },
  {
    id: "routing",
    header: "Routing",
    cell: (d) => (
      <span className="inline-flex items-center gap-2">
        <Routing device={d} />
        <AdBlocking device={d} />
      </span>
    ),
  },
];

/** The Devices section: every Device with its routing and status; each name opens its detail, and its ⋯ Menu Adopts a New one or renames an Adopted one. */
export function DevicesTable({ aside }: { aside?: ReactNode }) {
  const { data, error } = useDevices();
  const [sheet, setSheet] = useState<Device>();
  const failed = error && !data;
  const none = data?.devices.length === 0;
  return (
    <Panel title="Devices" actions={aside} flush>
      {failed ? (
        <Notice tone="down">{error.rawMessage}</Notice>
      ) : none ? (
        <EmptyState icon="network" title="No Devices">
          The firewall's DHCP knows no Devices.
        </EmptyState>
      ) : (
        <DataTable
          rows={data?.devices ?? []}
          rowKey={(d) => d.mac}
          columns={columns}
          loading={!data}
          strip={(d) => (
            <DeviceStrip device={d} bypassAlias={data?.bypassAlias ?? ""} adguardError={data?.adguardError ?? ""} openSheet={() => setSheet(d)} />
          )}
        />
      )}
      {sheet && <DeviceSheet device={sheet} onClose={() => setSheet(undefined)} />}
    </Panel>
  );
}
