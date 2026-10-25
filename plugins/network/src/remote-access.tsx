import { timestampMs, type Timestamp } from "@bufbuild/protobuf/wkt";
import { bytes } from "@ddash/display";
import { useQuery } from "@ddash/plugin";
import { NetworkService, type Reach, type RemoteAccessPeer } from "@ddash/proto/network/network_pb";
import { StatusDot, type Tone } from "@ddash/ui";

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

/**
 * The Remote Access peers (read-only): when each last shook hands, its
 * traffic, and what the pass rules on the Remote Access interface let it
 * reach.
 */
export function RemoteAccessPeers() {
  // Handshake ages are as of the read, which refreshes every minute.
  const { data, error, dataUpdatedAt: now } = useQuery(NetworkService.method.getRemoteAccess, {});
  if (error && !data) return <p className="px-4 py-3 text-sm text-down-text">{error.rawMessage}</p>;
  if (!data) return null;
  if (data.error) return <p className="px-4 py-3 text-sm text-warn-text">{data.error}</p>;
  return (
    <>
      <table aria-label="Remote Access peers" className="w-full text-sm">
        <thead>
          <tr className="border-b border-border text-left text-xs text-muted-foreground">
            <th scope="col" className="w-6 py-2 pl-4">
              <span className="sr-only">Connection</span>
            </th>
            <th scope="col" className="px-2 py-2 font-medium">
              Peer
            </th>
            <th scope="col" className="px-2 py-2 font-medium">
              Last handshake
            </th>
            <th scope="col" className="px-2 py-2 text-right font-medium">
              From peer
            </th>
            <th scope="col" className="px-2 py-2 text-right font-medium">
              To peer
            </th>
            <th scope="col" className="px-2 py-2 pr-4 font-medium">
              Can reach
            </th>
          </tr>
        </thead>
        <tbody>
          {data.peers.map((p) => {
            const c = connection(p, now);
            const name = peerName(p);
            return (
              <tr key={p.publicKey} className="border-b border-border align-top last:border-b-0">
                <td className="w-6 py-2 pl-4">
                  <StatusDot tone={c.tone} label={c.label} />
                </td>
                <th scope="row" className="px-2 py-2 text-left font-medium">
                  {name}
                  <div className="text-xs font-normal text-muted-foreground">{p.addresses.join(", ")}</div>
                </th>
                <td className="px-2 py-2 text-muted-foreground">{ago(p.latestHandshake, now)}</td>
                <td className="px-2 py-2 text-right text-muted-foreground tabular-nums">{bytes(p.rxBytes)}</td>
                <td className="px-2 py-2 text-right text-muted-foreground tabular-nums">{bytes(p.txBytes)}</td>
                <td className="px-2 py-2 pr-4">
                  {p.reaches.length > 0 ? (
                    <ul aria-label={`${name} can reach`} className="flex flex-col gap-0.5">
                      {p.reaches.map((r, i) => (
                        <ReachItem key={i} reach={r} />
                      ))}
                    </ul>
                  ) : (
                    <span className="text-muted-foreground">nothing</span>
                  )}
                </td>
              </tr>
            );
          })}
        </tbody>
      </table>
      <p className="px-4 py-3 text-xs text-muted-foreground">
        Tunnel {data.tunnel} on {data.interface}. What each peer can reach comes from that interface's pass rules; aliases aren't expanded.
      </p>
    </>
  );
}
