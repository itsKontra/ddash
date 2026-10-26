import { timestampDate, timestampMs } from "@bufbuild/protobuf/wkt";
import { certWarning, checkStatus, workloadName, workloadPage, workloadText } from "@ddash/display";
import { usePluginLink, useQuery, type Tone } from "@ddash/plugin";
import { AppsService, ProxyHostWarning, type ProxyHost, type Workload } from "@ddash/proto/apps/apps_pb";
import { ChecksService, type Check } from "@ddash/proto/checks/checks_pb";
import { DataTable, matches, Notice, Panel, SearchField, StatusDot, StatusList, useQueryState, type Column } from "@ddash/ui";

/**
 * apps' Proxy Hosts with their Apps' Checks. Reads only: the Check status
 * and certificate come from the App's Check, so ddash probes nothing for
 * this Page.
 */
function useProxyHosts() {
  const hosts = useQuery(AppsService.method.listProxyHosts, {});
  const checks = useQuery(ChecksService.method.listChecks, {});
  const byApp = checks.data && new Map(checks.data.checks.filter((c) => c.appId).map((c) => [c.appId, c]));
  return {
    hosts: hosts.data?.proxyHosts,
    byApp,
    appsError: hosts.data ? undefined : hosts.error,
    checksError: checks.data ? undefined : checks.error,
  };
}

/** A warning on the row's dot. */
const short: Record<ProxyHostWarning, string> = {
  [ProxyHostWarning.UNSPECIFIED]: "",
  [ProxyHostWarning.NO_WORKLOAD]: "upstream matches no Workload",
  [ProxyHostWarning.NO_TLS]: "no TLS",
};

/** A warning as the Warnings list says it, e.g. "upstream http://198.18.2.99 matches no Workload". */
const describe = (host: ProxyHost, w: ProxyHostWarning) => (w === ProxyHostWarning.NO_WORKLOAD ? `upstream ${host.upstream} matches no Workload` : short[w]);

/** proxy_pass without its nginx variables: "http://jellyfin:8096$request_uri" reads "http://jellyfin:8096". */
const bareUpstream = (upstream: string) => upstream.replace(/\$(\{\w+\}|\w+)/g, "");

const keyOf =(h: ProxyHost) => `${h.file}:${h.line}:${h.serverNames[0] ?? ""}`;

/** A Proxy Host with its App's Check: undefined without an App or while checks can't answer, null when the App has none. */
interface Row {
  host: ProxyHost;
  check: Check | null | undefined;
}

/** A text sort value that puts a row without one after the rest, ascending. */
const lastIfNone = (value: string | undefined) => (value === undefined ? "1" : `0${value}`);

/** The Check column's order, Down first; a row whose Check status is unknown comes after these. */
const checkRank: Record<Tone, number> = { down: 0, warn: 1, maint: 2, up: 3, info: 4, idle: 5 };

const columns: readonly Column<Row>[] = [
  {
    id: "domains",
    header: "Domains",
    rowHeader: true,
    sortable: true,
    sortValue: ({ host }) => host.serverNames[0] ?? "",
    cell: ({ host }) => <Domains host={host} />,
  },
  {
    id: "upstream",
    header: "Upstream",
    mono: true,
    sortable: true,
    sortValue: ({ host }) => bareUpstream(host.upstream),
    cell: ({ host }) => {
      const upstream = bareUpstream(host.upstream);
      return host.upstreamServers.length ? `${upstream} → ${host.upstreamServers.join(", ")}` : upstream;
    },
  },
  { id: "app", header: "App", sortable: true, sortValue: ({ host }) => lastIfNone(host.app?.name), cell: ({ host }) => <AppName host={host} /> },
  {
    id: "check",
    header: "Check",
    sortable: true,
    sortValue: ({ host, check }) => (host.app && check !== undefined ? checkRank[checkStatus(check ?? undefined).tone] : Number.MAX_SAFE_INTEGER),
    cell: ({ host, check }) => host.app && <CheckLine check={check} />,
  },
  {
    id: "workload",
    header: "Workload",
    sortable: true,
    sortValue: ({ host }) => lastIfNone(host.app?.workload && workloadName(host.app.workload)),
    cell: ({ host }) => (host.app ? host.app.workload ? <WorkloadName workload={host.app.workload} /> : "none" : "—"),
  },
  {
    id: "certificate",
    header: "Certificate",
    sortable: true,
    sortValue: ({ check }) => (check?.certExpires ? timestampMs(check.certExpires) : Number.MAX_SAFE_INTEGER),
    cell: ({ check }) => <Certificate check={check} />,
  },
];

/** Whether a Proxy Host matches the search: any of its domains, its upstream and servers, its App's or its Workload's name. */
const found = (q: string, host: ProxyHost) =>
  matches(
    q,
    ...host.serverNames,
    bareUpstream(host.upstream),
    ...host.upstreamServers,
    host.app?.name,
    host.app?.workload && workloadName(host.app.workload),
  );

/** By first domain, the order rows a sort ties stay in. */
const byDomain = (a: ProxyHost, b: ProxyHost) => (a.serverNames[0] ?? "").localeCompare(b.serverNames[0] ?? "", undefined, { numeric: true });

/** The Page's Warnings and Proxy Hosts Panels. */
export function ProxyHosts() {
  const { hosts, byApp, appsError, checksError } = useProxyHosts();
  const [q, setQ] = useQueryState("q", "");
  const warned = (hosts ?? []).filter((h) => h.warnings.length);
  const shown = (hosts ?? []).filter((h) => found(q, h)).sort(byDomain);
  return (
    <>
      {warned.length > 0 && (
        <Panel title="Warnings">
          <StatusList
            items={warned.flatMap((h) => h.warnings.map((w) => ({ tone: "warn" as const, title: h.serverNames[0], meta: describe(h, w) })))}
          />
        </Panel>
      )}
      <Panel
        title="Proxy Hosts"
        flush
        toolbar={<SearchField label="Search Proxy Hosts" value={q} onChange={setQ} placeholder="Search by domain, upstream, App or Workload" />}
      >
        {appsError ? (
          <div className="p-4">
            <Notice tone="down">apps unavailable: {appsError.rawMessage}</Notice>
          </div>
        ) : (
          <>
            {checksError && (
              <div className="p-4">
                <Notice tone="warn">checks unavailable: {checksError.rawMessage}</Notice>
              </div>
            )}
            <DataTable
              rows={shown.map((host) => ({ host, check: host.app && byApp ? (byApp.get(host.app.id) ?? null) : undefined }))}
              rowKey={({ host }) => keyOf(host)}
              columns={columns}
              sortKey="sort"
              defaultSort="domains"
              loading={!hosts}
              empty={hosts?.length && q ? `No Proxy Hosts match "${q}"` : "No server block forwards to an upstream."}
            />
          </>
        )}
      </Panel>
    </>
  );
}

/** A Proxy Host's domains, the first marked when something about the block looks wrong. */
function Domains({ host }: { host: ProxyHost }) {
  const [first, ...more] = host.serverNames;
  const warnings = host.warnings.map((w) => short[w]);
  return (
    <>
      <span className="flex items-center gap-2">
        {warnings.length > 0 && <StatusDot tone="warn" label={warnings.join("; ")} />}
        {first}
      </span>
      {more.map((n) => (
        <span key={n} className="block text-xs font-normal text-muted-foreground">
          {n}
        </span>
      ))}
    </>
  );
}

function AppName({ host }: { host: ProxyHost }) {
  const href = usePluginLink("apps");
  if (!host.app) return <span className="text-muted-foreground">no App</span>;
  return href ? (
    <a href={href} className="hover:underline">
      {host.app.name}
    </a>
  ) : (
    host.app.name
  );
}

function CheckLine({ check }: { check: Check | null | undefined }) {
  if (check === undefined) return <span className="text-muted-foreground">—</span>;
  const s = checkStatus(check ?? undefined);
  return (
    <span className="flex items-center gap-2">
      <StatusDot tone={s.tone} label={`Check: ${s.label}`} />
      <span className="text-muted-foreground">{s.label}</span>
    </span>
  );
}

/** When the certificate the App's Check last saw expires, warned within 14 days. */
function Certificate({ check }: { check: Check | null | undefined }) {
  if (!check?.certExpires) return "—";
  const d = timestampDate(check.certExpires);
  const warning = certWarning(check);
  return (
    <span className="flex flex-col">
      <time dateTime={d.toISOString()}>{d.toLocaleDateString(undefined, { dateStyle: "medium" })}</time>
      {warning && <span className="text-xs text-warn-text">{warning.replace(/^certificate /, "")}</span>}
    </span>
  );
}

/** "LXC grafana · running", linked to its Proxmox detail or the Docker Page when that Plugin is installed. */
function WorkloadName({ workload: w }: { workload: Workload }) {
  const page = workloadPage(w.ref);
  // Hooks run unconditionally: "" is never an installed Plugin.
  const href = usePluginLink(page?.plugin ?? "", page?.path);
  const text = workloadText(w);
  return href && page ? (
    <a href={href} className="hover:text-foreground hover:underline">
      {text}
    </a>
  ) : (
    text
  );
}
