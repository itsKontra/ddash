import { timestampDate } from "@bufbuild/protobuf/wkt";
import type { Certificate } from "@ddash/proto/nginx-proxy-manager-api/nginx_proxy_manager_api_pb";

const day = 86_400_000;

/** When a certificate expires; the epoch when Nginx Proxy Manager doesn't say. */
export const expiry = (cert: Certificate) => (cert.expiresOn ? timestampDate(cert.expiresOn) : new Date(0));

/** "in 5 days", "today" or "2 days ago". */
export function relative(at: Date, now = Date.now()): string {
  const ms = at.getTime() - now;
  const n = Math.floor(Math.abs(ms) / day);
  if (n === 0) return "today";
  const d = n === 1 ? "1 day" : `${n} days`;
  return ms > 0 ? `in ${d}` : `${d} ago`;
}
