import { timestampDate, type Timestamp } from "@bufbuild/protobuf/wkt";
import type { ListAppsResponse, SourceRead } from "@ddash/proto/apps/apps_pb";
import { Notice } from "@ddash/ui";
import { Link } from "react-router";

/** A time, machine-readable in dateTime. */
export function Time({ at }: { at: Timestamp }) {
  const d = timestampDate(at);
  return <time dateTime={d.toISOString()}>{d.toLocaleString(undefined, { dateStyle: "medium", timeStyle: "short" })}</time>;
}

const plugins = (
  <Link to="/plugins" className="font-semibold text-primary hover:underline">
    Configure it in Plugins
  </Link>
);

/** A source that can't be read: since when and why, the last good read still shown. */
function Unreadable({ source, what, read }: { source: string; what: string; read: SourceRead }) {
  return (
    <Notice tone="warn" title={`${source} unreadable`}>
      {read.failingSince ? (
        <>
          Since <Time at={read.failingSince} />:{" "}
        </>
      ) : null}
      {read.error}. {read.readAt ? `Showing the ${what} of its last good read.` : `No ${what} read since ddash started.`}
    </Notice>
  );
}

/**
 * The Notices over the split view, each from its ListApps flag: a source
 * to set up, Proxy Hosts read-only, the Reverse Proxy's Host Override
 * problem, a source unreadable since when.
 */
export function Notices({ list }: { list: ListAppsResponse }) {
  const npm = list.proxyHosts;
  const dns = list.hostOverrides;
  return (
    <>
      {npm?.notConfigured ? (
        <Notice tone="info" title="Set up Nginx Proxy Manager">
          ddash reads the Proxy Hosts once nginx-proxy-manager-api has Nginx Proxy Manager's address and ddash's user there. {plugins}
        </Notice>
      ) : (
        npm?.error && <Unreadable source="Nginx Proxy Manager" what="Proxy Hosts" read={npm} />
      )}
      {dns?.notConfigured ? (
        <Notice tone="info" title="Set up pfSense">
          ddash reads the Host Overrides once pfsense-api has pfSense's address and API key. {plugins}
        </Notice>
      ) : (
        dns?.error && <Unreadable source="pfSense" what="Host Overrides" read={dns} />
      )}
      {npm?.readOnly && (
        <Notice tone="warn" title="Proxy Hosts are read-only">
          ddash's user in Nginx Proxy Manager has Proxy Hosts on View: Apps read, but ddash can't add, change or delete a Proxy Host. Set them to Manage under the user's
          Permissions to let it.
        </Notice>
      )}
      {list.reverseProxy?.notice && <Notice tone="warn">{list.reverseProxy.notice}</Notice>}
    </>
  );
}
