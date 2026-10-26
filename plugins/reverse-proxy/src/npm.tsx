import { timestampDate, type Timestamp } from "@bufbuild/protobuf/wkt";
import { useQuery } from "@ddash/plugin";
import { NginxProxyManagerApiService } from "@ddash/proto/nginx-proxy-manager-api/nginx_proxy_manager_api_pb";

/** nginx-proxy-manager-api's GetStatus, shared by the Page and the badge. */
export function useStatus() {
  const { data, error } = useQuery(NginxProxyManagerApiService.method.getStatus, {});
  return { status: data, error };
}

/** nginx-proxy-manager-api's certificates, shared by the Page and the Widget. */
export function useCertificates() {
  const { data, error } = useQuery(NginxProxyManagerApiService.method.listCertificates, {});
  return { certificates: data?.certificates, error };
}

/** The error is the core's answer for a Component without config. */
export const notConfigured = (error: { rawMessage: string } | null | undefined) => error?.rawMessage === "not configured";

/** A time, machine-readable in dateTime. */
export function Time({ at }: { at: Timestamp | Date }) {
  const d = at instanceof Date ? at : timestampDate(at);
  return <time dateTime={d.toISOString()}>{d.toLocaleString(undefined, { dateStyle: "medium", timeStyle: "short" })}</time>;
}
