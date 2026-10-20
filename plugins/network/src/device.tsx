import type { Device } from "@ddash/proto/network/network_pb";
import { PageHeader, StatusDot } from "@ddash/ui";
import { useId, type ReactNode } from "react";
import { Link, useParams } from "react-router";
import { nameOf, useDevices } from "./devices";
import { Adoption, Routing } from "./devices-table";

/** A Device's detail (/network/devices/:mac): the lease and reservation facts and the other aliases holding it, read-only. */
export function DeviceDetail() {
  const { mac = "" } = useParams();
  const { data, error } = useDevices();
  const back = (
    <Link to="../.." relative="path" className="text-sm text-muted-foreground hover:underline">
      ← Network
    </Link>
  );
  if (error && !data) {
    return (
      <div>
        <PageHeader title={mac} actions={back} />
        <p className="text-sm text-down">{error.rawMessage}</p>
      </div>
    );
  }
  if (!data) return null;
  const d = data.devices.find((x) => x.mac === mac.toLowerCase());
  if (!d) {
    return (
      <div>
        <PageHeader title={mac} actions={back} />
        <p className="text-sm text-muted-foreground">No Device has the MAC {mac}. It may have left the firewall's DHCP.</p>
      </div>
    );
  }
  return (
    <div>
      <PageHeader title={nameOf(d)} subtitle={d.name ? d.mac : undefined} actions={back} />
      <div className="flex flex-col gap-6">
        <Summary device={d} bypassAlias={data.bypassAlias} />
        <Lease device={d} />
        <Reservation device={d} />
        <OtherAliases device={d} />
      </div>
    </div>
  );
}

function Panel({ title, children }: { title: string; children: ReactNode }) {
  const id = useId();
  return (
    <section aria-labelledby={id} className="rounded-ui border border-border bg-card">
      <h2 id={id} className="border-b border-border px-4 py-3 font-semibold">
        {title}
      </h2>
      <div className="px-4 py-3 text-sm">{children}</div>
    </section>
  );
}

/** Label and value rows; an empty value shows a dash. */
function Facts({ rows }: { rows: [string, ReactNode][] }) {
  return (
    <dl className="grid grid-cols-3 gap-x-4 gap-y-2">
      {rows.map(([label, value]) => (
        <div key={label} className="contents">
          <dt className="text-muted-foreground">{label}</dt>
          <dd className="col-span-2 tabular-nums">{value === "" || value === undefined ? "—" : value}</dd>
        </div>
      ))}
    </dl>
  );
}

function Summary({ device: d, bypassAlias }: { device: Device; bypassAlias: string }) {
  return (
    <Panel title="Device">
      <Facts
        rows={[
          ["IP", d.ip],
          ["MAC", d.mac],
          [
            "Online",
            <span key="online" className="inline-flex items-center gap-2">
              <StatusDot tone={d.online ? "up" : "idle"} />
              {d.online ? "online" : "offline"}
            </span>,
          ],
          ["Address", <Adoption key="adoption" device={d} />],
          [
            "Routing",
            <span key="routing" className="inline-flex items-center gap-2">
              <Routing device={d} />
              <span className="text-muted-foreground">{d.direct ? `in ${bypassAlias}` : "via the VPN Tunnel"}</span>
            </span>,
          ],
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
        <Facts
          rows={[
            ["Address", l.ip],
            ["Hostname", l.hostname],
            ["Interface", l.interface],
            ["Status", l.status],
            ["Seen", l.onlineStatus],
            ["Starts", l.starts],
            ["Ends", l.ends],
          ]}
        />
      ) : (
        <p className="text-muted-foreground">No lease right now.</p>
      )}
    </Panel>
  );
}

function Reservation({ device: d }: { device: Device }) {
  const r = d.reservation;
  return (
    <Panel title="Reservation">
      {r ? (
        <Facts
          rows={[
            ["Address", r.ip],
            ["Description", r.description],
            ["Hostname", r.hostname],
            ["Interface", r.interface],
          ]}
        />
      ) : (
        <p className="text-muted-foreground">None: a New Device, on a temporary lease.</p>
      )}
    </Panel>
  );
}

function OtherAliases({ device: d }: { device: Device }) {
  return (
    <Panel title="Other aliases">
      {d.aliases.length ? (
        <ul className="flex flex-col gap-1">
          {d.aliases.map((a) => (
            <li key={a.name}>
              <span className="font-medium">{a.name}</span>
              {a.description && <span className="text-muted-foreground"> · {a.description}</span>}
            </li>
          ))}
        </ul>
      ) : (
        <p className="text-muted-foreground">None.</p>
      )}
      <p className="mt-2 text-xs text-muted-foreground">Read-only: ddash changes only the bypass alias.</p>
    </Panel>
  );
}
