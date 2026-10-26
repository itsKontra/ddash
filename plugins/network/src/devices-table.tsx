import type { Device } from "@ddash/proto/network/network_pb";
import { DataTable, EmptyState, matches, Notice, Panel, Pill, SearchField, StatusDot, Tabs, useQueryState, type Column, type TabOption } from "@ddash/ui";
import type { ReactNode } from "react";
import { Link } from "react-router";
import { AdBlocking } from "./adblocking";
import { DeviceStrip } from "./device-strip";
import { detailPath, nameOf, tally, useDevices } from "./devices";

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

/** An IPv4 address as a number, so 198.18.1.20 sorts before 198.18.1.150; one that isn't sorts last. */
function ipValue(ip: string): number {
  const parts = ip.split(".").map(Number);
  if (parts.length !== 4 || parts.some((p) => !Number.isInteger(p) || p < 0 || p > 255)) return Number.MAX_SAFE_INTEGER;
  return parts.reduce((n, p) => n * 256 + p, 0);
}

const columns: Column<Device>[] = [
  { id: "name", header: "Name", rowHeader: true, sortable: true, sortValue: nameOf, cell: (d) => <Link to={detailPath(d)}>{nameOf(d)}</Link> },
  {
    id: "online",
    header: "Online",
    sortable: true,
    sortValue: (d) => (d.online ? 0 : 1),
    cell: (d) => <StatusDot tone={d.online ? "up" : "idle"} label={d.online ? "online" : "offline"} />,
  },
  { id: "ip", header: "IP", mono: true, sortable: true, sortValue: (d) => ipValue(d.ip), cell: (d) => d.ip },
  { id: "mac", header: "MAC", mono: true, sortable: true, sortValue: (d) => d.mac, cell: (d) => d.mac },
  // New first.
  { id: "address", header: "Address", sortable: true, sortValue: (d) => (d.adopted ? 1 : 0), cell: (d) => <Adoption device={d} /> },
  {
    id: "routing",
    header: "Routing",
    sortable: true,
    sortValue: (d) => (d.direct ? 1 : 0),
    cell: (d) => (
      <span className="inline-flex items-center gap-2">
        <Routing device={d} />
        <AdBlocking device={d} />
      </span>
    ),
  },
];

type TabId = "all" | "new" | "online" | "tunnelled" | "direct";

/** The table's Tabs, each with the Devices it shows. */
const tabNames: readonly { id: TabId; label: string; has: (d: Device) => boolean }[] = [
  { id: "all", label: "All", has: () => true },
  { id: "new", label: "New", has: (d) => !d.adopted },
  { id: "online", label: "Online", has: (d) => d.online },
  { id: "tunnelled", label: "Tunnelled", has: (d) => !d.direct },
  { id: "direct", label: "Direct", has: (d) => d.direct },
];

const tabOf = (id: string) => tabNames.find((t) => t.id === id) ?? tabNames[0];

/** Whether a Device matches the search: its name, IP, MAC or an alias holding it. */
const found = (q: string, d: Device) => matches(q, d.name, d.ip, d.mac, ...d.aliases.map((a) => a.name));

/** By name, the order rows a sort ties stay in. */
const byName = (a: Device, b: Device) => nameOf(a).localeCompare(nameOf(b), undefined, { numeric: true });

/**
 * The Devices section: every Device with its routing and status, searched,
 * sorted and split into Tabs under devices.* in the URL (the Page has other
 * tables). Each name opens its detail; each row's strip holds its Actions.
 */
export function DevicesTable({ aside }: { aside?: ReactNode }) {
  const { data, error } = useDevices();
  const [q, setQ] = useQueryState("devices.q", "");
  const [tab, setTab] = useQueryState("devices.tab", "all");
  const failed = error && !data;
  const none = data?.devices.length === 0;
  const shown = tabOf(tab);
  const searched = (data?.devices ?? []).filter((d) => found(q, d)).sort(byName);
  const tabs: TabOption<TabId>[] = tabNames.map((t) => ({ id: t.id, label: t.label, count: searched.filter(t.has).length }));
  return (
    <Panel
      title="Devices"
      actions={aside}
      flush
      toolbar={
        !failed &&
        !none && (
          <>
            <SearchField label="Search Devices" value={q} onChange={setQ} placeholder="Search by name, IP, MAC or alias" />
            <Tabs label="Show" value={shown.id} options={tabs} onChange={setTab} />
          </>
        )
      }
    >
      {failed ? (
        <Notice tone="down">{error.rawMessage}</Notice>
      ) : none ? (
        <EmptyState icon="network" title="No Devices">
          The firewall's DHCP knows no Devices.
        </EmptyState>
      ) : (
        <DataTable
          rows={searched.filter(shown.has)}
          rowKey={(d) => d.mac}
          columns={columns}
          sortKey="devices.sort"
          defaultSort="name"
          loading={!data}
          empty={q ? `No Devices match "${q}"` : `No ${shown.label} Devices`}
          rowLabel={nameOf}
          strip={(d) => data && <DeviceStrip device={d} bypassAlias={data.bypassAlias} adguardError={data.adguardError} />}
        />
      )}
    </Panel>
  );
}
