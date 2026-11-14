import { ago } from "@ddash/display";
import type { Tunnel } from "@ddash/proto/cloudflare-api/cloudflare_api_pb";
import { DnsState, type ListPublicHostnamesResponse, type PublicHostname } from "@ddash/proto/cloudflare-tunnel/cloudflare_tunnel_pb";
import {
  ActionStrip,
  Button,
  DataTable,
  Dialog,
  Field,
  FormActions,
  Notice,
  PageTitle,
  Panel,
  Pill,
  Presence,
  Skeleton,
  StatusList,
  TextInput,
  type Column,
} from "@ddash/ui";
import { useId, useState, type ReactNode } from "react";
import { PlanDialog, type ActionInit } from "./plan";
import { SetUp, connections, dnsLook, nameOf, tunnelLook, usePublicHostnames, useTunnel } from "./tunnel";

/**
 * The Cloudflare Tunnel Page: its connectors and their state, then the
 * Public Hostnames with the local service each forwards to, each added,
 * edited, removed, disabled and enabled through a Plan Dialog.
 */
export function TunnelPage() {
  const tunnel = useTunnel();
  const list = usePublicHostnames();
  const [dialog, setDialog] = useState<ReactNode>();
  const close = () => setDialog(undefined);
  const writable = !!list.data?.setUp && list.data.remotelyManaged;
  return (
    <div>
      <PageTitle
        kicker="Network"
        title="Cloudflare Tunnel"
        subtitle="Its connectors, and the Public Hostnames it serves from the internet."
        actions={
          !tunnel.notSetUp && (
            <Button size="sm" disabled={!writable} onClick={() => setDialog(<HostnameForm zones={list.data?.zones ?? []} onClose={close} open={setDialog} />)}>
              Add Public Hostname
            </Button>
          )
        }
      />
      {tunnel.notSetUp ? (
        <SetUp />
      ) : (
        <div className="flex flex-col gap-4">
          {list.data?.setUp && !list.data.remotelyManaged && (
            <Notice tone="info" title="Locally managed">
              This Cloudflare Tunnel runs from cloudflared&apos;s config.yml: ddash shows its Public Hostnames, and only a remotely managed Tunnel can be changed here.
            </Notice>
          )}
          <Connectors tunnel={tunnel.tunnel} error={tunnel.error?.rawMessage} now={tunnel.dataUpdatedAt} />
          <Hostnames list={list.data} error={list.error?.rawMessage} writable={writable} open={setDialog} close={close} />
        </div>
      )}
      <Presence>{dialog}</Presence>
    </div>
  );
}

/** Connectors: the Tunnel's state, and each cloudflared with its connections. */
function Connectors({ tunnel, error, now }: { tunnel?: Tunnel; error?: string; now: number }) {
  if (!tunnel) {
    return <Panel title="Connectors">{error ? <Notice tone="down">cloudflare-api can&apos;t read the Tunnel: {error}</Notice> : <Skeleton lines={2} />}</Panel>;
  }
  const look = tunnelLook[tunnel.status];
  return (
    <Panel
      title="Connectors"
      description={`${tunnel.name}: ${look.title}.`}
      actions={
        <Pill tone={look.tone} dot>
          {look.label}
        </Pill>
      }
    >
      {error && <Notice tone="warn">The last read failed: {error}</Notice>}
      {tunnel.connectors.length === 0 ? (
        <p className="text-sm text-muted-foreground">No connector is connected: run cloudflared with the Tunnel&apos;s token.</p>
      ) : (
        <StatusList
          items={tunnel.connectors.map((k) => ({
            tone: k.connections.length > 0 ? "up" : "down",
            title: `cloudflared ${k.version} · ${k.arch}`,
            aside: k.runAt ? `up ${ago(k.runAt, now)}` : undefined,
            meta: k.connections.length
              ? `${k.connections.length} connection${k.connections.length === 1 ? "" : "s"}: ${k.connections.map((c) => c.coloName).join(", ")} · from ${[...new Set(k.connections.map((c) => c.originIp))].join(", ")}`
              : "No connection",
          }))}
        />
      )}
      <p className="mt-3 text-xs text-muted-foreground">
        {tunnel.connectors.length} connector{tunnel.connectors.length === 1 ? "" : "s"}, {connections(tunnel)} connections in all.
      </p>
    </Panel>
  );
}

const columns: readonly Column<PublicHostname>[] = [
  { id: "hostname", header: "Hostname", rowHeader: true, sortable: true, sortValue: (p) => nameOf(p), cell: (p) => nameOf(p) },
  { id: "service", header: "Local service", mono: true, sortable: true, sortValue: (p) => p.fields?.service ?? "", cell: (p) => p.fields?.service },
  {
    id: "state",
    header: "State",
    sortable: true,
    sortValue: (p) => (p.enabled ? 0 : 1),
    cell: (p) => (p.enabled ? <Pill tone="up">Enabled</Pill> : <Pill>Disabled</Pill>),
  },
  {
    id: "dns",
    header: "DNS",
    hideBelow: "sm",
    cell: (p) =>
      p.enabled ? (
        <span title={p.dnsDetail || undefined}>
          <Pill tone={dnsLook[p.dns].tone}>{dnsLook[p.dns].label}</Pill>
        </span>
      ) : (
        <span className="text-muted-foreground">–</span>
      ),
  },
];

/** Public Hostnames: the table, each row's strip with its Plan Dialogs. */
function Hostnames({
  list,
  error,
  writable,
  open,
  close,
}: {
  list?: ListPublicHostnamesResponse;
  error?: string;
  writable: boolean;
  open: (d: ReactNode) => void;
  close: () => void;
}) {
  const rows = list?.publicHostnames ?? [];
  const problems = rows.filter((p) => p.enabled && p.dns !== DnsState.OK).length;
  return (
    <Panel
      flush
      title="Public Hostnames"
      description={list?.catchAll ? `Any other hostname gets ${list.catchAll}.` : "Each forwards from the internet to a local service, through its proxied CNAME."}
    >
      {error && <Notice tone="down">cloudflare-tunnel can&apos;t list the Public Hostnames: {error}</Notice>}
      {problems > 0 && (
        <Notice tone="warn">
          {problems === 1 ? "A Public Hostname's" : `${problems} Public Hostnames'`} DNS doesn&apos;t route to the Tunnel: disable and enable {problems === 1 ? "it" : "them"} to restore the proxied CNAME, or fix the record in Cloudflare.
        </Notice>
      )}
      <DataTable
        rows={rows}
        rowKey={(p) => `${p.fields?.hostname}|${p.fields?.path}`}
        columns={columns}
        loading={!list && !error}
        empty="The Tunnel serves no Public Hostname yet"
        rowLabel={nameOf}
        strip={(p) => (writable ? <RowStrip hostname={p} zones={list?.zones ?? []} open={open} close={close} /> : null)}
      />
    </Panel>
  );
}

/** A row's strip: Edit…, Disable… or Enable…, and Remove…. */
function RowStrip({ hostname: p, zones, open, close }: { hostname: PublicHostname; zones: readonly string[]; open: (d: ReactNode) => void; close: () => void }) {
  const name = { hostname: p.fields?.hostname ?? "", path: p.fields?.path ?? "" };
  const plan = (title: string, confirm: string, action: ActionInit, danger?: boolean) =>
    open(<PlanDialog title={title} confirm={confirm} action={action} danger={danger} onClose={close} />);
  return (
    <ActionStrip summary={p.enabled ? `Forwards to ${p.fields?.service}` : `Disabled: its settings are kept in ddash, without its ingress rule and CNAME.`}>
      <Button size="sm" variant="outline" onClick={() => open(<HostnameForm editing={p} zones={zones} onClose={close} open={open} />)}>
        Edit…
      </Button>
      {p.enabled ? (
        <Button size="sm" variant="outline" onClick={() => plan(`Disable ${nameOf(p)}`, "Disable", { action: { case: "disable", value: { name } } }, true)}>
          Disable…
        </Button>
      ) : (
        <Button size="sm" variant="outline" onClick={() => plan(`Enable ${nameOf(p)}`, "Enable", { action: { case: "enable", value: { name } } })}>
          Enable…
        </Button>
      )}
      <Button size="sm" variant="danger" onClick={() => plan(`Remove ${nameOf(p)}`, "Remove", { action: { case: "remove", value: { name } } }, true)}>
        Remove…
      </Button>
    </ActionStrip>
  );
}

/**
 * Add or edit a Public Hostname: its hostname, path and local service.
 * Saving opens the Plan Dialog of the Action in its place.
 */
function HostnameForm({ editing, zones, onClose, open }: { editing?: PublicHostname; zones: readonly string[]; onClose: () => void; open: (d: ReactNode) => void }) {
  const form = useId();
  const [hostname, setHostname] = useState(editing?.fields?.hostname ?? "");
  const [path, setPath] = useState(editing?.fields?.path ?? "");
  const [service, setService] = useState(editing?.fields?.service ?? "");
  const fields = { hostname: hostname.trim(), path: path.trim(), service: service.trim() };
  const title = editing ? `Edit ${nameOf(editing)}` : "Add Public Hostname";
  const submit = () => {
    const action: ActionInit = editing
      ? { action: { case: "edit", value: { name: { hostname: editing.fields?.hostname ?? "", path: editing.fields?.path ?? "" }, fields } } }
      : { action: { case: "add", value: { fields } } };
    open(<PlanDialog title={title} confirm={editing ? "Save" : "Add"} action={action} onClose={onClose} />);
  };
  return (
    <Dialog
      title={title}
      subtitle={editing && !editing.enabled ? "It's disabled: the change is kept in ddash until it's enabled." : "Cloudflare serves the hostname from the internet and forwards it to the service."}
      onClose={onClose}
      footer={
        <FormActions>
          <Button variant="ghost" onClick={onClose}>
            Cancel
          </Button>
          <Button type="submit" form={form} disabled={!fields.hostname || !fields.service}>
            Review Plan
          </Button>
        </FormActions>
      }
    >
      <form
        id={form}
        className="flex flex-col gap-4"
        onSubmit={(e) => {
          e.preventDefault();
          submit();
        }}
      >
        <Field label="Hostname" hint={zones.length ? `In ${zones.join(", ")}.` : undefined}>
          <TextInput value={hostname} onChange={(e) => setHostname(e.target.value)} placeholder="app.home.example" autoComplete="off" />
        </Field>
        <Field label="Path" hint="A path regex it matches, e.g. ^/api; empty for every path.">
          <TextInput value={path} onChange={(e) => setPath(e.target.value)} autoComplete="off" />
        </Field>
        <Field label="Local service" hint="Where cloudflared forwards to, e.g. http://198.18.1.20:8080.">
          <TextInput value={service} onChange={(e) => setService(e.target.value)} placeholder="http://198.18.1.20:8080" autoComplete="off" />
        </Field>
      </form>
    </Dialog>
  );
}
