import type { Badge, Status, Widget } from "@ddash/plugin";
import { Status as NpmStatus, type Certificate } from "@ddash/proto/nginx-proxy-manager-api/nginx_proxy_manager_api_pb";
import { Skeleton, StatusList, type Tone } from "@ddash/ui";
import { certificateState, expiry, relative } from "./certificates";
import { notConfigured, Time, useCertificates, useStatus as useNpmStatus } from "./npm";

const severity: Record<Tone, number> = { idle: 0, info: 0, maint: 0, up: 1, warn: 2, down: 3 };

/** The certificate expiring first and the worst state's tone among all; undefined without certificates. */
function nearest(certs: readonly Certificate[]): { cert: Certificate; tone: Tone } | undefined {
  if (certs.length === 0) return undefined;
  const cert = certs.reduce((a, b) => (expiry(b) < expiry(a) ? b : a));
  const tone = certs.map((c) => certificateState(c).tone).reduce((a, b) => (severity[b] > severity[a] ? b : a));
  return { cert, tone };
}

/** "legacy expires in 5 days" or "old expired 2 days ago". */
function expiresText(cert: Certificate) {
  const at = expiry(cert);
  return `${cert.niceName} ${at.getTime() < Date.now() ? "expired" : "expires"} ${relative(at)}`;
}

/** The nearest expiry, toned by the worst state among the certificates. */
function useStatus(): Status | undefined {
  const { certificates, error } = useCertificates();
  if (!certificates) {
    if (notConfigured(error)) return { tone: "warn", summary: "Set up Nginx Proxy Manager" };
    return error ? { tone: "warn", summary: `nginx-proxy-manager-api unavailable: ${error.rawMessage}` } : undefined;
  }
  const n = nearest(certificates);
  return n ? { tone: n.tone, summary: expiresText(n.cert) } : { tone: "idle", summary: "no certificates" };
}

/** The certificate expiring first and when. */
function Body() {
  const { certificates, error } = useCertificates();
  // While the certificates can't be read, the Widget's status says why.
  if (!certificates) return error ? null : <Skeleton lines={2} />;
  const n = nearest(certificates);
  if (!n) return <p className="text-sm text-muted-foreground">No certificates in Nginx Proxy Manager</p>;
  return <StatusList items={[{ tone: n.tone, title: n.cert.niceName, meta: <Time at={expiry(n.cert)} />, aside: relative(expiry(n.cert)) }]} />;
}

/** The Overview Widget: the nearest certificate expiry. */
export const certificates: Widget = {
  id: "certificates",
  title: "Certificates",
  sizes: ["S"],
  defaultSize: "S",
  Body,
  useStatus,
};

/** "!" on the Nav Entry while Nginx Proxy Manager is Down; Offline Proxy Hosts and certificate states don't badge. */
export function useBadge(): Badge | null {
  const { status } = useNpmStatus();
  return status?.status === NpmStatus.DOWN ? { text: "!", tone: "down" } : null;
}
