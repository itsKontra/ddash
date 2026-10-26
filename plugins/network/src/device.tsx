import type { Device } from "@ddash/proto/network/network_pb";
import { EmptyState, Facts, Notice, PageTitle, Panel, Skeleton, StatusDot, type Fact } from "@ddash/ui";
import { useState } from "react";
import { useParams } from "react-router";
import { AdBlockingToggle } from "./adblocking";
import { DeviceAction, RenameForm } from "./adopt";
import { nameOf, useDevices } from "./devices";
import { Adoption } from "./devices-table";
import { DirectToggle } from "./direct";
import { DeviceQueries } from "./queries";

// The detail is the route devices/:mac under the Network Page's.
const back = { href: "..", label: "Network" };

/** A Device's detail (/network/devices/:mac): Adopt or rename in place, the Direct and ad-blocking toggles, the lease and reservation facts, the other aliases holding it (read-only) and its last AdGuard queries. */
export function DeviceDetail() {
  const { mac = "" } = useParams();
  const { data, error } = useDevices();
  if (!data) {
    return (
      <div>
        <PageTitle kicker="Device" title={mac} back={back} />
        {error ? <Notice tone="down">{error.rawMessage}</Notice> : <Skeleton height="xl" />}
      </div>
    );
  }
  const d = data.devices.find((x) => x.mac === mac.toLowerCase());
  if (!d) {
    return (
      <div>
        <PageTitle kicker="Device" title={mac} back={back} />
        <Panel>
          <EmptyState icon="search" title="No such Device">
            No Device has the MAC {mac}. It may have left the firewall's DHCP.
          </EmptyState>
        </Panel>
      </div>
    );
  }
  return (
    <div>
      <PageTitle kicker="Device" title={nameOf(d)} subtitle={d.name ? d.mac : undefined} back={back} />
      <div className="flex flex-col gap-6">
        <Summary device={d} bypassAlias={data.bypassAlias} adguardError={data.adguardError} />
        <Lease device={d} />
        <Reservation device={d} />
        <OtherAliases device={d} />
        {d.ip && <DeviceQueries ip={d.ip} />}
      </div>
    </div>
  );
}

/** Facts whose empty values show a dash. */
function DeviceFacts({ items }: { items: Fact[] }) {
  return <Facts items={items.map((f) => (f.value === "" || f.value === undefined ? { ...f, value: "—" } : f))} />;
}

function Summary({ device: d, bypassAlias, adguardError }: { device: Device; bypassAlias: string; adguardError: string }) {
  const [renaming, setRenaming] = useState(false);
  // After a rename the focus goes back to Rename.
  const [renamed, setRenamed] = useState(false);
  return (
    <Panel title="Device" actions={!renaming && <DeviceAction device={d} onRename={() => setRenaming(true)} focus={renamed} />}>
      {renaming && (
        <div className="mb-4">
          <RenameForm
            device={d}
            onDone={() => {
              setRenaming(false);
              setRenamed(true);
            }}
          />
        </div>
      )}
      <DeviceFacts
        items={[
          { label: "IP", value: d.ip, mono: true },
          { label: "MAC", value: d.mac, mono: true },
          {
            label: "Online",
            value: (
              <span className="inline-flex items-center gap-2">
                <StatusDot tone={d.online ? "up" : "idle"} />
                {d.online ? "online" : "offline"}
              </span>
            ),
          },
          { label: "Address", value: <Adoption device={d} /> },
          { label: "Routing", value: <DirectToggle device={d} bypassAlias={bypassAlias} /> },
          { label: "Ad-blocking", value: <AdBlockingToggle device={d} adguardError={adguardError} /> },
        ]}
      />
    </Panel>
  );
}

function Lease({ device: d }: { device: Device }) {
  const l = d.lease;
  return (
    <Panel title="Lease">
      {l ? (
        <DeviceFacts
          items={[
            { label: "Address", value: l.ip, mono: true },
            { label: "Hostname", value: l.hostname },
            { label: "Interface", value: l.interface },
            { label: "Status", value: l.status },
            { label: "Seen", value: l.onlineStatus },
            { label: "Starts", value: l.starts, mono: true },
            { label: "Ends", value: l.ends, mono: true },
          ]}
        />
      ) : (
        <p className="text-sm text-muted-foreground">No lease right now.</p>
      )}
    </Panel>
  );
}

function Reservation({ device: d }: { device: Device }) {
  const r = d.reservation;
  return (
    <Panel title="Reservation">
      {r ? (
        <DeviceFacts
          items={[
            { label: "Address", value: r.ip, mono: true },
            { label: "Description", value: r.description },
            { label: "Hostname", value: r.hostname },
            { label: "Interface", value: r.interface },
          ]}
        />
      ) : (
        <p className="text-sm text-muted-foreground">None: a New Device, on a temporary lease.</p>
      )}
    </Panel>
  );
}

function OtherAliases({ device: d }: { device: Device }) {
  return (
    <Panel title="Other aliases" description="Read-only: ddash changes only the bypass alias.">
      {d.aliases.length ? (
        <ul className="flex flex-col gap-1 text-sm">
          {d.aliases.map((a) => (
            <li key={a.name}>
              <span className="font-semibold">{a.name}</span>
              {a.description && <span className="text-muted-foreground"> · {a.description}</span>}
            </li>
          ))}
        </ul>
      ) : (
        <p className="text-sm text-muted-foreground">None.</p>
      )}
    </Panel>
  );
}
