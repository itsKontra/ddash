import { durationMs, timestampDate } from "@bufbuild/protobuf/wkt";
import { useQuery } from "@ddash/plugin";
import { CoreService, type AuditChain, type AuditEntry } from "@ddash/proto/ddash/core/core_pb";
import {
  Button,
  CodeBlock,
  DataTable,
  EmptyState,
  Facts,
  noMatch,
  Notice,
  PageTitle,
  Panel,
  Pill,
  Presence,
  SearchField,
  Select,
  Sheet,
  StatusList,
  Tabs,
  useQueryState,
  type Column,
  type Tone,
} from "@ddash/ui";
import { useEffect, useEffectEvent, useState } from "react";

/** What the Audit Log is filtered by, empty meaning any, and its order. */
interface Filter {
  actorKind: string;
  target: string;
  outcome: string;
  query: string;
  /** "oldest" first, or "" for newest. */
  order: string;
}

/** The actor tabs; "anyone" asks for every actor kind. */
const actorKinds = [
  { id: "operator", label: "Operators" },
  { id: "component", label: "Components" },
  { id: "core", label: "Core" },
  { id: "anyone", label: "Anyone" },
];

const outcomes: [string, string][] = [
  ["", "Any outcome"],
  ["ok", "OK"],
  ["failed", "Failed"],
  ["interrupted", "Interrupted"],
  ["running", "Running"],
];

/**
 * The Audit Log Page under Settings: every Action as one chain, newest
 * first, rooted in what the actor started; a row opens its hops in a Sheet.
 * It starts on the Operator's own Actions and loads further chains a page
 * at a time, so the core searches and sorts them. The search, the filters
 * and the sort sit in the URL. Read at the NONE tier: on mount and after
 * Actions.
 */
export function AuditLog() {
  const [actor, setActor] = useQueryState("actor", "operator");
  const [target, setTarget] = useQueryState("target", "");
  const [outcome, setOutcome] = useQueryState("outcome", "");
  const [q, setQ] = useQueryState("q", "");
  // The DataTable's sort, by When alone: it picks the core's order.
  const [sort] = useQueryState("sort", defaultSort);
  const [targets, setTargets] = useState<string[]>([]);
  const filter: Filter = { actorKind: actor === "anyone" ? "" : actor, target, outcome, query: q, order: sort === "when" ? "oldest" : "" };
  const toolbar = (
    <>
      <div className="overflow-x-auto">
        <Tabs label="Actor" value={actor} onChange={setActor} options={actorKinds} />
      </div>
      <SearchField label="Search Actions" value={q} onChange={setQ} placeholder="Search by Action, target or actor" />
      <div className="flex flex-wrap gap-2 sm:ml-auto">
        <Select label="Target" value={target} onChange={(e) => setTarget(e.target.value)}>
          <option value="">Any target</option>
          {withSelected(targets, target).map((t) => (
            <option key={t} value={t}>
              {t}
            </option>
          ))}
        </Select>
        <Select label="Outcome" value={outcome} onChange={(e) => setOutcome(e.target.value)}>
          {outcomes.map(([v, text]) => (
            <option key={v} value={v}>
              {text}
            </option>
          ))}
        </Select>
      </div>
    </>
  );
  return (
    <div>
      <PageTitle title="Audit Log" subtitle="Every Action ddash took, kept for a year." back={{ href: "/settings", label: "Settings" }} />
      {/* A new search, filter or order starts again from the first page. */}
      <Panel flush toolbar={toolbar}>
        <Pages key={JSON.stringify(filter)} filter={filter} onTargets={setTargets} />
      </Panel>
    </div>
  );
}

/** Newest first, as the core reads the Audit Log by default. */
const defaultSort = "-when";

/** targets, keeping the selected one listed while a reload is on its way. */
const withSelected = (targets: string[], selected: string) =>
  selected === "" || targets.includes(selected) ? targets : [...targets, selected].sort();

/** A page of chains, once its answer is in. */
interface Loaded {
  token: string;
  next?: string;
  chains?: AuditChain[];
  error?: string;
}

/** The pages loaded so far, as one table: each starts at its token, the first at "". */
function Pages({ filter, onTargets }: { filter: Filter; onTargets: (targets: string[]) => void }) {
  const [pages, setPages] = useState<Loaded[]>([{ token: "" }]);
  const [shown, setShown] = useState<AuditChain>();
  const settled = (i: number, page: Omit<Loaded, "token">) => setPages((ps) => ps.map((p, j) => (j === i ? { ...p, ...page } : p)));
  const last = pages[pages.length - 1];
  const chains = pages.flatMap((p) => p.chains ?? []);
  const error = pages.find((p) => p.error)?.error;
  return (
    <>
      {pages.map((p, i) => (
        <Page key={p.token} request={{ ...filter, pageToken: p.token }} onSettled={(page) => settled(i, page)} onTargets={i === 0 ? onTargets : undefined} />
      ))}
      {error && (
        <Notice tone="down" title="The Audit Log can't be read">
          {error}
        </Notice>
      )}
      {(!error || chains.length > 0) && (
        <DataTable
          rows={chains}
          rowKey={(c) => c.id}
          columns={columns}
          onRowClick={setShown}
          sortKey="sort"
          defaultSort={defaultSort}
          loading={!pages[0].chains}
          empty={
            <EmptyState icon="scroll-text" title={filter.query.trim() ? noMatch("Actions", filter.query) : "No Actions match"}>
              Try Anyone, or another target or outcome.
            </EmptyState>
          }
        />
      )}
      {last.next && (
        <div className="flex justify-center border-t border-border p-4">
          <Button variant="outline" onClick={() => setPages((ps) => [...ps, { token: last.next! }])}>
            {filter.order === "oldest" ? "Load newer Actions" : "Load older Actions"}
          </Button>
        </div>
      )}
      <Presence>{shown && <ChainSheet chain={shown} onClose={() => setShown(undefined)} />}</Presence>
    </>
  );
}

/** One page of chains, reported up as its answer comes in. */
function Page({
  request,
  onSettled,
  onTargets,
}: {
  request: Filter & { pageToken: string };
  onSettled: (page: Omit<Loaded, "token">) => void;
  onTargets?: (targets: string[]) => void;
}) {
  const { data, error } = useQuery(CoreService.method.listAuditLog, request);
  // The callbacks change every render; only a new answer is news.
  const report = useEffectEvent((d: NonNullable<typeof data>) => {
    onSettled({ next: d.nextPageToken, chains: d.chains, error: undefined });
    onTargets?.(d.targets);
  });
  const fail = useEffectEvent((message: string) => onSettled({ error: message }));
  useEffect(() => {
    if (data) report(data);
  }, [data]);
  useEffect(() => {
    if (error && !data) fail(error.message);
  }, [error, data]);
  return null;
}

const root = (c: AuditChain) => c.entries[0] as AuditEntry | undefined;
const timeOf = (e?: AuditEntry) => (e?.time ? timestampDate(e.time) : undefined);
const actorOf = (e?: AuditEntry) => (e ? (e.username ? `${e.actorKind}:${e.username}` : e.actorKind) : "");
const tookOf = (e?: AuditEntry) => (e?.duration ? formatDuration(durationMs(e.duration)) : "running");
const toneOf = (outcome: string): Tone => (outcome === "ok" ? "up" : outcome === "interrupted" ? "warn" : outcome === "" ? "idle" : "down");

const columns: Column<AuditChain>[] = [
  { id: "action", header: "Action", rowHeader: true, cell: (c) => <ActionName entry={root(c)} /> },
  { id: "when", header: "When", sortable: true, sortValue: (c) => timeOf(root(c))?.getTime() ?? 0, cell: (c) => <When entry={root(c)} /> },
  { id: "actor", header: "Actor", hideBelow: "sm", cell: (c) => <span className="text-muted-foreground">{actorOf(root(c))}</span> },
  { id: "outcome", header: "Outcome", cell: (c) => <ChainOutcome chain={c} /> },
  { id: "hops", header: "Hops", align: "right", hideBelow: "sm", cell: (c) => <span className="tabular-nums">{c.entries.length}</span> },
  { id: "took", header: "Took", align: "right", hideBelow: "sm", cell: (c) => <span className="tabular-nums">{tookOf(root(c))}</span> },
];

function When({ entry }: { entry?: AuditEntry }) {
  const at = timeOf(entry);
  return at ? (
    <time dateTime={at.toISOString()} className="text-xs text-muted-foreground tabular-nums">
      {at.toLocaleString()}
    </time>
  ) : null;
}

function ActionName({ entry: e }: { entry?: AuditEntry }) {
  if (!e) return null;
  return (
    <span className="inline-flex items-center gap-2">
      {e.target}
      <span className="font-mono text-xs font-normal text-muted-foreground">{e.method}</span>
      {e.disruptive && <Pill tone="warn">disruptive</Pill>}
    </span>
  );
}

function Outcome({ outcome }: { outcome: string }) {
  return (
    <Pill tone={toneOf(outcome)} dot>
      {outcome === "" ? "running" : outcome}
    </Pill>
  );
}

/** The root's outcome, and how many hops after it weren't ok. */
function ChainOutcome({ chain }: { chain: AuditChain }) {
  const [first, ...rest] = chain.entries;
  const notOk = rest.filter((e) => e.outcome !== "ok").length;
  return (
    <span className="inline-flex items-center gap-2">
      <Outcome outcome={first?.outcome ?? ""} />
      {notOk > 0 && (
        <span className="text-xs font-semibold text-down-text">
          {notOk} {notOk === 1 ? "hop" : "hops"} not ok
        </span>
      )}
    </span>
  );
}

/** A chain in a wide Sheet: its failure, its facts, and every hop with its request. */
function ChainSheet({ chain, onClose }: { chain: AuditChain; onClose: () => void }) {
  const first = root(chain);
  if (!first) return null;
  return (
    <Sheet wide kicker="Audit Log · Action" title={`${first.target} · ${first.method}`} subtitle={timeOf(first)?.toLocaleString()} onClose={onClose}>
      <div className="flex flex-col gap-6">
        {first.message && <Notice tone={toneOf(first.outcome) === "warn" ? "warn" : "down"}>{first.message}</Notice>}
        <Facts
          items={[
            { label: "Actor", value: actorOf(first) },
            { label: "Outcome", value: <ChainOutcome chain={chain} /> },
            { label: "Took", value: tookOf(first) },
            { label: "Hops", value: chain.entries.length },
          ]}
        />
        <StatusList
          timeline
          items={chain.entries.map((e) => ({
            tone: toneOf(e.outcome),
            title: (
              <span className="inline-flex items-center gap-2">
                {e.target}
                <span className="font-mono text-xs font-normal text-muted-foreground">{e.method}</span>
                {e.disruptive && <Pill tone="warn">disruptive</Pill>}
              </span>
            ),
            aside: (
              <span className="inline-flex items-center gap-2">
                <Outcome outcome={e.outcome} />
                {e.duration && formatDuration(durationMs(e.duration))}
              </span>
            ),
            meta: [actorOf(e), e.message].filter(Boolean).join(" · "),
            body: e.request && <CodeBlock label="Request">{e.request}</CodeBlock>,
          }))}
        />
      </div>
    </Sheet>
  );
}

/** 3 ms, 1.5 s, 12 s, 2 min 5 s. */
export function formatDuration(ms: number): string {
  if (ms < 1000) return `${Math.round(ms)} ms`;
  if (ms < 10_000) return `${(ms / 1000).toFixed(1)} s`;
  if (ms < 60_000) return `${Math.round(ms / 1000)} s`;
  const s = Math.round(ms / 1000);
  return `${Math.floor(s / 60)} min ${s % 60} s`;
}
