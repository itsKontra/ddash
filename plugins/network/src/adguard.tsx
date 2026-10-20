import { useQuery, type Status, type Widget } from "@ddash/plugin";
import { AdguardApiService, type GetStatsResponse, type GetStatusResponse, type TopEntry } from "@ddash/proto/adguard-api/adguard_api_pb";
import { Badge, Sparkline, Stat, StatusDot, type Tone } from "@ddash/ui";

/** AdGuard's status (FAST), shared by the Page and the Widget. */
export function useAdguardStatus() {
  return useQuery(AdguardApiService.method.getStatus, {});
}

/** The last 24 h's stats (SLOW). */
export function useAdguardStats() {
  return useQuery(AdguardApiService.method.getStats, {});
}

const numbers = new Intl.NumberFormat("en-US");

/** A count with thousands separators, e.g. "18,432". */
export const count = (n: bigint | number) => numbers.format(n);

/** The share of the day's queries blocked by filters, e.g. "15%". */
export function blockedShare(s: GetStatsResponse): string {
  const total = Number(s.dnsQueries);
  return total ? `${Math.round((Number(s.blockedFiltering) / total) * 100)}%` : "—";
}

/** Whether AdGuard protects the network, as a tone and a label. */
export function protection(s: GetStatusResponse): { tone: Tone; label: string } {
  if (!s.running) return { tone: "down", label: "Not running" };
  if (s.protectionEnabled) return { tone: "up", label: "Protection on" };
  const paused = s.protectionPausedFor;
  if (paused) return { tone: "warn", label: `Protection paused, back on in ${Math.max(1, Math.ceil(Number(paused.seconds) / 60))} min` };
  return { tone: "warn", label: "Protection off" };
}

/** Down when the DNS server isn't running, warn while protection is off or adguard-api is unavailable. */
function useStatus(): Status | undefined {
  const { data, error } = useAdguardStatus();
  const { data: stats } = useAdguardStats();
  if (error) return { tone: "warn", summary: `AdGuard unavailable: ${error.rawMessage}` };
  if (!data) return undefined;
  if (!data.running) return { tone: "down", summary: "AdGuard isn't running" };
  const p = protection(data);
  if (p.tone !== "up" || !stats) return { tone: p.tone, summary: p.label };
  return { tone: "up", summary: `${p.label}, ${blockedShare(stats)} of ${count(stats.dnsQueries)} queries blocked` };
}

/** S: protection, and the day's queries and share blocked. */
function Body() {
  const { data } = useAdguardStatus();
  const { data: stats } = useAdguardStats();
  if (!data) return null;
  const p = protection(data);
  return (
    <div className="flex flex-col gap-3">
      <p className="flex items-center gap-2 text-sm">
        <StatusDot tone={p.tone} />
        {p.label}
      </p>
      {stats && (
        <div className="flex gap-6">
          <Stat label="queries, 24 h" value={count(stats.dnsQueries)} />
          <Stat label="blocked" value={blockedShare(stats)} />
        </div>
      )}
    </div>
  );
}

/** The Overview Widget: AdGuard's protection. */
export const adguard: Widget = {
  id: "adguard",
  title: "AdGuard",
  sizes: ["S"],
  defaultSize: "S",
  Body,
  useStatus,
};

/** The Network Page section's header aside: protection, as a pill. */
export function ProtectionBadge() {
  const { data } = useAdguardStatus();
  if (!data) return null;
  const p = protection(data);
  return <Badge tone={p.tone}>{p.label}</Badge>;
}

function Top({ label, entries }: { label: string; entries: TopEntry[] }) {
  if (!entries.length) return null;
  return (
    <div className="min-w-0 flex-1">
      <h3 className="mb-1 text-xs text-muted-foreground">{label}</h3>
      <ul aria-label={label} className="flex flex-col gap-1 text-sm">
        {entries.slice(0, 5).map((e) => (
          <li key={e.name} className="flex gap-2">
            <span className="truncate">{e.name}</span>
            <span className="ml-auto text-muted-foreground tabular-nums">{count(e.count)}</span>
          </li>
        ))}
      </ul>
    </div>
  );
}

/** The Network Page's AdGuard section body: the last 24 h's stats. */
export function AdguardStats() {
  const { data: status, error } = useAdguardStatus();
  const { data: stats, error: statsError } = useAdguardStats();
  const failed = error ?? statsError;
  if (failed && !stats) return <p className="px-4 py-3 text-sm text-down">{failed.rawMessage}</p>;
  if (!stats) return null;
  const unit = stats.timeUnit === "days" ? "day" : "hour";
  return (
    <div className="flex flex-col gap-4 px-4 py-3">
      <div className="flex flex-wrap gap-6">
        <Stat label="queries, 24 h" value={count(stats.dnsQueries)} />
        <Stat label="blocked by filters" value={count(stats.blockedFiltering)} />
        <Stat label="blocked share" value={blockedShare(stats)} />
        <Stat label="avg processing" value={`${stats.avgProcessingMs.toFixed(1)} ms`} />
      </div>
      <Sparkline
        label={`Queries and blocked, per ${unit}`}
        series={[stats.dnsQueriesSeries.map(Number), stats.blockedFilteringSeries.map(Number)]}
      />
      <div className="flex flex-wrap gap-6">
        <Top label="Most blocked domains" entries={stats.topBlockedDomains} />
        <Top label="Most queried domains" entries={stats.topQueriedDomains} />
        <Top label="Most active addresses" entries={stats.topClients} />
      </div>
      {status?.version && <p className="text-xs text-muted-foreground">AdGuard Home {status.version}</p>}
    </div>
  );
}
