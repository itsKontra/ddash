import { useAction, usePlugin } from "@ddash/plugin";
import { NetworkService, type Device } from "@ddash/proto/network/network_pb";
import { ActionStrip, Notice, ToggleButton } from "@ddash/ui";
import { useState } from "react";
import { DeviceAction, RenameForm } from "./adopt";

/**
 * A Devices row's strip: its routing and ad-blocking in a line, the
 * Tunnelled and Ads blocked toggles, acting at once with no confirm (on a
 * New Device network Adopts it first), and Rename, which turns the strip
 * into a Name field, or Adopt, which opens its Sheet. Ads blocked is there
 * only with AdGuard, and disabled while its clients can't be read.
 */
export function DeviceStrip({ device: d, bypassAlias, adguardError }: { device: Device; bypassAlias: string; adguardError: string }) {
  const adguard = usePlugin().requires.includes("adguard-api");
  const direct = useAction(NetworkService.method.setDirect);
  const ads = useAction(NetworkService.method.setAdBlocking);
  const [renaming, setRenaming] = useState(false);
  if (renaming) return <RenameForm device={d} onDone={() => setRenaming(false)} />;

  const blocked = !d.adBlockingOff;
  // While AdGuard's clients can't be read, whether it blocks ads is a guess.
  const unread = adguard && adguardError !== "";
  const summary = [
    d.direct ? `Direct, in ${bypassAlias}` : "Tunnelled via the VPN Tunnel",
    adguard && !unread && (blocked ? "ads blocked" : "ads allowed"),
    !d.adopted && "New: a change Adopts it first",
  ].filter(Boolean);
  return (
    <ActionStrip
      summary={summary.join(" · ")}
      notice={
        (unread || direct.error || ads.error) && (
          <>
            {unread && <Notice tone="warn">{adguardError}</Notice>}
            {direct.error && <Notice tone="down">{direct.error.rawMessage}</Notice>}
            {ads.error && <Notice tone="down">{ads.error.rawMessage}</Notice>}
          </>
        )
      }
    >
      <ToggleButton size="sm" pressed={!d.direct} disabled={direct.pending} onChange={(tunnelled) => void direct.run({ mac: d.mac, direct: !tunnelled })}>
        Tunnelled
      </ToggleButton>
      {adguard && (
        <ToggleButton size="sm" pressed={blocked} disabled={ads.pending || unread} onChange={(enabled) => void ads.run({ mac: d.mac, enabled })}>
          Ads blocked
        </ToggleButton>
      )}
      <DeviceAction device={d} onRename={() => setRenaming(true)} />
    </ActionStrip>
  );
}
