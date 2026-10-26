import { useAction, usePlugin } from "@ddash/plugin";
import { NetworkService, type Device, type ListDevicesResponse } from "@ddash/proto/network/network_pb";
import { ActionStrip, Notice, ToggleButton } from "@ddash/ui";
import { useState } from "react";
import { DeviceAction, RenameForm } from "./adopt";
import { InternetPathPicker, useSetInternetPath, whereItGoes } from "./internet-path";

/**
 * A Devices row's strip: its internet path and ad-blocking in a line, the
 * internet path picker and the Ads blocked toggle, acting at once with no
 * confirm (on a New Device network Adopts it first), and Rename, which
 * turns the strip into a Name field, or Adopt, which opens its Sheet. Ads
 * blocked is there only with AdGuard, and disabled while its clients can't
 * be read.
 */
export function DeviceStrip({ device: d, list }: { device: Device; list: ListDevicesResponse }) {
  const adguard = usePlugin().requires.includes("adguard-api");
  const { adguardError } = list;
  const path = useSetInternetPath();
  const ads = useAction(NetworkService.method.setAdBlocking);
  const [renaming, setRenaming] = useState(false);
  if (renaming) return <RenameForm device={d} onDone={() => setRenaming(false)} />;

  const blocked = !d.adBlockingOff;
  // While AdGuard's clients can't be read, whether it blocks ads is a guess.
  const unread = adguard && adguardError !== "";
  const summary = [
    whereItGoes(d, list),
    adguard && !unread && (blocked ? "ads blocked" : "ads allowed"),
    !d.adopted && "New: a change Adopts it first",
  ].filter(Boolean);
  return (
    <ActionStrip
      summary={summary.join(" · ")}
      notice={
        (unread || path.error || ads.error) && (
          <>
            {unread && <Notice tone="warn">{adguardError}</Notice>}
            {path.error && <Notice tone="down">{path.error.rawMessage}</Notice>}
            {ads.error && <Notice tone="down">{ads.error.rawMessage}</Notice>}
          </>
        )
      }
    >
      <InternetPathPicker device={d} blockAlias={list.blockAlias} action={path} />
      {adguard && (
        <ToggleButton size="sm" pressed={blocked} disabled={ads.pending || unread} onChange={(enabled) => void ads.run({ mac: d.mac, enabled })}>
          Ads blocked
        </ToggleButton>
      )}
      <DeviceAction device={d} onRename={() => setRenaming(true)} />
    </ActionStrip>
  );
}
