import { timestampMs } from "@bufbuild/protobuf/wkt";
import { useTail, type Status, type Widget } from "@ddash/plugin";
import { NetworkService, type ThroughputSample } from "@ddash/proto/network/network_pb";
import { EmptyState, Notice, Skeleton, Sparkline } from "@ddash/ui";

const HOUR = 60 * 60 * 1000;

/** The last hour of throughput samples, oldest first, and why the last sampling failed. */
export interface Hour {
  samples: ThroughputSample[];
  error: string;
}

/**
 * network's throughput ring, shared by the Page and the Widget: the whole
 * hour on the first call, then only the samples after the newest one held,
 * every 5 s (LIVE). Samples over an hour older than the newest drop out.
 */
export function useThroughput() {
  return useTail(NetworkService.method.getThroughput, {
    request: (hour: Hour | undefined) => ({ since: hour?.samples.at(-1)?.at }),
    merge: (hour: Hour | undefined, next): Hour => {
      const samples = [...(hour?.samples ?? []), ...next.samples];
      const newest = samples.at(-1)?.at;
      const from = newest ? timestampMs(newest) - HOUR : 0;
      return { samples: samples.filter((s) => s.at && timestampMs(s.at) > from), error: next.error };
    },
  });
}

/** One interface's download and upload, as the Operator sees them. */
export interface Direction {
  label: "WAN" | "LAN";
  down: (s: ThroughputSample) => number;
  up: (s: ThroughputSample) => number;
}

/**
 * WAN download is what it receives from the internet; LAN download is
 * what the firewall sends on to the home network.
 */
export const directions: Direction[] = [
  { label: "WAN", down: (s) => s.wanInBytesPerSecond, up: (s) => s.wanOutBytesPerSecond },
  { label: "LAN", down: (s) => s.lanOutBytesPerSecond, up: (s) => s.lanInBytesPerSecond },
];

/** A rate in bytes per second as bits per second, e.g. "20 Mbit/s". */
export function bitRate(bytesPerSecond: number): string {
  let v = bytesPerSecond * 8;
  const units = ["bit/s", "kbit/s", "Mbit/s", "Gbit/s"];
  let u = 0;
  while (v >= 1000 && u < units.length - 1) {
    v /= 1000;
    u++;
  }
  return `${v.toLocaleString("en", { maximumFractionDigits: v < 10 ? 1 : 0 })} ${units[u]}`;
}

/** One interface's current rates and its last hour. */
export function Rates({ direction, samples }: { direction: Direction; samples: ThroughputSample[] }) {
  const last = samples.at(-1);
  return (
    <li aria-label={direction.label} className="flex flex-col gap-1">
      <div className="flex items-baseline gap-2 text-sm">
        <span className="font-medium">{direction.label}</span>
        {last && (
          <span className="ml-auto text-muted-foreground tabular-nums">
            ↓ {bitRate(direction.down(last))} · ↑ {bitRate(direction.up(last))}
          </span>
        )}
      </div>
      <Sparkline label={`${direction.label} over the last hour`} series={[samples.map(direction.down), samples.map(direction.up)]} />
    </li>
  );
}

/** Up with WAN's current rates; warn while sampling fails or network is unavailable. */
function useStatus(): Status | undefined {
  const { data, error } = useThroughput();
  if (error) return { tone: "warn", summary: `network unavailable: ${error.rawMessage}` };
  if (!data) return undefined;
  if (data.error) return { tone: "warn", summary: `Throughput unknown: ${data.error}` };
  const last = data.samples.at(-1);
  if (!last) return { tone: "up", summary: "Collecting the first samples" };
  const wan = directions[0];
  return { tone: "up", summary: `WAN ↓ ${bitRate(wan.down(last))} ↑ ${bitRate(wan.up(last))}` };
}

/** M: WAN and LAN, each with its rates now and its last hour. */
function Body() {
  const { data, error } = useThroughput();
  if (!data) return error ? null : <Skeleton lines={4} />;
  if (data.samples.length === 0) return <p className="text-sm text-muted-foreground">Collecting the first samples</p>;
  return (
    <ul className="flex flex-col gap-3">
      {directions.map((d) => (
        <Rates key={d.label} direction={d} samples={data.samples} />
      ))}
    </ul>
  );
}

/** The Network Page's throughput: WAN and LAN over the last hour, and why sampling fails. */
export function ThroughputRates() {
  const { data, error } = useThroughput();
  if (error && !data) return <Notice tone="down">{error.rawMessage}</Notice>;
  if (!data) return <Skeleton height="lg" />;
  return (
    <div className="flex flex-col gap-3">
      {data.error && <Notice tone="warn">{data.error}</Notice>}
      {data.samples.length === 0 ? (
        <EmptyState icon="activity" title="Collecting the first samples">
          network samples throughput every 5 seconds.
        </EmptyState>
      ) : (
        <ul className="grid gap-4 sm:grid-cols-2">
          {directions.map((d) => (
            <Rates key={d.label} direction={d} samples={data.samples} />
          ))}
        </ul>
      )}
      <p className="text-xs text-muted-foreground">Sampled every 5 seconds; download strong, upload muted.</p>
    </div>
  );
}

/** The Overview Widget: WAN and LAN throughput over the last hour. */
export const throughput: Widget = {
  id: "throughput",
  title: "Throughput",
  sizes: ["M"],
  defaultSize: "M",
  Body,
  useStatus,
};
