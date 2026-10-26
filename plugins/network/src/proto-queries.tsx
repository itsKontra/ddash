// PROTOTYPE (wayfinder: "What do the Queries panel's collapsed and expanded
// views look like?"): three variants of a Device's Queries Panel, switchable
// with ?variant=A|B|C and the floating bar, against an in-memory query log
// (?scenario= picks its shape). Throwaway; lives on prototype/queries-panel
// only. Nothing here calls AdGuard.
//
//   A In place   "Show all" in the Panel's header expands it where it is
//                (queries.view=all); a Segmented All/Blocked/Allowed and a
//                Type Select; two-way Newer/Older pages of 50.
//   B Sheet      the Panel stays 5 rows; "All queries" opens a wide Sheet
//                (queries.open=1) with Tabs and a Type Select; no paging:
//                the newest 50 matches and a note to narrow the search.
//   C Search     a search box is always in the Panel; typing or a filter
//                expands it (expanded = any queries.* param); Answer and
//                Type Selects; one-way "Older than 13:12" cursor and "Back
//                to newest".
//
// Every variant: the collapsed view is the last 5 blocked (one AdGuard
// call); the expanded one runs a bounded loop in ddash that stops at 50
// matches or after scanning 2,000 log lines, and says so when the budget
// runs out first.
import {
  Button,
  DataTable,
  EmptyState,
  Icon,
  Notice,
  Panel,
  Pill,
  Segmented,
  SearchField,
  Select,
  Sheet,
  Skeleton,
  Tabs,
  useQueryState,
  type Column,
} from "@ddash/ui";
import { useEffect, useMemo, useState, type ReactNode } from "react";
import { useSearchParams } from "react-router";

// ---- the fake log ---------------------------------------------------------

type Mode = "all" | "blocked" | "allowed";
interface Q {
  i: number; // its place in the log, newest 0: the cursor
  time: Date;
  domain: string;
  type: string;
  blocked: boolean;
  rule: string;
  status: string;
  cached: boolean;
}

export type Scenario = "normal" | "quiet" | "empty" | "sparse" | "error" | "anonymized" | "slow";
export const scenarios: Record<Scenario, string> = {
  normal: "Normal (6,000 lines)",
  quiet: "Nothing blocked",
  empty: "No queries at all",
  sparse: "Sparse (budget runs out)",
  error: "AdGuard unreachable",
  anonymized: "anonymize_client_ip on",
  slow: "Slow (3 s)",
};

const allowedDomains = [
  "api.github.com",
  "www.google.com",
  "i.ytimg.com",
  "time.cloudflare.com",
  "connectivity-check.ubuntu.com",
  "registry.npmjs.org",
  "updates.signal.org",
  "pool.ntp.org",
  "mail.proton.me",
  "cdn.jsdelivr.net",
  "en.wikipedia.org",
  "s3.eu-central-1.amazonaws.com",
  "github-cloud.s3.amazonaws.com",
  "push.apple.com",
  "home-assistant.lan",
  "_dns.resolver.arpa",
  "20.1.168.192.in-addr.arpa",
];
const blockedDomains: [string, string][] = [
  ["doubleclick.net", "||doubleclick.net^"],
  ["app-measurement.com", "||app-measurement.com^"],
  ["telemetry.microsoft.com", "||telemetry.microsoft.com^"],
  ["ads.youtube.com", "||ads.youtube.com^"],
  ["graph.facebook.com", "||graph.facebook.com^$important"],
  ["sentry.io", "||sentry.io^"],
  ["metrics.icloud.com", "||metrics.icloud.com^"],
  ["analytics.tiktok.com", "||analytics.tiktok.com^"],
];
const types = ["A", "AAAA", "HTTPS", "PTR", "SVCB", "TXT"];

function rng(seed: number) {
  let s = seed;
  return () => ((s = (s * 1103515245 + 12345) % 2147483648) / 2147483648);
}

function makeLog(scenario: Scenario): Q[] {
  if (scenario === "empty" || scenario === "anonymized") return [];
  const r = rng(42);
  const blockRate = scenario === "quiet" ? 0 : scenario === "sparse" ? 0.0015 : 0.07;
  const now = Date.now();
  let t = now - 4_000;
  const log: Q[] = [];
  for (let i = 0; i < 6000; i++) {
    t -= Math.floor(r() * 25_000);
    const blocked = r() < blockRate;
    const b = blockedDomains[Math.floor(r() * blockedDomains.length)];
    const type = r() < 0.55 ? "A" : types[Math.floor(r() * types.length)];
    log.push({
      i,
      time: new Date(t),
      domain: blocked ? b[0] : allowedDomains[Math.floor(r() * allowedDomains.length)],
      type: type === "PTR" && !blocked ? "PTR" : type,
      blocked,
      rule: blocked ? b[1] : "",
      status: r() < 0.03 ? "NXDOMAIN" : "NOERROR",
      cached: r() < 0.4,
    });
  }
  return log;
}

/** The prototype's matches(): every whitespace word in some field, case-insensitively. */
const matches = (query: string, ...fields: string[]) =>
  query
    .toLowerCase()
    .split(/\s+/)
    .filter(Boolean)
    .every((w) => fields.some((f) => f.toLowerCase().includes(w)));

const ScanBudget = 2000;
export const PageSize = 50;

interface Page {
  rows: Q[];
  scanned: number; // log lines this request looked at
  older: number | null; // the next cursor: where an "older" request starts; null at the log's end
  exhausted: boolean; // the scan budget ran out before PageSize matches
  oldestScanned?: Date;
}

/** Fakes ListQueries: AdGuard's cursor paging, then ddash's bounded filter loop. */
function useFakeQueries(
  scenario: Scenario,
  req: { mode: Mode; type: string; q: string; from: number; limit: number; adguardSide?: boolean },
): { data?: Page; error?: string } {
  const log = useMemo(() => makeLog(scenario), [scenario]);
  const [state, setState] = useState<{ key: string; data?: Page; error?: string }>({ key: "" });
  const key = JSON.stringify([scenario, req]);
  useEffect(() => {
    const t = setTimeout(
      () => {
        if (scenario === "error") {
          setState({ key, error: "adguard-api: Get \"http://10.0.0.53/control/querylog\": dial tcp 10.0.0.53:80: connect: connection refused" });
          return;
        }
        const rows: Q[] = [];
        let i = req.from;
        // AdGuard filters by blocked/allowed itself (reason) and scans 50k lines;
        // a text search or a type filter runs in ddash with the 2,000-line budget.
        const budget = req.adguardSide || (!req.q && req.type === "any") ? 50_000 : ScanBudget;
        for (; i < log.length && rows.length < req.limit && i - req.from < budget; i++) {
          const x = log[i];
          if (req.mode === "blocked" && !x.blocked) continue;
          if (req.mode === "allowed" && x.blocked) continue;
          if (req.type !== "any" && x.type !== req.type) continue;
          if (req.q && !matches(req.q, x.domain, x.rule)) continue;
          rows.push(x);
        }
        setState({
          key,
          data: {
            rows,
            scanned: i - req.from,
            older: i < log.length ? i : null,
            exhausted: rows.length < req.limit && i < log.length,
            oldestScanned: log[i - 1]?.time,
          },
        });
      },
      scenario === "slow" ? 3000 : 350,
    );
    return () => clearTimeout(t);
  }, [key]); // eslint-disable-line
  return state.key === key ? state : {};
}

// ---- shared bits ----------------------------------------------------------

const clock = (d: Date) => d.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit", second: "2-digit" });
const hm = (d: Date) => d.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });
const ago = (d: Date) => {
  const m = Math.round((Date.now() - d.getTime()) / 60_000);
  if (m < 1) return "just now";
  if (m < 60) return `${m} min ago`;
  const h = Math.round(m / 60);
  return h < 24 ? `${h} h ago` : `${Math.round(h / 24)} d ago`;
};

const answer = (q: Q) =>
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

const fullColumns: Column<Q>[] = [
  { id: "domain", header: "Domain", rowHeader: true, cell: (q) => q.domain, sortable: true, sortValue: (q) => q.domain },
  { id: "time", header: "Time", mono: true, cell: (q) => clock(q.time), sortable: true, sortValue: (q) => -q.i },
  { id: "type", header: "Type", cell: (q) => <span className="text-muted-foreground">{q.type}</span>, sortable: true, sortValue: (q) => q.type, hideBelow: "sm" },
  { id: "answer", header: "Answer", cell: answer },
];

const blockedColumns: Column<Q>[] = [
  { id: "domain", header: "Domain", rowHeader: true, cell: (q) => q.domain },
  { id: "rule", header: "Rule", mono: true, cell: (q) => <span className="text-xs text-muted-foreground">{q.rule}</span>, hideBelow: "sm" },
  { id: "time", header: "When", cell: (q) => <span className="text-muted-foreground">{ago(q.time)}</span>, align: "right" },
];

const modes: Mode[] = ["all", "blocked", "allowed"];
const modeLabel: Record<Mode, string> = { all: "All", blocked: "Blocked", allowed: "Allowed" };
const modeOf = (s: string): Mode => (modes.includes(s as Mode) ? (s as Mode) : "all");

function TypeSelect({ value, onChange }: { value: string; onChange: (v: string) => void }) {
  return (
    <Select label="Type" value={value} onChange={(e) => onChange(e.target.value)}>
      <option value="any">Any type</option>
      {types.map((t) => (
        <option key={t} value={t}>
          {t}
        </option>
      ))}
    </Select>
  );
}

/** What a Device shows when AdGuard can't tell its queries apart. */
function Anonymized({ ip }: { ip: string }) {
  return (
    <EmptyState icon="eye-off" title="AdGuard hides client addresses">
      AdGuard's “Anonymize client IP” setting is on, so its log can't tell {ip}'s queries apart. Turn it off in AdGuard's DNS settings to see them here.
    </EmptyState>
  );
}

const failed = (error: string) => <Notice tone="down" title="Can't read AdGuard's query log">{error}</Notice>;

/** The empty text for a search that found nothing, per #112: No <thing> matches "<query>". */
function emptyFor(ip: string, mode: Mode, type: string, q: string, scanned: number) {
  const what = `${mode === "all" ? "" : mode + " "}${type === "any" ? "" : type + " "}queries`;
  if (q) return `No ${what} match “${q}” in the ${scanned.toLocaleString()} log lines searched.`;
  if (mode === "all" && type === "any") return `No queries from ${ip} in AdGuard's log.`;
  return `No ${what} from ${ip} in AdGuard's log.`;
}

/** The collapsed view's 5 blocked rows: one AdGuard-side call, shared by A and C. */
function useBlocked5(scenario: Scenario) {
  return useFakeQueries(scenario, { mode: "blocked", type: "any", q: "", from: 0, limit: 5, adguardSide: true });
}

function Collapsed({ ip, scenario, list }: { ip: string; scenario: Scenario; list?: boolean }) {
  const { data, error } = useBlocked5(scenario);
  if (scenario === "anonymized") return <Anonymized ip={ip} />;
  if (error) return failed(error);
  if (list) {
    if (!data) return <div className="p-5"><Skeleton lines={5} /></div>;
    if (!data.rows.length) return <p className="px-5 py-6 text-sm text-muted-foreground">Nothing from {ip} is blocked in AdGuard's log.</p>;
    return (
      <ul className="divide-y divide-border">
        {data.rows.map((q) => (
          <li key={q.i} className="flex items-baseline gap-3 px-5 py-2.5 text-sm">
            <span className="text-down">
              <Icon name="shield-ban" size="sm" />
            </span>
            <span className="min-w-0 flex-1">
              <span className="font-semibold break-all">{q.domain}</span>
              <span className="ml-2 font-mono text-xs text-muted-foreground max-sm:hidden">{q.rule}</span>
            </span>
            <span className="shrink-0 text-xs text-muted-foreground tabular-nums">{ago(q.time)}</span>
          </li>
        ))}
      </ul>
    );
  }
  return (
    <DataTable
      rows={data?.rows ?? []}
      rowKey={(q) => String(q.i)}
      columns={blockedColumns}
      loading={!data}
      density="compact"
      empty={`Nothing from ${ip} is blocked in AdGuard's log.`}
    />
  );
}

/** The line under an expanded table: how far the search reached. */
function Reach({ page, children }: { page: Page; children?: ReactNode }) {
  return (
    <div className="flex flex-wrap items-center gap-x-4 gap-y-2 border-t border-border px-5 py-3 text-xs text-muted-foreground">
      <span className="min-w-0 flex-1">{children}</span>
    </div>
  );
}

// ---- A: in place ----------------------------------------------------------

function VariantA({ ip, scenario }: { ip: string; scenario: Scenario }) {
  const [view, setView] = useQueryState("queries.view", "blocked5");
  const open = view === "all";
  const [q, setQ] = useQueryState("queries.q", "");
  const [mode, setMode] = useQueryState("queries.mode", "all");
  const [type, setType] = useQueryState("queries.type", "any");
  // A stack of cursors: Newer pops, Older pushes. Not in the URL: a cursor goes stale as the log grows.
  const [stack, setStack] = useState<number[]>([0]);
  const from = stack[stack.length - 1];
  useEffect(() => setStack([0]), [q, mode, type]);
  const toggle = (
    <Button variant="ghost" size="sm" icon={open ? "chevron-up" : "chevron-down"} onClick={() => setView(open ? "blocked5" : "all")} aria-expanded={open}>
      {open ? "Show less" : "Show all"}
    </Button>
  );
  if (!open) {
    return (
      <Panel title="Queries" description={`The last 5 queries AdGuard blocked from ${ip}.`} actions={toggle} flush>
        <Collapsed ip={ip} scenario={scenario} />
      </Panel>
    );
  }
  return (
    <Panel
      title="Queries"
      description={`Everything AdGuard logged from ${ip}, newest first, ${PageSize} at a time.`}
      actions={toggle}
      toolbar={
        scenario !== "anonymized" && (
          <>
            <SearchField label="Search queries" placeholder="Search domains and rules" value={q} onChange={setQ} />
            <Segmented label="Answer" value={modeLabel[modeOf(mode)]} options={modes.map((m) => modeLabel[m])} onChange={(l) => setMode(modes.find((m) => modeLabel[m] === l) ?? "all")} />
            <TypeSelect value={type} onChange={setType} />
          </>
        )
      }
      flush
    >
      <AExpanded ip={ip} scenario={scenario} q={q} mode={modeOf(mode)} type={type} from={from} page={stack.length} onOlder={(c) => setStack([...stack, c])} onNewer={() => setStack(stack.slice(0, -1))} />
    </Panel>
  );
}

function AExpanded(p: { ip: string; scenario: Scenario; q: string; mode: Mode; type: string; from: number; page: number; onOlder: (c: number) => void; onNewer: () => void }) {
  const { data, error } = useFakeQueries(p.scenario, { mode: p.mode, type: p.type, q: p.q, from: p.from, limit: PageSize });
  if (p.scenario === "anonymized") return <Anonymized ip={p.ip} />;
  if (error) return failed(error);
  return (
    <>
      <DataTable rows={data?.rows ?? []} rowKey={(x) => String(x.i)} columns={fullColumns} loading={!data} density="compact" sortKey="queries.sort" empty={data && emptyFor(p.ip, p.mode, p.type, p.q, data.scanned)} />
      {data && (data.rows.length > 0 || data.older !== null) && (
        <div className="flex flex-wrap items-center gap-x-4 gap-y-2 border-t border-border px-5 py-3 text-xs text-muted-foreground">
          <span className="min-w-0 flex-1">
            {data.exhausted ? (
              <>
                Searched {data.scanned.toLocaleString()} log lines back to {data.oldestScanned && hm(data.oldestScanned)}: {data.rows.length} match.
              </>
            ) : (
              <>
                Page {p.page}
                {data.rows.length > 0 && ` · ${clock(data.rows[0].time)} to ${clock(data.rows[data.rows.length - 1].time)}`}
              </>
            )}
          </span>
          <span className="flex gap-2">
            <Button variant="outline" size="sm" icon="chevron-left" disabled={p.page === 1} onClick={p.onNewer}>
              Newer
            </Button>
            <Button variant="outline" size="sm" disabled={data.older === null} onClick={() => data.older !== null && p.onOlder(data.older)}>
              {data.exhausted ? "Search older" : "Older"}
              <Icon name="chevron-right" size="sm" />
            </Button>
          </span>
        </div>
      )}
    </>
  );
}

// ---- B: Sheet -------------------------------------------------------------

function VariantB({ ip, scenario }: { ip: string; scenario: Scenario }) {
  const [open, setOpen] = useQueryState("queries.open", "");
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
        <Collapsed ip={ip} scenario={scenario} list />
      </Panel>
      {open && <BSheet ip={ip} scenario={scenario} onClose={() => setOpen("")} />}
    </>
  );
}

function BSheet({ ip, scenario, onClose }: { ip: string; scenario: Scenario; onClose: () => void }) {
  const [q, setQ] = useQueryState("queries.q", "");
  const [mode, setMode] = useQueryState("queries.mode", "all");
  const [type, setType] = useQueryState("queries.type", "any");
  // "Search older" widens the scan from the same newest end; the cap stays 50 rows.
  const [reach, setReach] = useState(0);
  useEffect(() => setReach(0), [q, mode, type]);
  const m = modeOf(mode);
  const { data, error } = useFakeQueries(scenario, { mode: m, type, q, from: reach, limit: PageSize });
  const close = () => {
    setQ("");
    setMode("all");
    setType("any");
    onClose();
  };
  return (
    <Sheet
      wide
      kicker="Queries"
      title={ip}
      subtitle="Everything AdGuard logged from this Device, newest first."
      onClose={close}
      footer={
        data &&
        data.rows.length === PageSize && (
          <p className="text-xs text-muted-foreground">
            The newest {PageSize} matches, back to {clock(data.rows[PageSize - 1].time)}. Search or filter to reach older ones.
          </p>
        )
      }
    >
      {scenario === "anonymized" ? (
        <Anonymized ip={ip} />
      ) : (
        <div className="flex flex-col gap-4">
          <SearchField label="Search queries" placeholder="Search domains and rules" value={q} onChange={setQ} />
          <div className="flex flex-wrap items-center gap-2">
            <Tabs label="Answer" value={m} options={modes.map((x) => ({ id: x, label: modeLabel[x] }))} onChange={setMode} />
            <span className="ml-auto">
              <TypeSelect value={type} onChange={setType} />
            </span>
          </div>
          {error && failed(error)}
          {data?.exhausted && (
            <Notice tone="info" title={`${data.rows.length} of ${PageSize} found`}>
              <span className="flex flex-wrap items-center gap-3">
                ddash searched the newest {(reach + data.scanned).toLocaleString()} log lines, back to {data.oldestScanned && hm(data.oldestScanned)}.
                {data.older !== null && (
                  <Button size="sm" variant="outline" onClick={() => setReach(data.older ?? 0)}>
                    Search {ScanBudget.toLocaleString()} older
                  </Button>
                )}
              </span>
            </Notice>
          )}
          {!error && (
            <div className="-mx-5">
              <DataTable rows={data?.rows ?? []} rowKey={(x) => String(x.i)} columns={fullColumns} loading={!data} density="compact" sortKey="queries.sort" empty={data && emptyFor(ip, m, type, q, data.scanned)} />
            </div>
          )}
        </div>
      )}
    </Sheet>
  );
}

// ---- C: search-first ------------------------------------------------------

function VariantC({ ip, scenario }: { ip: string; scenario: Scenario }) {
  const [q, setQ] = useQueryState("queries.q", "");
  const [mode, setMode] = useQueryState("queries.mode", "");
  const [type, setType] = useQueryState("queries.type", "");
  const [older, setOlder] = useQueryState("queries.older", "");
  const open = Boolean(q || mode || type || older);
  const clear = () => {
    setQ("");
    setMode("");
    setType("");
    setOlder("");
  };
  return (
    <Panel
      title="Queries"
      description={open ? `Searching everything AdGuard logged from ${ip}.` : `Recently blocked from ${ip}. Search or filter to see every query.`}
      actions={
        open && (
          <Button variant="ghost" size="sm" icon="x" onClick={clear}>
            Clear
          </Button>
        )
      }
      toolbar={
        scenario !== "anonymized" && (
          <>
            <SearchField label="Search queries" placeholder={`Search all of ${ip}'s queries`} value={q} onChange={setQ} />
            <Select label="Answer" value={mode || "any"} onChange={(e) => setMode(e.target.value === "any" ? "" : e.target.value)}>
              <option value="any">Any answer</option>
              <option value="blocked">Blocked</option>
              <option value="allowed">Allowed</option>
            </Select>
            <Select label="Type" value={type || "any"} onChange={(e) => setType(e.target.value === "any" ? "" : e.target.value)}>
              <option value="any">Any type</option>
              {types.map((t) => (
                <option key={t} value={t}>
                  {t}
                </option>
              ))}
            </Select>
            {!open && (
              <Button variant="ghost" size="sm" onClick={() => setMode("all")}>
                Every query
              </Button>
            )}
          </>
        )
      }
      flush
    >
      {open ? (
        <CExpanded ip={ip} scenario={scenario} q={q} mode={modeOf(mode || "all")} type={type || "any"} from={Number(older) || 0} onOlder={(c) => setOlder(String(c))} onNewest={() => setOlder("")} />
      ) : (
        <Collapsed ip={ip} scenario={scenario} />
      )}
    </Panel>
  );
}

function CExpanded(p: { ip: string; scenario: Scenario; q: string; mode: Mode; type: string; from: number; onOlder: (c: number) => void; onNewest: () => void }) {
  const { data, error } = useFakeQueries(p.scenario, { mode: p.mode, type: p.type, q: p.q, from: p.from, limit: PageSize });
  if (p.scenario === "anonymized") return <Anonymized ip={p.ip} />;
  if (error) return failed(error);
  const last = data?.rows[data.rows.length - 1];
  return (
    <>
      {p.from > 0 && (
        <div className="flex items-center gap-3 border-b border-border bg-muted px-5 py-2 text-xs">
          <Icon name="clock" size="sm" />
          <span className="flex-1">Older queries, not the newest.</span>
          <Button variant="ghost" size="sm" onClick={p.onNewest}>
            Back to newest
          </Button>
        </div>
      )}
      <DataTable rows={data?.rows ?? []} rowKey={(x) => String(x.i)} columns={fullColumns} loading={!data} density="compact" sortKey="queries.sort" empty={data && emptyFor(p.ip, p.mode, p.type, p.q, data.scanned)} />
      {data && data.older !== null && (
        <Reach page={data}>
          <span className="flex flex-wrap items-center gap-3">
            {data.exhausted
              ? `Only ${data.rows.length} matches in the ${data.scanned.toLocaleString()} log lines back to ${data.oldestScanned && hm(data.oldestScanned)}.`
              : `${PageSize} shown. There are more.`}
            <Button variant="outline" size="sm" icon="clock" onClick={() => data.older !== null && p.onOlder(data.older)}>
              {data.exhausted ? `Keep searching before ${data.oldestScanned && hm(data.oldestScanned)}` : `Older than ${last && hm(last.time)}`}
            </Button>
          </span>
        </Reach>
      )}
    </>
  );
}

// ---- the switcher ---------------------------------------------------------

const variants = { A: "In place", B: "Sheet", C: "Search-first" } as const;
type Variant = keyof typeof variants;
const order = Object.keys(variants) as Variant[];

/** PROTOTYPE: the Queries Panel in the variant ?variant= names, plus the floating bar. */
export function ProtoQueries({ ip }: { ip: string }) {
  const [params, setParams] = useSearchParams();
  const v = (order.includes(params.get("variant") as Variant) ? params.get("variant") : "A") as Variant;
  const scenario = (params.get("scenario") as Scenario) in scenarios ? (params.get("scenario") as Scenario) : "normal";
  const set = (k: string, val: string) =>
    setParams(
      (prev) => {
        const next = new URLSearchParams();
        // Switching variant or scenario drops the queries.* state: each variant reads it differently.
        for (const [pk, pv] of prev) if (!pk.startsWith("queries.")) next.set(pk, pv);
        next.set(k, val);
        return next;
      },
      { replace: true, preventScrollReset: true },
    );
  const step = (d: number) => set("variant", order[(order.indexOf(v) + d + order.length) % order.length]);
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key !== "ArrowLeft" && e.key !== "ArrowRight") return;
      const t = e.target as HTMLElement | null;
      if (t?.closest("input, textarea, select, [contenteditable], [role=radio], [role=tab], [role=dialog]")) return;
      step(e.key === "ArrowLeft" ? -1 : 1);
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  });
  const queryState = [...params].filter(([k]) => k.startsWith("queries."));
  return (
    <>
      {v === "A" && <VariantA ip={ip} scenario={scenario} />}
      {v === "B" && <VariantB ip={ip} scenario={scenario} />}
      {v === "C" && <VariantC ip={ip} scenario={scenario} />}
      <div className="fixed bottom-4 left-1/2 z-50 flex -translate-x-1/2 flex-col items-center gap-1">
        <code className="rounded bg-foreground/80 px-2 py-0.5 text-[11px] text-background">{queryState.length ? queryState.map(([k, val]) => `${k}=${val}`).join("  ") : "no queries.* in the URL"}</code>
        <div className="flex items-center gap-1 rounded-full bg-foreground px-2 py-1.5 text-sm text-background shadow-lg">
          <button type="button" aria-label="Previous variant" onClick={() => step(-1)} className="cursor-pointer rounded-full p-1 hover:opacity-70">
            <Icon name="chevron-left" />
          </button>
          <span className="min-w-36 text-center font-semibold">
            {v} ({variants[v]})
          </span>
          <button type="button" aria-label="Next variant" onClick={() => step(1)} className="cursor-pointer rounded-full p-1 hover:opacity-70">
            <Icon name="chevron-right" />
          </button>
          <select aria-label="Scenario" value={scenario} onChange={(e) => set("scenario", e.target.value)} className="ml-2 cursor-pointer rounded-full bg-background/20 px-2 py-0.5 text-xs text-background">
            {Object.entries(scenarios).map(([k, label]) => (
              <option key={k} value={k} className="text-foreground">
                {label}
              </option>
            ))}
          </select>
        </div>
      </div>
    </>
  );
}
