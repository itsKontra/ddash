import { useAction, useQuery } from "@ddash/plugin";
import { NetworkService, type Device } from "@ddash/proto/network/network_pb";
import { Button, Sheet } from "@ddash/ui";
import { useId, useState, type FormEvent } from "react";
import { nameOf } from "./devices";

const inputClass = "h-9 w-full rounded-ui border border-border bg-card px-3 text-sm tabular-nums placeholder:text-muted-foreground";

function Text({ label, help, value, onChange, placeholder }: { label: string; help?: string; value: string; onChange: (v: string) => void; placeholder?: string }) {
  const id = useId();
  return (
    <div className="flex flex-col gap-1">
      <label htmlFor={id} className="text-sm font-medium">
        {label}
      </label>
      <input id={id} type="text" className={inputClass} value={value} placeholder={placeholder} onChange={(e) => onChange(e.target.value)} />
      {help && <p className="text-xs text-muted-foreground">{help}</p>}
    </div>
  );
}

function Failed({ error }: { error?: { rawMessage: string } }) {
  if (!error) return null;
  return (
    <p role="alert" className="text-sm text-down-text">
      {error.rawMessage}
    </p>
  );
}

/** The sheet Adopting a New Device: its name and the reserved address, pre-filled with the lowest free one. */
function AdoptSheet({ device: d, onClose }: { device: Device; onClose: () => void }) {
  const adopt = useAction(NetworkService.method.adoptDevice);
  const { data: next } = useQuery(NetworkService.method.nextReservedAddress, {});
  const [name, setName] = useState(d.name);
  // Until edited, the address follows the offer.
  const [ip, setIp] = useState<string>();
  const address = ip ?? next?.ip ?? "";
  const range = next?.reservedRange;

  const save = async (e: FormEvent) => {
    e.preventDefault();
    if (await adopt.run({ mac: d.mac, name, ip: address })) onClose();
  };

  return (
    <Sheet title={`Adopt ${nameOf(d)}`} onClose={onClose}>
      <form onSubmit={(e) => void save(e)} className="flex flex-col gap-4">
        <p className="text-sm text-muted-foreground">
          Names <span className="font-medium text-foreground tabular-nums">{d.mac}</span> and reserves the address below for it on the firewall. The
          Device keeps <span className="tabular-nums">{d.ip}</span> for now and moves to it when its lease renews.
        </p>
        <Text label="Name" value={name} onChange={setName} />
        <Text
          label="Address"
          value={address}
          onChange={setIp}
          help={
            range
              ? next?.ip
                ? `The lowest free address in the reserved range ${range}; any free one in it will do.`
                : `The reserved range ${range} is full: widen it in network's config.`
              : undefined
          }
        />
        <p className="text-xs text-muted-foreground">Applying it restarts the firewall's DHCP server, a few seconds' blip.</p>
        <Failed error={adopt.error} />
        <div className="flex gap-2">
          <Button type="submit" disabled={adopt.pending}>
            Adopt
          </Button>
        </div>
      </form>
    </Sheet>
  );
}

/** The sheet renaming an Adopted Device: its static mapping's description. */
function RenameSheet({ device: d, onClose }: { device: Device; onClose: () => void }) {
  const rename = useAction(NetworkService.method.renameDevice);
  const [name, setName] = useState(d.name);

  const save = async (e: FormEvent) => {
    e.preventDefault();
    if (await rename.run({ mac: d.mac, name })) onClose();
  };

  return (
    <Sheet title={`Rename ${nameOf(d)}`} onClose={onClose}>
      <form onSubmit={(e) => void save(e)} className="flex flex-col gap-4">
        <Text label="Name" value={name} onChange={setName} />
        <p className="text-sm text-muted-foreground">
          It keeps its reserved address <span className="tabular-nums">{d.reservation?.ip ?? d.ip}</span>. Change the address or remove the
          reservation in pfSense.
        </p>
        <Failed error={rename.error} />
        <div className="flex gap-2">
          <Button type="submit" disabled={rename.pending}>
            Rename
          </Button>
        </div>
      </form>
    </Sheet>
  );
}

/** Adopt for a New Device, Rename for an Adopted one, each opening its sheet. */
export function DeviceAction({ device: d }: { device: Device }) {
  const [open, setOpen] = useState(false);
  const label = `${d.adopted ? "Rename" : "Adopt"} ${nameOf(d)}`;
  return (
    <>
      <Button variant="outline" size="sm" aria-label={label} onClick={() => setOpen(true)}>
        {d.adopted ? "Rename" : "Adopt"}
      </Button>
      {open && (d.adopted ? <RenameSheet device={d} onClose={() => setOpen(false)} /> : <AdoptSheet device={d} onClose={() => setOpen(false)} />)}
    </>
  );
}
