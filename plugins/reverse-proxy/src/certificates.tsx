import { timestampDate } from "@bufbuild/protobuf/wkt";
import { usePluginLink } from "@ddash/plugin";
import type { App } from "@ddash/proto/apps/apps_pb";
import type { Certificate } from "@ddash/proto/nginx-proxy-manager-api/nginx_proxy_manager_api_pb";
import { DataTable, Notice, Panel, Pill, type Column, type Tone } from "@ddash/ui";
import { Fragment } from "react";
import { Link } from "react-router";
import { useApps } from "./apps";
import { Time, useCertificates } from "./npm";

const day = 86_400_000;

/** A certificate's state, from its provider and how long it has left. */
export interface CertificateState {
  tone: Tone;
  label: string;
  /** What the Operator does about it; empty when OK. */
  message: string;
}

const renewLog = "Nginx Proxy Manager hasn't renewed this: check its Let's Encrypt log.";
const upload = "Upload a new certificate in Nginx Proxy Manager.";

/**
 * Expired (down) once past its expiry; Not renewing (warn) for Let's
 * Encrypt at 20 days or less, which Nginx Proxy Manager should have
 * renewed by then; Expires soon (warn) for an uploaded one at 30 days or
 * less; else OK.
 */
export function certificateState(cert: Certificate, now = Date.now()): CertificateState {
  const left = expiry(cert).getTime() - now;
  const letsEncrypt = cert.provider === "letsencrypt";
  const message = letsEncrypt ? renewLog : upload;
  if (left < 0) return { tone: "down", label: "Expired", message };
  if (letsEncrypt && left <= 20 * day) return { tone: "warn", label: "Not renewing", message };
  if (!letsEncrypt && left <= 30 * day) return { tone: "warn", label: "Expires soon", message };
  return { tone: "up", label: "OK", message: "" };
}

export const expiry = (cert: Certificate) => (cert.expiresOn ? timestampDate(cert.expiresOn) : new Date(0));

/** "in 5 days", "today" or "2 days ago". */
export function relative(at: Date, now = Date.now()): string {
  const ms = at.getTime() - now;
  const n = Math.floor(Math.abs(ms) / day);
  if (n === 0) return "today";
  const d = n === 1 ? "1 day" : `${n} days`;
  return ms > 0 ? `in ${d}` : `${d} ago`;
}

/** The Apps whose Proxy Host uses the certificate. */
const using = (apps: readonly App[], cert: Certificate) => apps.filter((a) => a.proxyHost && a.proxyHost.certificateId !== 0n && a.proxyHost.certificateId === cert.id);

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
        const s = certificateState(c);
        return (
          <span className="flex flex-col items-start gap-1">
            <Pill tone={s.tone}>{s.label}</Pill>
            {s.message && <span className="text-xs text-muted-foreground">{s.message}</span>}
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
