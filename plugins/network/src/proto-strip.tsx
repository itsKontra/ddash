// PROTOTYPE (wayfinder: "What does a row's inline action strip look like and
// hold, for Devices and Workloads?"): a Device row's strip in each variant
// (see @ddash/ui's proto-strip.tsx). Throwaway; prototype/row-action-strip only.
import { useAction, useQuery } from "@ddash/plugin";
import { NetworkService, type Device } from "@ddash/proto/network/network_pb";
import { Button, Field, Notice, Segmented, Switch, TextInput, useStripVariant } from "@ddash/ui";
import { useState, type FormEvent, type ReactNode } from "react";
import { AdBlockingToggle } from "./adblocking";
import { DirectToggle } from "./direct";

interface StripProps {
  device: Device;
  bypassAlias: string;
  adguardError: string;
  /** Opens the Adopt or Rename Sheet. */
  openSheet: () => void;
}

export function DeviceStrip(props: StripProps) {
  const v = useStripVariant();
  if (v === "2") return <Inline {...props} />;
  if (v === "3") return <Command {...props} />;
  return <Toolbar {...props} />;
}

const adoptsFirst = "Toggling Direct or ad-blocking on a New Device Adopts it first, at the lowest free reserved address.";

/** 1: one line of controls; Rename and Adopt open their Sheets, the Switches act at once. */
function Toolbar({ device: d, bypassAlias, adguardError, openSheet }: StripProps) {
  return (
    <div className="flex flex-col gap-2">
      <div className="flex flex-wrap items-start gap-x-8 gap-y-3 text-sm">
        <Button variant="outline" size="sm" icon={d.adopted ? "pencil" : "badge-check"} onClick={openSheet}>
          {d.adopted ? "Rename…" : "Adopt…"}
        </Button>
        <DirectToggle device={d} bypassAlias={bypassAlias} />
        <AdBlockingToggle device={d} adguardError={adguardError} />
      </div>
    </div>
  );
}

/** 2: a small form in the strip: the name (and for a New Device the address) left, the two settings right. */
function Inline({ device: d, bypassAlias, adguardError }: StripProps) {
  const direct = useAction(NetworkService.method.setDirect);
  const ads = useAction(NetworkService.method.setAdBlocking);
  const on = !d.adBlockingOff;
  return (
    <div className="grid gap-6 pt-2 sm:grid-cols-[minmax(0,22rem)_minmax(0,1fr)]">
      {d.adopted ? <InlineRename device={d} /> : <InlineAdopt device={d} />}
      <div className="flex flex-col gap-4 text-sm">
        <Setting
          title="Routing"
          control={<Switch checked={d.direct} disabled={direct.pending} label="Direct" onChange={(v) => void direct.run({ mac: d.mac, direct: v })} />}
          state={d.direct ? "Direct" : "Tunnelled"}
          detail={d.direct ? `Its address is in ${bypassAlias}, so it skips the VPN Tunnel.` : "Its traffic leaves through the VPN Tunnel."}
          error={direct.error?.rawMessage}
        />
        <Setting
          title="Ad-blocking"
          control={
            <Switch label="Ad-blocking" checked={on} disabled={ads.pending || adguardError !== ""} onChange={(enabled) => void ads.run({ mac: d.mac, enabled })} />
          }
          state={on ? "On" : "Off: ads allowed"}
          detail={adguardError || (on ? "AdGuard blocks ads for it." : "Until turned back on.")}
          error={ads.error?.rawMessage}
        />
        {!d.adopted && <p className="text-xs text-muted-foreground">{adoptsFirst}</p>}
      </div>
    </div>
  );
}

function Setting({ title, control, state, detail, error }: { title: string; control: ReactNode; state: string; detail: string; error?: string }) {
  return (
    <div className="flex flex-col gap-1">
      <div className="flex items-center gap-3">
        {control}
        <span className="font-semibold">{title}</span>
        <span>{state}</span>
      </div>
      <span className="pl-12 text-xs text-muted-foreground">{detail}</span>
      {error && <Notice tone="down">{error}</Notice>}
    </div>
  );
}

function InlineRename({ device: d, onDone }: { device: Device; onDone?: () => void }) {
  const rename = useAction(NetworkService.method.renameDevice);
  const [name, setName] = useState(d.name);
  const save = async (e: FormEvent) => {
    e.preventDefault();
    if (await rename.run({ mac: d.mac, name })) onDone?.();
  };
  return (
    <form onSubmit={(e) => void save(e)} className="flex flex-col gap-2">
      <div className="flex items-end gap-2">
        <div className="min-w-0 flex-1">
          <Field label="Name">
            <TextInput value={name} onChange={(e) => setName(e.target.value)} />
          </Field>
        </div>
        <Button type="submit" disabled={rename.pending || name === d.name}>
          Rename
        </Button>
        {onDone && (
          <Button variant="ghost" onClick={onDone}>
            Cancel
          </Button>
        )}
      </div>
      {rename.error && <Notice tone="down">{rename.error.rawMessage}</Notice>}
    </form>
  );
}

function InlineAdopt({ device: d }: { device: Device }) {
  const adopt = useAction(NetworkService.method.adoptDevice);
  const { data: next } = useQuery(NetworkService.method.nextReservedAddress, {});
  const [name, setName] = useState(d.name);
  const [ip, setIp] = useState<string>();
  const address = ip ?? next?.ip ?? "";
  const save = (e: FormEvent) => {
    e.preventDefault();
    void adopt.run({ mac: d.mac, name, ip: address });
  };
  return (
    <form onSubmit={save} className="flex flex-col gap-3">
      <div className="grid grid-cols-2 gap-2">
        <Field label="Name">
          <TextInput value={name} onChange={(e) => setName(e.target.value)} />
        </Field>
        <Field label="Address">
          <TextInput value={address} onChange={(e) => setIp(e.target.value)} />
        </Field>
      </div>
      <div className="flex items-center gap-3">
        <Button type="submit" disabled={adopt.pending}>
          Adopt
        </Button>
        <span className="text-xs text-muted-foreground">Restarts the firewall's DHCP server, a few seconds' blip.</span>
      </div>
      {adopt.error && <Notice tone="down">{adopt.error.rawMessage}</Notice>}
    </form>
  );
}

/** 3: a summary line left, compact controls right; Rename swaps the strip for a name field, Adopt opens its Sheet. */
function Command({ device: d, bypassAlias, adguardError, openSheet }: StripProps) {
  const direct = useAction(NetworkService.method.setDirect);
  const ads = useAction(NetworkService.method.setAdBlocking);
  const [renaming, setRenaming] = useState(false);
  if (renaming) return <InlineRename device={d} onDone={() => setRenaming(false)} />;
  const on = !d.adBlockingOff;
  const error = direct.error?.rawMessage ?? ads.error?.rawMessage;
  return (
    <div className="flex flex-col gap-2">
      <div className="flex flex-wrap items-center justify-between gap-x-6 gap-y-2">
        <p className="text-xs text-muted-foreground">
          {d.direct ? `Direct, in ${bypassAlias}` : "Tunnelled via the VPN Tunnel"} · {on ? "ads blocked" : "ads allowed"}
          {!d.adopted && " · New: a change Adopts it first"}
        </p>
        <div className="flex flex-wrap items-center gap-3">
          <Segmented
            label="Routing"
            value={d.direct ? "Direct" : "Tunnelled"}
            options={["Tunnelled", "Direct"] as const}
            onChange={(v) => !direct.pending && void direct.run({ mac: d.mac, direct: v === "Direct" })}
          />
          <Segmented
            label="Ads"
            value={on ? "Ads blocked" : "Ads allowed"}
            options={["Ads blocked", "Ads allowed"] as const}
            onChange={(v) => !ads.pending && adguardError === "" && void ads.run({ mac: d.mac, enabled: v === "Ads blocked" })}
          />
          {d.adopted ? (
            <Button size="sm" variant="ghost" icon="pencil" onClick={() => setRenaming(true)}>
              Rename
            </Button>
          ) : (
            <Button size="sm" icon="badge-check" onClick={openSheet}>
              Adopt…
            </Button>
          )}
        </div>
      </div>
      {adguardError && <Notice tone="warn">{adguardError}</Notice>}
      {error && <Notice tone="down">{error}</Notice>}
    </div>
  );
}
