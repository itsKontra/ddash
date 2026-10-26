import { useAction, useQuery } from "@ddash/plugin";
import { NetworkService, type Device } from "@ddash/proto/network/network_pb";
import { Button, Field, FormActions, Notice, Presence, Sheet, TextInput } from "@ddash/ui";
import { useId, useState, type FormEvent } from "react";
import { nameOf } from "./devices";

/** The sheet Adopting a New Device: its name and the reserved address, pre-filled with the lowest free one. */
function AdoptSheet({ device: d, onClose }: { device: Device; onClose: () => void }) {
  const adopt = useAction(NetworkService.method.adoptDevice);
  const { data: next } = useQuery(NetworkService.method.nextReservedAddress, {});
  const form = useId();
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
    <Sheet
      title={`Adopt ${nameOf(d)}`}
      onClose={onClose}
      footer={
        <FormActions status="Applying it restarts the firewall's DHCP server, a few seconds' blip.">
          <Button type="submit" form={form} disabled={adopt.pending}>
            Adopt
          </Button>
        </FormActions>
      }
    >
      <form id={form} onSubmit={(e) => void save(e)} className="flex flex-col gap-4">
        <p className="text-sm text-muted-foreground">
          Names <span className="font-medium text-foreground tabular-nums">{d.mac}</span> and reserves the address below for it on the firewall. The Device
          keeps <span className="tabular-nums">{d.ip}</span> for now and moves to it when its lease renews.
        </p>
        <Field label="Name">
          <TextInput value={name} onChange={(e) => setName(e.target.value)} />
        </Field>
        <Field
          label="Address"
          hint={
            range
              ? next?.ip
                ? `The lowest free address in the reserved range ${range}; any free one in it will do.`
                : `The reserved range ${range} is full: widen it in network's config.`
              : undefined
          }
        >
          <TextInput value={address} onChange={(e) => setIp(e.target.value)} />
        </Field>
        {adopt.error && <Notice tone="down">{adopt.error.rawMessage}</Notice>}
      </form>
    </Sheet>
  );
}

/** The sheet renaming an Adopted Device: its static mapping's description. */
function RenameSheet({ device: d, onClose }: { device: Device; onClose: () => void }) {
  const rename = useAction(NetworkService.method.renameDevice);
  const form = useId();
  const [name, setName] = useState(d.name);

  const save = async (e: FormEvent) => {
    e.preventDefault();
    if (await rename.run({ mac: d.mac, name })) onClose();
  };

  return (
    <Sheet
      title={`Rename ${nameOf(d)}`}
      onClose={onClose}
      footer={
        <FormActions>
          <Button type="submit" form={form} disabled={rename.pending}>
            Rename
          </Button>
        </FormActions>
      }
    >
      <form id={form} onSubmit={(e) => void save(e)} className="flex flex-col gap-4">
        <Field label="Name">
          <TextInput value={name} onChange={(e) => setName(e.target.value)} />
        </Field>
        <p className="text-sm text-muted-foreground">
          It keeps its reserved address <span className="tabular-nums">{d.reservation?.ip ?? d.ip}</span>. Change the address or remove the
          reservation in pfSense.
        </p>
        {rename.error && <Notice tone="down">{rename.error.rawMessage}</Notice>}
      </form>
    </Sheet>
  );
}

/** The sheet for a Device: Adopt for a New one, rename for an Adopted one. */
export function DeviceSheet({ device: d, onClose }: { device: Device; onClose: () => void }) {
  return d.adopted ? <RenameSheet device={d} onClose={onClose} /> : <AdoptSheet device={d} onClose={onClose} />;
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
      <Presence>{open && <DeviceSheet device={d} onClose={() => setOpen(false)} />}</Presence>
    </>
  );
}
