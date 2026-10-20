import { timestampDate } from "@bufbuild/protobuf/wkt";
import { usePluginLink, useQuery } from "@ddash/plugin";
import { AppsService, type Workload } from "@ddash/proto/apps/apps_pb";
import { ChecksService, type Check } from "@ddash/proto/checks/checks_pb";
import { Kind } from "@ddash/proto/ddash/workload/workload_pb";
import { StatusDot } from "@ddash/ui";
import { certWarning, checkStatus } from "./check-status";
import { describe, proxyHosts, type ProxyHost } from "./proxy-hosts";
import { useSnapshot } from "./snapshot";

/**
 * The Proxy Hosts joined with apps' Apps and checks' Checks. Reads only:
 * the Check status and certificate come from the App's Check, so ddash
 * probes nothing for this Page.
 */
function useProxyHosts() {
  const { snapshot } = useSnapshot();
  const apps = useQuery(AppsService.method.listApps, {});
  const checks = useQuery(ChecksService.method.listChecks, {});
  const byApp = checks.data && new Map(checks.data.checks.filter((c) => c.appId).map((c) => [c.appId, c]));
  return {
    hosts: snapshot && proxyHosts(snapshot, apps.data?.apps, byApp),
    appsError: apps.data ? undefined : apps.error,
    checksError: checks.data ? undefined : checks.error,
  };
}

const cell = "px-3 py-2 align-top";

/** The Page's Warnings and Proxy Hosts sections. */
export function ProxyHosts() {
  const { hosts, appsError, checksError } = useProxyHosts();
  if (!hosts) return null;
  const warned = hosts.filter((h) => h.warnings.length);
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
                <li key={`${h.key} ${w}`} className="flex items-center gap-2">
                  <StatusDot tone="warn" label="warning" />
                  <span>
                    <span className="font-medium">{h.name}</span>: {describe(h, w)}
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
        {hosts.length === 0 ? (
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
                  <Row key={h.key} host={h} />
                ))}
              </tbody>
            </table>
          </div>
        )}
      </section>
    </>
  );
}

function Row({ host }: { host: ProxyHost }) {
  const [first, ...more] = host.server.serverNames;
  return (
    <tr aria-label={host.name} className="border-b border-border last:border-b-0">
      <th scope="row" className={`${cell} pl-4 text-left font-normal`}>
        <span className="flex items-center gap-2 font-medium">
          {host.warnings.length > 0 && <StatusDot tone="warn" label={host.warnings.join("; ")} />}
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
      <td className={cell}>{host.app && <CheckLine check={host.check} />}</td>
      <td aria-label="Workload" className={`${cell} text-muted-foreground`}>
        {host.app ? host.app.workload ? <WorkloadName workload={host.app.workload} /> : "none" : "—"}
      </td>
      <td aria-label="Certificate" className={`${cell} pr-4 text-muted-foreground`}>
        <Certificate check={host.check} />
      </td>
    </tr>
  );
}

function AppName({ host }: { host: ProxyHost }) {
  const href = usePluginLink("apps");
  if (host.app === undefined) return <span className="text-muted-foreground">—</span>;
  if (host.app === null) return <span className="text-muted-foreground">no App</span>;
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

const kindLabel: Record<Kind, string> = {
  [Kind.UNSPECIFIED]: "Workload",
  [Kind.LXC]: "LXC",
  [Kind.VM]: "VM",
  [Kind.DOCKER]: "Docker",
  [Kind.EXTERNAL]: "External",
};

/** The Plugin whose Page lists a Workload of this kind. */
const pluginOf: Partial<Record<Kind, string>> = { [Kind.LXC]: "proxmox", [Kind.VM]: "proxmox", [Kind.DOCKER]: "docker" };

/** "LXC grafana · running", linked to the Proxmox or Docker Page when that Plugin is installed. */
function WorkloadName({ workload: w }: { workload: Workload }) {
  const kind = w.ref?.kind ?? Kind.UNSPECIFIED;
  // Hooks run unconditionally: "" is never an installed Plugin.
  const href = usePluginLink(pluginOf[kind] ?? "");
  const name = `${kindLabel[kind]} ${w.name || w.ref?.id || w.ref?.host || ""}`.trim();
  const text = w.state ? `${name} · ${w.state}` : name;
  return href ? (
    <a href={href} className="hover:text-foreground hover:underline">
      {text}
    </a>
  ) : (
    text
  );
}
