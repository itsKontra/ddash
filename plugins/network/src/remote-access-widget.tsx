import { ago } from "@ddash/display";
import type { Status, Widget, WidgetSize } from "@ddash/plugin";
import { Notice, Skeleton, StatusList } from "@ddash/ui";
import { AddDevice } from "./add-device";
import { connection, peerName, useRemoteAccess } from "./remote-access";

/** How many peers each size lists. */
const shown: Record<WidgetSize, number> = { S: 3, M: 8, L: 16 };

/** Up with the counts; warn when there's no Remote Access tunnel or network can't read it. */
function useStatus(): Status | undefined {
  const { data, error, dataUpdatedAt: now } = useRemoteAccess();
  if (error) return { tone: "warn", summary: `Remote Access unavailable: ${error.rawMessage}` };
  if (!data) return undefined;
  if (data.error) return { tone: "warn", summary: data.error };
  const connected = data.peers.filter((p) => connection(p, now).tone === "up").length;
  return { tone: "up", summary: `${data.peers.length} ${data.peers.length === 1 ? "peer" : "peers"}, ${connected} connected` };
}

/** The peers with their last handshake, and Add a device. */
function Body({ size }: { size: WidgetSize }) {
  const { data, error, dataUpdatedAt: now } = useRemoteAccess();
  if (!data) return error ? null : <Skeleton lines={3} />;
  if (data.error) return <Notice tone="warn">{data.error}</Notice>;
  const peers = data.peers.slice(0, shown[size]);
  const more = data.peers.length - peers.length;
  return (
    <div className="flex flex-col gap-3">
      {peers.length > 0 ? (
        <StatusList
          items={peers.map((p) => {
            const c = connection(p, now);
            return { tone: c.tone, title: peerName(p), aside: p.latestHandshake ? ago(p.latestHandshake, now) : c.label };
          })}
        />
      ) : (
        <p className="text-sm text-muted-foreground">No device has Remote Access yet.</p>
      )}
      {more > 0 && <p className="text-xs text-muted-foreground">{`and ${more} more`}</p>}
      <div>
        <AddDevice size="sm" unavailable={data.addUnavailable} />
      </div>
    </div>
  );
}

/** The Overview Widget: the Remote Access peers' last handshakes, and Add a device. */
export const remoteAccess: Widget = {
  id: "remote-access",
  title: "Remote Access",
  sizes: ["S", "M"],
  defaultSize: "S",
  Body,
  useStatus,
};
