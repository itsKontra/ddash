import { timestampDate } from "@bufbuild/protobuf/wkt";
import { certWarning, checkStatus, workloadPage, workloadText } from "@ddash/display";
import { usePluginLink, useQuery } from "@ddash/plugin";
import { AppsService, ProxyHostWarning, type ProxyHost, type Workload } from "@ddash/proto/apps/apps_pb";
import { ChecksService, type Check } from "@ddash/proto/checks/checks_pb";
import { DataTable, Notice, Panel, StatusDot, StatusList, type Column } from "@ddash/ui";

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

const keyOf = (h: ProxyHost) => `${h.file}:${h.line}:${h.serverNames[0] ?? ""}`;

/** A Proxy Host with its App's Check: undefined without an App or while checks can't answer, null when the App has none. */
interface Row {
  host: ProxyHost;
  check: Check | null | undefined;
}

const columns: readonly Column<Row>[] = [
  { id: "domains", header: "Domains", rowHeader: true, cell: ({ host }) => <Domains host={host} /> },
  {
    id: "upstream",
    header: "Upstream",
    mono: true,
    cell: ({ host }) => (host.upstreamServers.length ? `${host.upstream} → ${host.upstreamServers.join(", ")}` : host.upstream),
  },
  { id: "app", header: "App", cell: ({ host }) => <AppName host={host} /> },
  { id: "check", header: "Check", cell: ({ host, check }) => host.app && <CheckLine check={check} /> },
  {
    id: "workload",
    header: "Workload",
    cell: ({ host }) => (host.app ? host.app.workload ? <WorkloadName workload={host.app.workload} /> : "none" : "—"),
  },
  { id: "certificate", header: "Certificate", cell: ({ check }) => <Certificate check={check} /> },
];

/** The Page's Warnings and Proxy Hosts Panels. */
export function ProxyHosts() {
  const { hosts, byApp, appsError, checksError } = useProxyHosts();
  const warned = (hosts ?? []).filter((h) => h.warnings.length);
  return (
    <>
      {warned.length > 0 && (
        <Panel title="Warnings">
          <StatusList
            items={warned.flatMap((h) => h.warnings.map((w) => ({ tone: "warn" as const, title: h.serverNames[0], meta: describe(h, w) })))}
          />
        </Panel>
      )}
      <Panel title="Proxy Hosts" flush>
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
              rows={(hosts ?? []).map((host) => ({ host, check: host.app && byApp ? (byApp.get(host.app.id) ?? null) : undefined }))}
              rowKey={({ host }) => keyOf(host)}
              columns={columns}
              loading={!hosts}
              empty="No server block forwards to an upstream."
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
