import { timestampDate } from "@bufbuild/protobuf/wkt";
import { certWarning, checkStatus, workloadPage, workloadText } from "@ddash/display";
import { usePluginLink, useQuery } from "@ddash/plugin";
import { AppsService, ProxyHostWarning, type ProxyHost, type Workload } from "@ddash/proto/apps/apps_pb";
import { ChecksService, type Check } from "@ddash/proto/checks/checks_pb";
import { StatusDot } from "@ddash/ui";

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

const cell = "px-3 py-2 align-top";

/** The Page's Warnings and Proxy Hosts sections. */
export function ProxyHosts() {
  const { hosts, byApp, appsError, checksError } = useProxyHosts();
  if (!hosts && !appsError) return null;
  const warned = (hosts ?? []).filter((h) => h.warnings.length);
  return (
    <>
      {warned.length > 0 && (
        <section aria-labelledby="warnings" className="rounded-ui border border-border bg-card p-4">
          <h2 id="warnings" className="mb-3 font-semibold">
            Warnings
          </h2>
          <ul className="flex flex-col gap-1 text-sm">
            {warned.flatMap((h) =>
              h.warnings.map((w) => (
                <li key={`${keyOf(h)} ${w}`} className="flex items-center gap-2">
                  <StatusDot tone="warn" label="warning" />
                  <span>
                    <span className="font-medium">{h.serverNames[0]}</span>: {describe(h, w)}
                  </span>
                </li>
              )),
            )}
          </ul>
        </section>
      )}
      <section aria-labelledby="proxy-hosts" className="rounded-ui border border-border bg-card">
        <header className="border-b border-border px-4 py-3">
          <h2 id="proxy-hosts" className="font-semibold">
            Proxy Hosts
          </h2>
          {[appsError && `apps unavailable: ${appsError.rawMessage}`, checksError && `checks unavailable: ${checksError.rawMessage}`]
            .filter(Boolean)
            .map((note) => (
              <p key={String(note)} className="text-sm text-muted-foreground">
                {note}
              </p>
            ))}
        </header>
        {!hosts ? null : hosts.length === 0 ? (
          <p className="px-4 py-3 text-sm text-muted-foreground">No server block forwards to an upstream.</p>
        ) : (
          <div className="overflow-x-auto">
            <table aria-label="Proxy Hosts" className="w-full text-sm">
              <thead>
                <tr className="border-b border-border text-left text-xs text-muted-foreground">
                  {["Domains", "Upstream", "App", "Check", "Workload", "Certificate"].map((h) => (
                    <th key={h} scope="col" className={`${cell} font-medium first:pl-4 last:pr-4`}>
                      {h}
                    </th>
                  ))}
                </tr>
              </thead>
              <tbody>
                {hosts.map((h) => (
                  <Row key={keyOf(h)} host={h} check={h.app && byApp ? (byApp.get(h.app.id) ?? null) : undefined} />
                ))}
              </tbody>
            </table>
          </div>
        )}
      </section>
    </>
  );
}

/** A Proxy Host's row; check is undefined without an App or while checks can't answer, null when the App has none. */
function Row({ host, check }: { host: ProxyHost; check: Check | null | undefined }) {
  const [first, ...more] = host.serverNames;
  const warnings = host.warnings.map((w) => short[w]);
  return (
    <tr aria-label={first} className="border-b border-border last:border-b-0">
      <th scope="row" className={`${cell} pl-4 text-left font-normal`}>
        <span className="flex items-center gap-2 font-medium">
          {warnings.length > 0 && <StatusDot tone="warn" label={warnings.join("; ")} />}
          {first}
        </span>
        {more.map((n) => (
          <span key={n} className="block text-xs text-muted-foreground">
            {n}
          </span>
        ))}
      </th>
      <td aria-label="Upstream" className={`${cell} text-muted-foreground`}>
        {host.upstreamServers.length ? `${host.upstream} → ${host.upstreamServers.join(", ")}` : host.upstream}
      </td>
      <td aria-label="App" className={cell}>
        <AppName host={host} />
      </td>
      <td className={cell}>{host.app && <CheckLine check={check} />}</td>
      <td aria-label="Workload" className={`${cell} text-muted-foreground`}>
        {host.app ? host.app.workload ? <WorkloadName workload={host.app.workload} /> : "none" : "—"}
      </td>
      <td aria-label="Certificate" className={`${cell} pr-4 text-muted-foreground`}>
        <Certificate check={check} />
      </td>
    </tr>
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
      {warning && <span className="text-xs text-warn">{warning.replace(/^certificate /, "")}</span>}
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
