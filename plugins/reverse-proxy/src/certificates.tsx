import { expiry, relative } from "@ddash/display";
import { usePluginLink } from "@ddash/plugin";
import type { App } from "@ddash/proto/apps/apps_pb";
import { CertificateState, type Certificate } from "@ddash/proto/nginx-proxy-manager-api/nginx_proxy_manager_api_pb";
import { DataTable, Notice, Panel, Pill, type Column, type Tone } from "@ddash/ui";
import { Fragment } from "react";
import { Link } from "react-router";
import { useApps } from "./apps";
import { Time, useCertificates } from "./npm";

/** Each certificate state's tone and label, as nginx-proxy-manager-api tells the state. */
export const stateLook: Record<CertificateState, { tone: Tone; label: string }> = {
  [CertificateState.UNSPECIFIED]: { tone: "idle", label: "Unknown" },
  [CertificateState.OK]: { tone: "up", label: "OK" },
  [CertificateState.NOT_RENEWING]: { tone: "warn", label: "Not renewing" },
  [CertificateState.EXPIRES_SOON]: { tone: "warn", label: "Expires soon" },
  [CertificateState.EXPIRED]: { tone: "down", label: "Expired" },
};

/** The Apps whose Proxy Host uses the certificate. */
const using = (apps: readonly App[], cert: Certificate) => apps.filter((a) => cert.id !== 0n && a.proxyHost?.fields?.certificateId === cert.id);

/** The Apps using a certificate, each linked to its pane on the Apps Page when that Plugin is installed. */
function Apps({ apps }: { apps: readonly App[] }) {
  const page = usePluginLink("apps");
  if (apps.length === 0) return <span className="text-muted-foreground">None</span>;
  return (
    <>
      {apps.map((a, i) => (
        <Fragment key={a.id}>
          {i > 0 && ", "}
          {page ? <Link to={`${page}?app=${encodeURIComponent(a.id)}`}>{a.name}</Link> : a.name}
        </Fragment>
      ))}
    </>
  );
}

/** Certificates: every one Nginx Proxy Manager has, with its state and the Apps using it. */
export function Certificates() {
  const { certificates, error } = useCertificates();
  const { list } = useApps();
  const apps = list?.apps ?? [];
  const columns: readonly Column<Certificate>[] = [
    { id: "name", header: "Name", rowHeader: true, sortable: true, sortValue: (c) => c.niceName, cell: (c) => c.niceName },
    { id: "domains", header: "Domains", cell: (c) => c.domainNames.join(", ") },
    {
      id: "expires",
      header: "Expires",
      sortable: true,
      sortValue: (c) => expiry(c).getTime(),
      cell: (c) => (
        <span className="flex flex-col">
          <Time at={expiry(c)} />
          <span className="text-xs text-muted-foreground">{relative(expiry(c))}</span>
        </span>
      ),
    },
    { id: "apps", header: "Apps", cell: (c) => <Apps apps={using(apps, c)} /> },
    {
      id: "state",
      header: "State",
      cell: (c) => {
        const s = stateLook[c.state];
        return (
          <span className="flex flex-col items-start gap-1">
            <Pill tone={s.tone}>{s.label}</Pill>
            {c.stateMessage && <span className="text-xs text-muted-foreground">{c.stateMessage}</span>}
          </span>
        );
      },
    },
  ];
  return (
    <Panel title="Certificates" flush>
      {!certificates && error ? (
        <Notice tone="down">Couldn't read the certificates: {error.rawMessage}</Notice>
      ) : (
        <DataTable
          rows={certificates ?? []}
          rowKey={(c) => String(c.id)}
          columns={columns}
          loading={!certificates}
          empty="No certificates in Nginx Proxy Manager"
        />
      )}
    </Panel>
  );
}
