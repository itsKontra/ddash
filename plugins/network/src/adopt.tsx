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

/**
 * Renaming an Adopted Device in place, its static mapping's description:
 * a Name field with Rename and Cancel. Enter renames; Escape in the field
 * cancels, and goes no further (a row's strip stays open).
 */
export function RenameForm({ device: d, onDone }: { device: Device; onDone: () => void }) {
  const rename = useAction(NetworkService.method.renameDevice);
  const [name, setName] = useState(d.name);

  const save = async (e: FormEvent) => {
    e.preventDefault();
    if (await rename.run({ mac: d.mac, name })) onDone();
  };

  return (
    <form onSubmit={(e) => void save(e)} className="flex flex-col gap-2">
      <div className="flex flex-wrap items-start gap-2">
        <div className="min-w-0 flex-1 basis-56">
          <Field label="Name" hint={`It keeps its reserved address ${d.reservation?.ip ?? d.ip}; change that in pfSense.`}>
            <TextInput
              autoFocus
              value={name}
              onChange={(e) => setName(e.target.value)}
              onKeyDown={(e) => {
                if (e.key !== "Escape") return;
                e.preventDefault();
                onDone();
              }}
            />
          </Field>
        </div>
        {/* In line with the field, under its label. */}
        <div className="flex gap-2 pt-5.5">
          <Button type="submit" disabled={rename.pending}>
            Rename
          </Button>
          <Button variant="ghost" onClick={onDone}>
            Cancel
          </Button>
        </div>
      </div>
      {rename.error && <Notice tone="down">{rename.error.rawMessage}</Notice>}
    </form>
  );
}

/**
 * A Device's naming Action, the same on its Page and in its Devices row's
 * strip: Rename for an Adopted Device (onRename turns the caller's view
 * into a RenameForm), or Adopt… for a New one, opening its Sheet. The
 * Sheet stays mounted past the Adopt, so it animates out.
 */
export function DeviceAction({ device: d, onRename, focus }: { device: Device; onRename: () => void; focus?: boolean }) {
  const [adopting, setAdopting] = useState(false);
  return (
    <>
      {d.adopted ? (
        <Button size="sm" variant="ghost" icon="pencil" autoFocus={focus} onClick={onRename}>
          Rename
        </Button>
      ) : (
        <Button size="sm" icon="badge-check" onClick={() => setAdopting(true)}>
          Adopt…
        </Button>
      )}
      <Presence>{adopting && <AdoptSheet device={d} onClose={() => setAdopting(false)} />}</Presence>
    </>
  );
}
