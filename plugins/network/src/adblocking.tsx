import { useAction } from "@ddash/plugin";
import { NetworkService, type Device } from "@ddash/proto/network/network_pb";
import { Badge, Switch } from "@ddash/ui";
import { nameOf } from "./devices";

/** "Ads allowed" for a Device whose ad-blocking is off; nothing while it is on, the default. */
export function AdBlocking({ device: d }: { device: Device }) {
  return d.adBlockingOff ? <Badge tone="warn">Ads allowed</Badge> : null;
}

/**
 * The Device's ad-blocking switch: applies at once, no confirm, audited.
 * On a New Device network Adopts it first. Disabled while AdGuard's
 * clients can't be read, since the switch's state would be a guess.
 */
export function AdBlockingToggle({ device: d, adguardError }: { device: Device; adguardError: string }) {
  const set = useAction(NetworkService.method.setAdBlocking);
  const on = !d.adBlockingOff;
  return (
    <div className="flex flex-col gap-1">
      <span className="inline-flex items-center gap-2">
        <Switch label={`Ad-blocking ${nameOf(d)}`} checked={on} disabled={set.pending || adguardError !== ""} onChange={(enabled) => void set.run({ mac: d.mac, enabled })} />
        <span className="text-muted-foreground">{on ? "on" : "off: ads allowed, until turned back on"}</span>
      </span>
      {adguardError && <span className="text-xs text-warn">{adguardError}</span>}
      {!d.adopted && <span className="text-xs text-muted-foreground">Turning ad-blocking off Adopts it first, at the lowest free reserved address.</span>}
      {set.error && (
        <span role="alert" className="text-xs text-down">
          {set.error.rawMessage}
        </span>
      )}
    </div>
  );
}
