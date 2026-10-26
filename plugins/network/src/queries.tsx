import { timestampDate, type Timestamp } from "@bufbuild/protobuf/wkt";
import { useQuery } from "@ddash/plugin";
import { AdguardApiService, QueryMode, type Query } from "@ddash/proto/adguard-api/adguard_api_pb";
import {
  Button,
  DataTable,
  EmptyState,
  Icon,
  Notice,
  Panel,
  Pill,
  Presence,
  SearchField,
  Select,
  Sheet,
  Skeleton,
  Tabs,
  useQueryState,
  type Column,
  type TabOption,
} from "@ddash/ui";
import { useState } from "react";
import { useSearchParams } from "react-router";
import { count } from "./adguard";
import { ago } from "./remote-access";

/** How many blocked queries the Panel lists. */
const RecentLimit = 5;
/** How many queries the Sheet shows at most: the newest that match, with no paging. */
const SheetLimit = 50;
/** How many log lines ListQueries reads for a search or a type filter before it stops. */
const ScanBudget = 2000;

/** The URL params the Sheet keeps its state in: the Device Page's queries.* (#112). */
const prefix = "queries.";

type Mode = "all" | "blocked" | "allowed";
const modes: TabOption<Mode>[] = [
  { id: "all", label: "All" },
  { id: "blocked", label: "Blocked" },
  { id: "allowed", label: "Allowed" },
];
const modeOf = (s: string): Mode => (s === "blocked" || s === "allowed" ? s : "all");
const queryMode: Record<Mode, QueryMode> = { all: QueryMode.UNSPECIFIED, blocked: QueryMode.BLOCKED, allowed: QueryMode.ALLOWED };

/** The record types the Type Select offers, the common ones first. */
const types = ["A", "AAAA", "HTTPS", "SVCB", "PTR", "CNAME", "TXT", "MX", "SRV", "NS", "SOA"];

/** A query's time as HH:MM:SS, or HH:MM without seconds. */
const time = (t: Timestamp | undefined, seconds = true) =>
  t ? timestampDate(t).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit", second: seconds ? "2-digit" : undefined, hourCycle: "h23" }) : "—";

const answer = (q: Query) =>
  q.blocked ? (
    <span className="inline-flex items-center gap-2">
      <Pill tone="down">Blocked</Pill>
      {q.rule && <span className="text-xs break-all text-muted-foreground">{q.rule}</span>}
    </span>
  ) : (
    <span className="text-muted-foreground">
      {q.status}
      {q.cached && ", cached"}
    </span>
  );

const columns: Column<Query>[] = [
  { id: "domain", header: "Domain", rowHeader: true, cell: (q) => q.domain, sortable: true, sortValue: (q) => q.domain },
  { id: "time", header: "Time", mono: true, cell: (q) => time(q.time), sortable: true, sortValue: (q) => (q.time ? timestampDate(q.time).getTime() : 0) },
  {
    id: "type",
    header: "Type",
    cell: (q) => <span className="text-muted-foreground">{q.type}</span>,
    sortable: true,
    sortValue: (q) => q.type,
    hideBelow: "sm",
  },
  { id: "answer", header: "Answer", cell: answer },
];

const failed = (message: string) => (
  <Notice tone="down" title="Can't read AdGuard's query log">
    {message}
  </Notice>
);

/** Shown in place of both views while AdGuard anonymizes client addresses: no log line holds the Device's. */
function Anonymized({ ip }: { ip: string }) {
  return (
    <EmptyState icon="eye-off" title="AdGuard hides client addresses">
      AdGuard's “Anonymize client IP” setting is on, so its log can't tell {ip}'s queries apart. Turn it off in AdGuard's DNS settings to see them here.
    </EmptyState>
  );
}

/**
 * The Device Page's AdGuard queries: a "Recently blocked" Panel listing the
 * last 5 blocked from ip, and its All queries Sheet with the full log. The
 * Sheet's state is in the URL (queries.open, .q, .mode, .type, .sort), so
 * it survives a reload; closing it clears them all.
 */
export function DeviceQueries({ ip }: { ip: string }) {
  const { data, error, dataUpdatedAt } = useQuery(AdguardApiService.method.listQueries, { client: ip, limit: RecentLimit, mode: QueryMode.BLOCKED });
  const [open, setOpen] = useQueryState(`${prefix}open`, "");
  const [q, setQ] = useQueryState(`${prefix}q`, "");
  const [mode, setMode] = useQueryState(`${prefix}mode`, "all");
  const [type, setType] = useQueryState(`${prefix}type`, "");
  const [, setParams] = useSearchParams();
  // In one write: each of several setters would start from the same params.
  const close = () =>
    setParams(
      (prev) => {
        const next = new URLSearchParams(prev);
        for (const key of prev.keys()) if (key.startsWith(prefix)) next.delete(key);
        return next;
      },
      { replace: true, preventScrollReset: true },
    );
  const anonymized = Boolean(data?.anonymizeClientIp);
  let body;
  if (error && !data) body = failed(error.rawMessage);
  else if (anonymized) body = <Anonymized ip={ip} />;
  else if (!data)
    body = (
      <div className="p-5">
        <Skeleton lines={RecentLimit} />
      </div>
    );
  else if (data.queries.length === 0) body = <p className="px-5 py-6 text-sm text-muted-foreground">Nothing from {ip} is blocked in AdGuard's log.</p>;
  else body = <RecentlyBlocked queries={data.queries} now={dataUpdatedAt} />;
  return (
    <>
      <Panel
        title="Recently blocked"
        description={`What AdGuard last blocked from ${ip}.`}
        actions={
          <Button variant="outline" size="sm" icon="list-filter" onClick={() => setOpen("1")}>
            All queries
          </Button>
        }
        flush
      >
        {body}
      </Panel>
      {/* The filters go in as props, so a closing Sheet keeps its own while it animates out. */}
      <Presence>
        {open === "1" && (
          <QueriesSheet
            ip={ip}
            filter={{ q, mode: modeOf(mode), type }}
            onSearch={setQ}
            onMode={setMode}
            onType={setType}
            anonymized={anonymized}
            onClose={close}
          />
        )}
      </Presence>
    </>
  );
}

/** The Panel's list, each query's age as of now: when AdGuard answered. */
function RecentlyBlocked({ queries, now }: { queries: readonly Query[]; now: number }) {
  return (
    <ul className="divide-y divide-border">
      {queries.map((q, i) => (
        // AdGuard's log has no id; a query is its place in it.
        <li key={i} className="flex items-baseline gap-3 px-5 py-2.5 text-sm">
          <span className="self-center text-down">
            <Icon name="shield-ban" size="sm" />
          </span>
          <span className="min-w-0 flex-1">
            <span className="font-semibold break-all">{q.domain}</span>
            {q.rule && <span className="ml-2 font-mono text-xs break-all text-muted-foreground max-sm:hidden">{q.rule}</span>}
          </span>
          <span className="shrink-0 text-xs text-muted-foreground tabular-nums">{ago(q.time, now)}</span>
        </li>
      ))}
    </ul>
  );
}

interface Filter {
  q: string;
  mode: Mode;
  type: string;
}

/** Where a search reads from: the filter it is for (of), the cursor it resumes at, and how many log lines it read before. */
interface Scan {
  of: string;
  olderThan: string;
  linesBefore: number;
}

/** "No blocked AAAA queries match “x” in the 2,000 log lines searched.", or "No queries from <ip> in AdGuard's log." without a search. */
function nothingFound(ip: string, { q, mode, type }: Filter, lines: number) {
  const what = [mode === "all" ? "" : mode, type, "queries"].filter(Boolean).join(" ");
  return q.trim() ? `No ${what} match “${q.trim()}” in the ${count(lines)} log lines searched.` : `No ${what} from ${ip} in AdGuard's log.`;
}

function QueriesSheet({
  ip,
  filter,
  onSearch,
  onMode,
  onType,
  anonymized,
  onClose,
}: {
  ip: string;
  filter: Filter;
  onSearch: (q: string) => void;
  onMode: (mode: Mode) => void;
  onType: (type: string) => void;
  /** Whether the Panel's answer already said AdGuard anonymizes client addresses. */
  anonymized: boolean;
  onClose: () => void;
}) {
  const text = filter.q.trim();
  // A text search or a type filter runs in ddash within the scan budget; mode alone runs in AdGuard.
  const searched = Boolean(text || filter.type);
  // A cursor goes stale as the log grows, so the scan isn't in the URL; a new filter starts from the newest.
  const of = JSON.stringify(filter);
  const [kept, setScan] = useState<Scan>({ of, olderThan: "", linesBefore: 0 });
  const from = kept.of === of ? kept : { of, olderThan: "", linesBefore: 0 };
  const { data, error } = useQuery(AdguardApiService.method.listQueries, {
    client: ip,
    limit: SheetLimit,
    mode: queryMode[filter.mode],
    text,
    type: filter.type,
    olderThan: from.olderThan,
  });
  const rows = data?.queries ?? [];
  const lines = from.linesBefore + (data?.linesRead ?? 0);
  const outOfBudget = searched && data !== undefined && rows.length < SheetLimit && data.oldest !== "";
  const hidden = anonymized || Boolean(data?.anonymizeClientIp);
  return (
    <Sheet
      wide
      kicker="Queries"
      title={ip}
      subtitle="Everything AdGuard logged from this Device, newest first."
      onClose={onClose}
      footer={
        !hidden &&
        rows.length === SheetLimit && (
          <p className="text-xs text-muted-foreground">
            The newest {SheetLimit} matches, back to {time(rows[SheetLimit - 1].time)}. Search or filter to reach older ones.
          </p>
        )
      }
    >
      {hidden ? (
        <Anonymized ip={ip} />
      ) : (
        <div className="flex flex-col gap-4">
          <SearchField label="Search queries" placeholder="Search domains and rules" value={filter.q} onChange={onSearch} />
          <div className="flex flex-wrap items-center gap-2">
            <Tabs label="Answer" value={filter.mode} options={modes} onChange={onMode} />
            <span className="ml-auto">
              <Select label="Type" value={filter.type} onChange={(e) => onType(e.target.value)}>
                <option value="">Any type</option>
                {(types.includes(filter.type) || !filter.type ? types : [...types, filter.type]).map((t) => (
                  <option key={t} value={t}>
                    {t}
                  </option>
                ))}
              </Select>
            </span>
          </div>
          {error && !data ? (
            failed(error.rawMessage)
          ) : (
            <>
              {outOfBudget && (
                <Notice tone="info" title={`${rows.length} of ${SheetLimit} found`}>
                  <span className="flex flex-wrap items-center gap-3">
                    ddash searched the newest {count(lines)} log lines, back to {time(data.reached, false)}.
                    <Button
                      size="sm"
                      variant="outline"
                      onClick={() => setScan({ of, olderThan: data.oldest, linesBefore: lines })}
                    >
                      Search {count(ScanBudget)} older
                    </Button>
                  </span>
                </Notice>
              )}
              <div className="-mx-6">
                <DataTable
                  rows={rows}
                  // AdGuard's log has no id; a query is its place in it.
                  rowKey={(q) => `${rows.indexOf(q)}-${q.domain}`}
                  columns={columns}
                  loading={!data}
                  density="compact"
                  sortKey={`${prefix}sort`}
                  empty={nothingFound(ip, filter, lines)}
                />
              </div>
            </>
          )}
        </div>
      )}
    </Sheet>
  );
}
