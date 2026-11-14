import { useAction, useQuery } from "@ddash/plugin";
import { ProxmoxService, type SSHKey } from "@ddash/proto/proxmox/proxmox_pb";
import { Button, CodeBlock, DataTable, Dialog, Field, FormActions, Notice, PageTitle, Panel, TextArea, TextInput, type Column } from "@ddash/ui";
import { ago } from "@ddash/display";
import { useNow } from "./format";
import { useState } from "react";

// The SSH Keys Page (/proxmox/ssh-keys): the named public keys ddash
// stores and installs on Workloads. Generating one creates a key pair in
// ddash and shows its private key once, to download; ddash keeps only the
// public key. An existing public key can be pasted instead.

const back = { href: "..", label: "Proxmox" };

/** The file a generated private key downloads as: "id_ed25519_laptop". */
export const keyFile = (name: string) => `id_ed25519_${name}`;

export function SSHKeys() {
  const { data, error } = useQuery(ProxmoxService.method.listSSHKeys, {});
  const remove = useAction(ProxmoxService.method.deleteSSHKey);
  const [dialog, setDialog] = useState<"generate" | "paste">();
  const now = useNow();
  const columns: readonly Column<SSHKey>[] = [
    { id: "name", header: "Name", rowHeader: true, sortable: true, sortValue: (k) => k.name, cell: (k) => k.name },
    { id: "type", header: "Type", hideBelow: "sm", cell: (k) => k.type },
    { id: "fingerprint", header: "Fingerprint", mono: true, cell: (k) => k.fingerprint },
    { id: "source", header: "Source", hideBelow: "sm", cell: (k) => (k.generated ? "generated in ddash" : "pasted") },
    { id: "created", header: "Added", sortable: true, sortValue: (k) => Number(k.created?.seconds ?? 0), cell: (k) => ago(k.created, now) },
    {
      id: "delete",
      header: "",
      align: "right",
      cell: (k) => (
        <Button
          size="sm"
          variant="danger"
          disabled={remove.pending}
          onClick={() =>
            void remove.run(
              { name: k.name },
              { confirm: { text: `Delete the SSH Key ${k.name} from ddash? It stays installed on the Workloads that have it: remove it there first to revoke it.`, action: "Delete" } },
            )
          }
        >
          Delete
        </Button>
      ),
    },
  ];
  return (
    <div>
      <PageTitle
        kicker="Proxmox"
        title="SSH Keys"
        subtitle="Named public keys ddash installs on Workloads through their Host Agent."
        back={back}
        actions={
          <>
            <Button icon="key-round" onClick={() => setDialog("generate")}>
              Generate a key
            </Button>
            <Button variant="outline" onClick={() => setDialog("paste")}>
              Paste a public key
            </Button>
          </>
        }
      />
      {error && !data && <Notice tone="down">{error.rawMessage}</Notice>}
      {remove.error && <Notice tone="down">{remove.error.rawMessage}</Notice>}
      <Panel flush>
        <DataTable rows={data?.keys ?? []} rowKey={(k) => k.name} columns={columns} loading={!data && !error} defaultSort="name" empty="ddash stores no SSH Key yet" />
      </Panel>
      {dialog === "generate" && <GenerateDialog onClose={() => setDialog(undefined)} />}
      {dialog === "paste" && <PasteDialog onClose={() => setDialog(undefined)} />}
    </div>
  );
}

/** Generating a key: its name, then its private key, shown this once. Closing forgets it. */
function GenerateDialog({ onClose }: { onClose: () => void }) {
  const [name, setName] = useState("");
  const generate = useAction(ProxmoxService.method.generateSSHKey);
  // Only here, in memory, until the Dialog closes.
  const [secret, setSecret] = useState<{ name: string; privateKey: string; publicKey: string }>();
  const close = () => {
    setSecret(undefined);
    onClose();
  };
  const go = async () => {
    const res = await generate.run({ name: name.trim() });
    if (res?.key) setSecret({ name: res.key.name, privateKey: res.privateKey, publicKey: res.key.publicKey });
  };
  const download = () => {
    if (!secret) return;
    const url = URL.createObjectURL(new Blob([secret.privateKey], { type: "application/x-pem-file" }));
    const a = document.createElement("a");
    a.href = url;
    a.download = keyFile(secret.name);
    a.click();
    URL.revokeObjectURL(url);
  };
  if (secret) {
    return (
      <Dialog
        title={`Private key of ${secret.name}`}
        subtitle="Shown once: ddash doesn't keep it. Download it now; once you close this, it's gone."
        onClose={close}
        footer={
          <FormActions>
            <Button variant="outline" icon="download" onClick={download}>
              Download {keyFile(secret.name)}
            </Button>
            <Button onClick={close}>Done</Button>
          </FormActions>
        }
      >
        <div className="flex flex-col gap-3">
          <Notice tone="warn">Keep it where only you can read it, e.g. ~/.ssh/{keyFile(secret.name)} with mode 600.</Notice>
          <CodeBlock label="Private key" copy>
            {secret.privateKey}
          </CodeBlock>
          <p className="text-xs text-muted-foreground">Its public key, which ddash keeps and installs:</p>
          <CodeBlock label="Public key">{secret.publicKey}</CodeBlock>
        </div>
      </Dialog>
    );
  }
  return (
    <Dialog
      title="Generate an SSH Key"
      subtitle="ddash creates an Ed25519 key pair, keeps its public key and gives you its private key once."
      onClose={close}
      footer={
        <FormActions>
          <Button variant="ghost" onClick={close}>
            Cancel
          </Button>
          <Button type="submit" form="generate-ssh-key" disabled={!name.trim() || generate.pending}>
            Generate
          </Button>
        </FormActions>
      }
    >
      <form
        id="generate-ssh-key"
        className="flex flex-col gap-3"
        onSubmit={(e) => {
          e.preventDefault();
          void go();
        }}
      >
        <Field label="Name" hint="Letters, digits, ., _ and -, e.g. laptop.">
          <TextInput value={name} onChange={(e) => setName(e.target.value)} placeholder="laptop" autoFocus />
        </Field>
        {generate.error && <Notice tone="down">{generate.error.rawMessage}</Notice>}
      </form>
    </Dialog>
  );
}

function PasteDialog({ onClose }: { onClose: () => void }) {
  const [name, setName] = useState("");
  const [key, setKey] = useState("");
  const add = useAction(ProxmoxService.method.addSSHKey);
  const go = async () => {
    if (await add.run({ name: name.trim(), publicKey: key.trim() })) onClose();
  };
  return (
    <Dialog
      title="Paste a public key"
      subtitle="A key you already use: ddash stores it under a name and installs it on Workloads."
      onClose={onClose}
      footer={
        <FormActions>
          <Button variant="ghost" onClick={onClose}>
            Cancel
          </Button>
          <Button type="submit" form="add-ssh-key" disabled={!name.trim() || !key.trim() || add.pending}>
            Add
          </Button>
        </FormActions>
      }
    >
      <form
        id="add-ssh-key"
        className="flex flex-col gap-3"
        onSubmit={(e) => {
          e.preventDefault();
          void go();
        }}
      >
        <Field label="Name" hint="Letters, digits, ., _ and -.">
          <TextInput value={name} onChange={(e) => setName(e.target.value)} placeholder="desktop" autoFocus />
        </Field>
        <Field label="Public key" hint="Its authorized_keys line, e.g. the content of ~/.ssh/id_ed25519.pub. Never the private key.">
          <TextArea value={key} onChange={(e) => setKey(e.target.value)} rows={4} placeholder="ssh-ed25519 AAAA… you@laptop" />
        </Field>
        {add.error && <Notice tone="down">{add.error.rawMessage}</Notice>}
      </form>
    </Dialog>
  );
}
