import { timestampMs, type Timestamp } from "@bufbuild/protobuf/wkt";
import { bytes } from "@ddash/display";
import { useQuery } from "@ddash/plugin";
import { NetworkService, type Reach, type RemoteAccessPeer } from "@ddash/proto/network/network_pb";
import { DataTable, Notice, Panel, StatusDot, type Column, type Tone } from "@ddash/ui";

/** WireGuard handshakes every 2 minutes while a peer is connected. */
const CONNECTED_WITHIN = 3 * 60_000;

/** How long ago a time was, e.g. "2 min ago"; "never" when unset. */
export function ago(ts: Timestamp | undefined, now: number): string {
  if (!ts) return "never";
  const mins = Math.floor((now - timestampMs(ts)) / 60_000);
  if (mins < 1) return "<1 min ago";
  if (mins < 60) return `${mins} min ago`;
  const hours = Math.floor(mins / 60);
  if (hours < 48) return `${hours} h ago`;
  return `${Math.floor(hours / 24)} d ago`;
}

function connection(p: RemoteAccessPeer, now: number): { tone: Tone; label: string } {
  if (!p.latestHandshake) return { tone: "idle", label: "never connected" };
  if (now - timestampMs(p.latestHandshake) <= CONNECTED_WITHIN) return { tone: "up", label: "connected" };
  return { tone: "idle", label: "not connected" };
}

/** A peer's name, or the start of its public key when it has none. */
const peerName = (p: RemoteAccessPeer) => p.name || `${p.publicKey.slice(0, 8)}…`;

function ReachItem({ reach }: { reach: Reach }) {
  return (
    <li title={reach.rule}>
      <span className="font-medium">{reach.destination}</span>
      {reach.label && <span className="text-muted-foreground">{` ${reach.label}`}</span>}
      <span className="text-muted-foreground">{` · ${reach.protocol}${reach.ports ? ` ${reach.ports}` : ""}`}</span>
    </li>
  );
}

/** The peer table's columns; handshake ages are as of now. */
function peerColumns(now: number): Column<RemoteAccessPeer>[] {
  return [
    {
      id: "peer",
      header: "Peer",
      rowHeader: true,
      cell: (p) => (
        <>
          {peerName(p)}
          <div className="text-xs font-normal text-muted-foreground">{p.addresses.join(", ")}</div>
        </>
      ),
    },
    {
      id: "connection",
      header: "Connection",
      cell: (p) => {
        const c = connection(p, now);
        return <StatusDot tone={c.tone} label={c.label} />;
      },
    },
    { id: "handshake", header: "Last handshake", cell: (p) => <span className="text-muted-foreground">{ago(p.latestHandshake, now)}</span> },
    { id: "rx", header: "From peer", align: "right", mono: true, cell: (p) => bytes(p.rxBytes) },
    { id: "tx", header: "To peer", align: "right", mono: true, cell: (p) => bytes(p.txBytes) },
    {
      id: "reach",
      header: "Can reach",
      cell: (p) =>
        p.reaches.length > 0 ? (
          <ul aria-label={`${peerName(p)} can reach`} className="flex flex-col gap-0.5">
            {p.reaches.map((r, i) => (
              <ReachItem key={i} reach={r} />
            ))}
          </ul>
        ) : (
          <span className="text-muted-foreground">nothing</span>
        ),
    },
  ];
}

/**
 * The Remote Access section: the peers (read-only), when each last shook
 * hands, its traffic, and what the pass rules on the Remote Access
 * interface let it reach.
 */
export function RemoteAccessPeers() {
  // Handshake ages are as of the read, which refreshes every minute.
  const { data, error, dataUpdatedAt: now } = useQuery(NetworkService.method.getRemoteAccess, {});
  const failed = error && !data;
  const partial = Boolean(data?.error);
  return (
    <Panel
      title="Remote Access"
      description={
        data &&
        !data.error &&
        `Tunnel ${data.tunnel} on ${data.interface}. What each peer can reach comes from that interface's pass rules; aliases aren't expanded.`
      }
      flush={!failed && !partial}
    >
      {failed ? (
        <Notice tone="down">{error.rawMessage}</Notice>
      ) : data?.error ? (
        <Notice tone="warn">{data.error}</Notice>
      ) : (
        <DataTable rows={data?.peers ?? []} rowKey={(p) => p.publicKey} columns={peerColumns(now)} loading={!data} empty="The tunnel has no peers." />
      )}
    </Panel>
  );
}

