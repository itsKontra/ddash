import { ago } from "@ddash/display";
import { useQuery, type Status, type Widget } from "@ddash/plugin";
import type { GetStatsResponse, GetStatusResponse } from "@ddash/proto/adguard-api/adguard_api_pb";
import { PfsenseApiService } from "@ddash/proto/pfsense-api/pfsense_api_pb";
import { Icon, Notice, Pill, Row, Rows, Skeleton, Sparkline, Unit } from "@ddash/ui";
import { Fragment, type ReactNode } from "react";
import { blockedShare, count, pauseLeft, useAdguardStats, useAdguardStatus } from "./adguard";
import { tally, useDevices } from "./devices";
import { ProtectionSwitch } from "./protection";
import { peerName, useRemoteAccess } from "./remote-access";
import { lastSeen, latency, routeOut, wanGateways } from "./route";
import { bitRate, directions, useThroughput } from "./throughput";
import { useTunnel } from "./tunnel";

/** The VPN Tunnel, the WAN's gateways (undefined until pfSense's are read) and the route out. */
function useRoute() {
  const tunnel = useTunnel();
  const { data: gateways } = useQuery(PfsenseApiService.method.listGateways, {});
  const wan = tunnel.data && gateways ? wanGateways(tunnel.data, gateways.gateways) : undefined;
  return { tunnel, wan, route: tunnel.data && routeOut(tunnel.data, wan) };
}

/** The route out's tone and summary; warn when network is unavailable. */
function useStatus(): Status | undefined {
  const { tunnel, route } = useRoute();
  if (tunnel.error) return { tone: "warn", summary: `network unavailable: ${tunnel.error.rawMessage}` };
  return route && { tone: route.tone, summary: route.summary };
}

/** "Online via WireGuard", and the VPN Tunnel's and the WAN's latency and loss. */
function Headline() {
  const { tunnel, wan, route } = useRoute();
  const { data, error } = tunnel;
  if (!data || !route) return error ? <Notice tone="warn">{error.rawMessage}</Notice> : <Skeleton lines={2} />;
  return (
    <div>
      <p className="text-xl leading-tight font-extrabold tracking-tight">{route.headline}</p>
      <p className="mt-1 text-sm text-muted-foreground">
        {latency(data, wan).map((part, i) => (
          <Fragment key={part}>
            {i > 0 && " "}
            <span className="whitespace-nowrap">{i > 0 ? `· ${part}` : part}</span>
          </Fragment>
        ))}
      </p>
      {data.error && <p className="mt-1 text-xs text-warn-text">{data.error}</p>}
    </div>
  );
}

/** The WAN's last hour as a live line, with its current download and upload. */
function Wan() {
  const { data, error } = useThroughput();
  if (!data) return error ? null : <Skeleton height="sm" />;
  const wan = directions[0];
  const last = data.samples.at(-1);
  return (
    <div>
      <Sparkline label="WAN over the last hour" series={[data.samples.map(wan.down), data.samples.map(wan.up)]} />
      <p className="mt-1 flex items-center gap-3 text-xs whitespace-nowrap text-muted-foreground tabular-nums">
        {last ? (
          <>
            <span className="flex items-center gap-1">
              <Icon name="arrow-down" size="sm" label="Download" />
              <span className="font-semibold text-foreground">{bitRate(wan.down(last))}</span>
            </span>
            <span className="flex items-center gap-1">
              <Icon name="arrow-up" size="sm" label="Upload" />
              <span className="font-semibold text-foreground">{bitRate(wan.up(last))}</span>
            </span>
          </>
        ) : (
          <span>{data.error ? "Throughput unknown" : "Collecting the first samples"}</span>
        )}
        <span className="ml-auto @max-2xs:hidden">WAN, live</span>
      </p>
    </div>
  );
}

const quiet = (text: string) => <span className="font-normal text-muted-foreground">{text}</span>;

/** Devices online of all, with the New ones as a pill. */
function DevicesRow() {
  const { data, error } = useDevices();
  const n = data && tally(data.devices);
  const value = n ? (
    <>
      <span>
        {`${n.online} of ${n.total}`}
        <Unit> online</Unit>
      </span>
      {n.new > 0 && <Pill tone="info">{`${n.new} new`}</Pill>}
    </>
  ) : (
    error && quiet("unavailable")
  );
  return <Row icon="smartphone" label="Devices" value={value ?? undefined} />;
}

/** What ad blocking does now: the share of the day's queries blocked, or why it blocks nothing. */
function adBlocking(s: GetStatusResponse, stats: GetStatsResponse | undefined): ReactNode {
  if (!s.running) return <span className="text-down-text">not running</span>;
  if (!s.protectionEnabled) {
    const left = pauseLeft(s);
    return (
      <span className="text-warn-text">
        {left ? "paused" : "off"}
        {left && <Unit>{` ${left} min`}</Unit>}
      </span>
    );
  }
  if (!stats) return "on";
  return (
    <span>
      {blockedShare(stats)}
      <Unit>{` of ${count(stats.dnsQueries)}`}</Unit>
    </span>
  );
}

/** AdGuard's share blocked and its protection switch. */
function AdBlockingRow() {
  const { data, error } = useAdguardStatus();
  const { data: stats } = useAdguardStats();
  const value = data ? (
    <>
      {adBlocking(data, stats)}
      <ProtectionSwitch status={data} durations={false} />
    </>
  ) : (
    error && quiet("unavailable")
  );
  return <Row icon={data && !data.protectionEnabled ? "shield-off" : "shield-check"} label="Ad blocking" value={value ?? undefined} />;
}

/** The Remote Access peer seen last, and when; a narrow Widget keeps only when. */
function RemoteAccessRow() {
  const { data, error, dataUpdatedAt: now } = useRemoteAccess();
  let value: ReactNode;
  if (!data) value = error ? quiet("unavailable") : undefined;
  else if (data.error) value = quiet("not set up");
  else {
    const peer = lastSeen(data.peers);
    value = peer ? (
      <span className="flex min-w-0 font-normal text-muted-foreground">
        <span className="flex min-w-0 @max-2xs:hidden">
          <span className="truncate">{peerName(peer)}</span>
          <span className="shrink-0 whitespace-pre"> · </span>
        </span>
        <span className="shrink-0">{ago(peer.latestHandshake, now)}</span>
      </span>
    ) : data.peers.length ? (
      <span className="font-normal text-muted-foreground">
        never
        <Unit> connected</Unit>
      </span>
    ) : (
      quiet("no peers yet")
    );
  }
  return <Row icon="key-round" label="Remote Access" value={value} truncate />;
}

/**
 * How the home network gets out, in one Widget: the route as a sentence,
 * the latency and loss, the WAN's throughput, then Devices, ad blocking
 * and Remote Access as rows. Nothing is cut but a peer's name: in a narrow
 * Widget the units drop out (Unit).
 */
function Body() {
  return (
    <div className="@container flex flex-col gap-3">
      <Headline />
      <Wan />
      <Rows>
        <DevicesRow />
        <AdBlockingRow />
        <RemoteAccessRow />
      </Rows>
    </div>
  );
}

/** The Overview Widget: is the internet all right? */
export const networkWidget: Widget = {
  id: "network",
  title: "Network",
  sizes: ["S", "M"],
  defaultSize: "M",
  defaultOrder: 30,
  Body,
  useStatus,
};
