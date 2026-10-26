import type { MessageInitShape } from "@bufbuild/protobuf";
import { useQuery } from "@ddash/plugin";
import type { ProxyHostFieldsSchema } from "@ddash/proto/apps/apps_pb";
import { NginxProxyManagerApiService, type Certificate } from "@ddash/proto/nginx-proxy-manager-api/nginx_proxy_manager_api_pb";
import { Checkbox, Field, Select, TextInput } from "@ddash/ui";

/**
 * The Proxy Host fields as the Operator set them. certificateId is unset
 * until they pick one: the certificate covering the hostname is used.
 * The flags are their wish; Nginx Proxy Manager's toggle rules apply on
 * top (resolve).
 */
export interface ProxyDraft {
  scheme: "http" | "https";
  host: string;
  port: string;
  certificateId?: bigint;
  blockExploits: boolean;
  websockets: boolean;
  forceSsl: boolean;
  http2: boolean;
  hsts: boolean;
  hstsSubdomains: boolean;
}

/** A new Proxy Host's fields: Block Common Exploits, and Force SSL and HTTP/2 once there is a certificate. */
export const newProxy = (): ProxyDraft => ({
  scheme: "http",
  host: "",
  port: "80",
  blockExploits: true,
  websockets: false,
  forceSsl: true,
  http2: true,
  hsts: false,
  hstsSubdomains: false,
});

/** Nginx Proxy Manager's certificates, to pick a Proxy Host's from. */
export function useCertificates(): readonly Certificate[] {
  const { data } = useQuery(NginxProxyManagerApiService.method.listCertificates, {});
  return data?.certificates ?? [];
}

/** The certificate covering host: one naming it, else a wildcard over its parent; 0n for none. */
export function coveringCert(certs: readonly Certificate[], host: string): bigint {
  const h = host.toLowerCase();
  const names = (c: Certificate) => c.domainNames.map((d) => d.toLowerCase());
  const exact = certs.find((c) => names(c).includes(h));
  if (exact) return exact.id;
  const dot = h.indexOf(".");
  const wildcard = dot > 0 ? certs.find((c) => names(c).includes(`*.${h.slice(dot + 1)}`)) : undefined;
  return wildcard?.id ?? 0n;
}

/**
 * The fields as Nginx Proxy Manager will save them: the certificate
 * covering host unless one was picked, and its toggle rules applied: no
 * certificate turns Force SSL and HTTP/2 off, no Force SSL turns HSTS off,
 * no HSTS turns HSTS for subdomains off.
 */
export function resolve(p: ProxyDraft, certs: readonly Certificate[], host: string): MessageInitShape<typeof ProxyHostFieldsSchema> {
  const certificateId = p.certificateId ?? coveringCert(certs, host);
  const forceSsl = certificateId !== 0n && p.forceSsl;
  const hsts = forceSsl && p.hsts;
  return {
    forwardScheme: p.scheme,
    forwardHost: p.host.trim(),
    forwardPort: Number(p.port) || 0,
    certificateId,
    blockExploits: p.blockExploits,
    websockets: p.websockets,
    forceSsl,
    http2: certificateId !== 0n && p.http2,
    hsts,
    hstsSubdomains: hsts && p.hstsSubdomains,
  };
}

/**
 * A Proxy Host's fields, as Add App (and the Reverse proxy Panel's edits)
 * set them: the upstream, the certificate from Nginx Proxy Manager's
 * existing ones, and the flags. Nginx Proxy Manager's toggle rules show as
 * unchecked, disabled boxes.
 */
export function ProxyFields({ value: p, onChange, host, certificates }: { value: ProxyDraft; onChange: (p: ProxyDraft) => void; host: string; certificates: readonly Certificate[] }) {
  const set = (patch: Partial<ProxyDraft>) => onChange({ ...p, ...patch });
  const f = resolve(p, certificates, host);
  const covering = coveringCert(certificates, host);
  const cert = certificates.find((c) => c.id === covering);
  return (
    <div className="flex flex-col gap-4">
      <div className="grid grid-cols-4 gap-2">
        <Field label="Scheme">
          <Select value={p.scheme} onChange={(e) => set({ scheme: e.target.value as ProxyDraft["scheme"] })}>
            <option value="http">http</option>
            <option value="https">https</option>
          </Select>
        </Field>
        <div className="col-span-2">
          <Field label="Upstream host">
            <TextInput value={p.host} placeholder="198.18.2.10" onChange={(e) => set({ host: e.target.value })} />
          </Field>
        </div>
        <Field label="Port">
          <TextInput value={p.port} inputMode="numeric" onChange={(e) => set({ port: e.target.value })} />
        </Field>
      </div>
      <Field
        label="Certificate"
        hint={
          cert
            ? `From Nginx Proxy Manager. ${cert.niceName} covers ${host || "the hostname"}.`
            : "From Nginx Proxy Manager. None of its certificates covers this hostname; ddash never requests one."
        }
      >
        <Select value={String(f.certificateId)} onChange={(e) => set({ certificateId: BigInt(e.target.value) })}>
          <option value="0">None (plain HTTP)</option>
          {certificates.map((c) => (
            <option key={String(c.id)} value={String(c.id)}>
              {c.niceName}
              {c.id === covering ? " (covers it)" : ""}
            </option>
          ))}
        </Select>
      </Field>
      <div className="grid grid-cols-1 gap-2 sm:grid-cols-2">
        <Checkbox label="Block Common Exploits" checked={p.blockExploits} onChange={(e) => set({ blockExploits: e.target.checked })} />
        <Checkbox label="Websockets" checked={p.websockets} onChange={(e) => set({ websockets: e.target.checked })} />
        <Checkbox label="Force SSL" checked={!!f.forceSsl} disabled={!f.certificateId} onChange={(e) => set({ forceSsl: e.target.checked })} />
        <Checkbox label="HTTP/2" checked={!!f.http2} disabled={!f.certificateId} onChange={(e) => set({ http2: e.target.checked })} />
        <Checkbox label="HSTS" checked={!!f.hsts} disabled={!f.forceSsl} onChange={(e) => set({ hsts: e.target.checked })} />
        <Checkbox label="HSTS subdomains" checked={!!f.hstsSubdomains} disabled={!f.hsts} onChange={(e) => set({ hstsSubdomains: e.target.checked })} />
      </div>
    </div>
  );
}
