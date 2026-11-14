import { useAction } from "@ddash/plugin";
import { NetworkService, type AddRemoteAccessPeerResponse } from "@ddash/proto/network/network_pb";
import { Button, CodeBlock, Dialog, Field, FormActions, Notice, Presence, TextInput } from "@ddash/ui";
import { useId, useState, type FormEvent } from "react";
import { QrCode } from "./qr";

/**
 * The config's file name: the name as a WireGuard interface may be called
 * (up to 15 of letters, digits and "_=+.-"), since wg-quick names the
 * interface after the file.
 */
export function configFile(name: string): string {
  const base = name
    .trim()
    .toLowerCase()
    .replace(/[^a-z0-9_=+.-]+/g, "-")
    .replace(/^-+|-+$/g, "")
    .slice(0, 15)
    .replace(/-+$/, "");
  return `${base || "remote-access"}.conf`;
}

/** Saves text as a file through the browser. */
function download(file: string, text: string) {
  const url = URL.createObjectURL(new Blob([text], { type: "text/plain" }));
  const a = document.createElement("a");
  a.href = url;
  a.download = file;
  a.click();
  URL.revokeObjectURL(url);
}

/** What the Dialog shows once the device is added: its config as a QR code and as text, this once. */
function Added({ name, added }: { name: string; added: AddRemoteAccessPeerResponse }) {
  return (
    <div className="flex flex-col gap-4">
      <Notice tone="info" title="Shown once">
        ddash doesn&apos;t keep the private key, and pfSense only holds the public one: scan the code or save the config now. Closing this forgets it.
      </Notice>
      {added.warning && <Notice tone="warn">{added.warning}</Notice>}
      <p className="text-sm text-muted-foreground">
        Scan it with the WireGuard app (Add a tunnel, Scan from QR code). {name} gets{" "}
        <span className="font-medium text-foreground tabular-nums">{added.peer?.addresses.join(", ")}</span>.
      </p>
      <div className="flex justify-center">
        <QrCode text={added.config} label={`WireGuard config of ${name} as a QR code`} />
      </div>
      <CodeBlock label="WireGuard config" copy>
        {added.config}
      </CodeBlock>
    </div>
  );
}

/**
 * The Dialog adding a device to Remote Access: name it, Add, then its
 * config as a QR code and as text with a download. The config lives in
 * this Dialog's state alone, so closing it forgets the private key.
 */
export function AddDeviceDialog({ unavailable, onClose }: { unavailable?: string; onClose: () => void }) {
  const add = useAction(NetworkService.method.addRemoteAccessPeer);
  const form = useId();
  const [name, setName] = useState("");
  const [added, setAdded] = useState<AddRemoteAccessPeerResponse>();

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    const res = await add.run({ name });
    if (res) setAdded(res);
  };

  if (added) {
    const shown = added.peer?.name || name;
    return (
      <Dialog
        title={`${shown} is added`}
        kicker="Remote Access"
        onClose={onClose}
        footer={
          <FormActions>
            <Button variant="outline" icon="download" onClick={() => download(configFile(shown), added.config)}>
              Download
            </Button>
            <Button onClick={onClose}>Done</Button>
          </FormActions>
        }
      >
        <Added name={shown} added={added} />
      </Dialog>
    );
  }
  return (
    <Dialog
      title="Add a device"
      kicker="Remote Access"
      onClose={onClose}
      footer={
        <FormActions status="ddash generates its key pair and gives pfSense the public key only.">
          <Button variant="ghost" onClick={onClose}>
            Cancel
          </Button>
          <Button type="submit" form={form} disabled={add.pending || !!unavailable || name.trim() === ""}>
            Add
          </Button>
        </FormActions>
      }
    >
      <form id={form} onSubmit={(e) => void submit(e)} className="flex flex-col gap-4">
        <p className="text-sm text-muted-foreground">
          Adds a WireGuard peer for a phone or laptop at the next free Remote Access address, then shows its config once, to scan or save.
        </p>
        {unavailable && <Notice tone="warn">{unavailable}</Notice>}
        <Field label="Name">
          <TextInput autoFocus value={name} placeholder="Anna's phone" onChange={(e) => setName(e.target.value)} />
        </Field>
        {add.error && <Notice tone="down">{add.error.rawMessage}</Notice>}
      </form>
    </Dialog>
  );
}

/** "Add a device", opening the AddDeviceDialog; the Dialog stays mounted past its close, so it animates out. */
export function AddDevice({ unavailable, size }: { unavailable?: string; size?: "sm" | "md" }) {
  const [open, setOpen] = useState(false);
  return (
    <>
      <Button size={size} variant="outline" icon="smartphone" onClick={() => setOpen(true)}>
        Add a device
      </Button>
      <Presence>{open && <AddDeviceDialog unavailable={unavailable} onClose={() => setOpen(false)} />}</Presence>
    </>
  );
}
