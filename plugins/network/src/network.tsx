import { useQuery } from "@ddash/plugin";
import { PfsenseApiService, type Gateway } from "@ddash/proto/pfsense-api/pfsense_api_pb";
import { Badge, PageHeader, StatusDot, type Tone } from "@ddash/ui";
import type { ReactNode } from "react";
import { RemoteAccessPeers } from "./remote-access";
import { ThroughputRates } from "./throughput";
import { members, ms, percent, stateTone, tiers, useTunnel } from "./tunnel";

/** The network Page: the VPN Tunnel's tier and every gateway's status. */
export function Network() {
  return (
    <div>
      <PageHeader title="Network" subtitle="The firewall's gateways, the VPN Tunnel, throughput and Remote Access." />
      <div className="flex flex-col gap-6">
        <VpnTunnel />
        <Gateways />
        <Section id="network-throughput" title="Throughput">
          <ThroughputRates />
        </Section>
        <Section id="network-remote-access" title="Remote Access">
          <RemoteAccessPeers />
        </Section>
      </div>
    </div>
  );
}

function Section({ id, title, aside, children }: { id: string; title: string; aside?: ReactNode; children: ReactNode }) {
  return (
    <section aria-labelledby={id} className="rounded-ui border border-border bg-card">
      <header className="flex items-center gap-3 border-b border-border px-4 py-3">
        <h2 id={id} className="font-semibold">
          {title}
        </h2>
        {aside}
      </header>
      {children}
    </section>
  );
}

function VpnTunnel() {
  const { data, error } = useTunnel();
  const tier = data && tiers[data.tier];
  return (
    <Section id="network-vpn" title="VPN Tunnel" aside={tier && <Badge tone={tier.tone}>{tier.label}</Badge>}>
      {error && !data && <p className="px-4 py-3 text-sm text-down">{error.message}</p>}
      {data?.error && <p className="px-4 py-3 text-sm text-warn">{data.error}</p>}
      {data && data.gateways.length > 0 && (
        <ul className="flex flex-col gap-2 px-4 py-3 text-sm">
          {members(data).map((m) => (
            <li key={m.gateway.name} aria-label={m.kind} className="flex items-center gap-2">
              <StatusDot tone={stateTone[m.state]} label={m.state} />
              <span className="font-medium">{m.kind}</span>
              <span className="text-muted-foreground">
                {m.gateway.name} · tier {m.gateway.tier}
              </span>
              <span className="ml-auto text-muted-foreground">{m.state}</span>
            </li>
          ))}
        </ul>
      )}
      {data && <p className="px-4 pb-3 text-xs text-muted-foreground">Failover group {data.group || "unknown"}, read every minute.</p>}
    </Section>
  );
}

/** How a gateway's pfSense status reads at a glance, and its name for screen readers. */
function gatewayDot(g: Gateway): { tone: Tone; label: string } {
  const troubled = g.substatus && g.substatus !== "none";
  switch (g.status) {
    case "online":
      return troubled ? { tone: "warn", label: `online: ${g.substatus}` } : { tone: "up", label: "online" };
    case "down":
      return { tone: "down", label: troubled ? `down: ${g.substatus}` : "down" };
    case "none":
      return { tone: "idle", label: "not monitored" };
    default:
      return { tone: "idle", label: g.status || "unknown" };
  }
}

function Gateways() {
  const { data, error } = useQuery(PfsenseApiService.method.listGateways, {});
  return (
    <Section id="network-gateways" title="Gateways">
      {error && !data && <p className="px-4 py-3 text-sm text-down">{error.message}</p>}
      {data && (
        <table aria-label="Gateways" className="w-full text-sm">
          <thead>
            <tr className="border-b border-border text-left text-xs text-muted-foreground">
              <th scope="col" className="w-6 py-2 pl-4">
                <span className="sr-only">Status</span>
              </th>
              <th scope="col" className="px-2 py-2 font-medium">
                Gateway
              </th>
              <th scope="col" className="px-2 py-2 text-right font-medium">
                Latency
              </th>
              <th scope="col" className="px-2 py-2 pr-4 text-right font-medium">
                Loss
              </th>
            </tr>
          </thead>
          <tbody>
            {data.gateways.map((g) => {
              const dot = gatewayDot(g);
              return (
                <tr key={g.name} className="border-b border-border last:border-b-0">
                  <td className="w-6 py-2 pl-4">
                    <StatusDot tone={dot.tone} label={dot.label} />
                  </td>
                  <th scope="row" className="px-2 py-2 text-left font-medium">
                    {g.name}
                  </th>
                  <td className="px-2 py-2 text-right text-muted-foreground tabular-nums">{ms(g.delayMs)}</td>
                  <td className="px-2 py-2 pr-4 text-right text-muted-foreground tabular-nums">{percent(g.lossPercent)}</td>
                </tr>
              );
            })}
          </tbody>
        </table>
      )}
    </Section>
  );
}
