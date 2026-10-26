import { timestampDate } from "@bufbuild/protobuf/wkt";
import type { Tone } from "@ddash/plugin";
import type { Certificate } from "@ddash/proto/nginx-proxy-manager-api/nginx_proxy_manager_api_pb";

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
