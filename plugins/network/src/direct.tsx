import { useAction } from "@ddash/plugin";
import { NetworkService, type Device } from "@ddash/proto/network/network_pb";
import { Switch } from "@ddash/ui";
import { nameOf } from "./devices";

/**
 * The Direct toggle: Direct puts the Device's address in the bypass alias,
 * Tunnelled takes it out, at once and with no confirm. On a New Device it
 * Adopts it first (network does), so the setting sticks to a stable address.
 */
export function DirectToggle({ device: d, bypassAlias }: { device: Device; bypassAlias: string }) {
  const set = useAction(NetworkService.method.setDirect);
  return (
    <div className="flex flex-col gap-1">
      <span className="inline-flex items-center gap-2">
        <Switch checked={d.direct} disabled={set.pending} label={`Direct ${nameOf(d)}`} onChange={(direct) => void set.run({ mac: d.mac, direct })} />
        <span>{d.direct ? "Direct" : "Tunnelled"}</span>
        <span className="text-muted-foreground">{d.direct ? `in ${bypassAlias}` : "via the VPN Tunnel"}</span>
      </span>
      {!d.adopted && <span className="text-xs text-muted-foreground">Toggling Adopts it first, at the lowest free reserved address.</span>}
      {set.error && (
        <span role="alert" className="text-down">
          {set.error.rawMessage}
        </span>
      )}
    </div>
  );
}
