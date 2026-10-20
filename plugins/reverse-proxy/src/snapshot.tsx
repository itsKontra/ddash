import { timestampDate, type Timestamp } from "@bufbuild/protobuf/wkt";
import { useQuery } from "@ddash/plugin";
import { NginxConfigService } from "@ddash/proto/nginx-config/nginx_config_pb";

/** nginx-config's last read of nginx's config, shared by the Page, Widget and badge. */
export function useSnapshot() {
  const { data, error } = useQuery(NginxConfigService.method.getSnapshot, {});
  return { snapshot: data?.snapshot, error };
}

/** A read time, machine-readable in dateTime. */
export function ReadTime({ at }: { at: Timestamp }) {
  const d = timestampDate(at);
  return <time dateTime={d.toISOString()}>{d.toLocaleString(undefined, { dateStyle: "medium", timeStyle: "short" })}</time>;
}

/** The first line of nginx's error text. */
export const firstLine = (text: string) => text.split("\n", 1)[0];
