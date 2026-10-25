import { useQuery } from "@ddash/plugin";
import { PfsenseApiService, type Gateway } from "@ddash/proto/pfsense-api/pfsense_api_pb";
import { DataTable, Notice, PageTitle, Panel, Pill, Skeleton, StatusDot, StatusList, type Column, type Tone } from "@ddash/ui";
import { AdguardStats, ProtectionBadge } from "./adguard";
import { DevicesTable, NewCount } from "./devices-table";
import { RemoteAccessPeers } from "./remote-access";
import { ThroughputRates } from "./throughput";
import { members, ms, percent, stateTone, tiers, useTunnel } from "./tunnel";

/** The network Page: the VPN Tunnel's tier, the gateways, throughput, the Devices, Remote Access and AdGuard. */
export function Network() {
  return (
    <div>
      <PageTitle title="Network" subtitle="The firewall's gateways, the VPN Tunnel, throughput, the Devices, Remote Access and AdGuard." />
      <div className="flex flex-col gap-6">
        <VpnTunnel />
        <Gateways />
        <Panel title="Throughput">
          <ThroughputRates />
        </Panel>
        <DevicesTable aside={<NewCount />} />
        <RemoteAccessPeers />
        <Panel title="AdGuard" actions={<ProtectionBadge />}>
          <AdguardStats />
        </Panel>
      </div>
    </div>
  );
}

function VpnTunnel() {
  const { data, error } = useTunnel();
  const tier = data && tiers[data.tier];
  return (
    <Panel title="VPN Tunnel" actions={tier && <Pill tone={tier.tone}>{tier.label}</Pill>}>
      <div className="flex flex-col gap-3">
        {error && !data && <Notice tone="down">{error.rawMessage}</Notice>}
        {!data && !error && <Skeleton lines={2} />}
        {data?.error && <Notice tone="warn">{data.error}</Notice>}
        {data && data.gateways.length > 0 && (
          <StatusList
            items={members(data).map((m) => ({
              tone: stateTone[m.state],
              title: m.kind,
              meta: `${m.gateway.name} · tier ${m.gateway.tier}`,
              aside: m.state,
            }))}
          />
        )}
        {data && <p className="text-xs text-muted-foreground">Failover group {data.group || "unknown"}, read every minute.</p>}
      </div>
    </Panel>
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

const gatewayColumns: Column<Gateway>[] = [
  { id: "gateway", header: "Gateway", rowHeader: true, cell: (g) => g.name },
  {
    id: "status",
    header: "Status",
    cell: (g) => {
      const dot = gatewayDot(g);
      return <StatusDot tone={dot.tone} label={dot.label} />;
    },
  },
  { id: "latency", header: "Latency", align: "right", mono: true, cell: (g) => ms(g.delayMs) },
  { id: "loss", header: "Loss", align: "right", mono: true, cell: (g) => percent(g.lossPercent) },
];

function Gateways() {
  const { data, error } = useQuery(PfsenseApiService.method.listGateways, {});
  const failed = error && !data;
  return (
    <Panel title="Gateways" flush>
      {failed ? (
        <Notice tone="down">{error.rawMessage}</Notice>
      ) : (
        <DataTable rows={data?.gateways ?? []} rowKey={(g) => g.name} columns={gatewayColumns} loading={!data} empty="pfSense reports no gateways." />
      )}
    </Panel>
  );
}
