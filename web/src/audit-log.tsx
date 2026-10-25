import { durationMs, timestampDate } from "@bufbuild/protobuf/wkt";
import { useQuery } from "@ddash/plugin";
import { CoreService, type AuditChain, type AuditEntry } from "@ddash/proto/ddash/core/core_pb";
import { Button, CodeBlock, cx, DataTable, EmptyState, Facts, Notice, PageTitle, Panel, Pill, ProtoSheet, Select, StatusList, Tabs, tintOf, useVariant, type Tone } from "@ddash/ui";
import { ChevronRight, ScrollText } from "lucide-react";
import { useEffect, useEffectEvent, useState, type ReactNode } from "react";

/** What the Audit Log is filtered by; empty means any. */
interface Filter {
  actorKind: string;
  target: string;
  outcome: string;
}

const actorKinds: [string, string][] = [
  ["operator", "Operators"],
  ["component", "Components"],
  ["core", "Core"],
  ["", "Anyone"],
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
 * first, rooted in what the actor started and expandable to its hops. It
 * starts on the Operator's own Actions and loads older chains a page at a
 * time. Read at the NONE tier: on mount and after Actions.
 * PROTOTYPE: ?variant=1 a table whose rows open the hops in a Sheet;
 * ?variant=2 a timeline by day, hops unfolding inline.
 */
export function AuditLog() {
  const [filter, setFilter] = useState<Filter>({ actorKind: "operator", target: "", outcome: "" });
  const [targets, setTargets] = useState<string[]>([]);
  const set = (key: keyof Filter) => (value: string) => setFilter((f) => ({ ...f, [key]: value }));
  return (
    <div>
      <PageTitle kicker="Settings" title="Audit Log" subtitle="Every Action ddash took, kept for a year." back={{ href: "/settings", label: "Settings" }} />
      {/* A new filter starts again from the newest page. */}
      <Pages
        key={JSON.stringify(filter)}
        filter={filter}
        onTargets={setTargets}
        toolbar={
          <div className="flex flex-col gap-3 border-b border-border p-5 lg:flex-row lg:items-center">
            <div className="overflow-x-auto">
              <Tabs label="Actor" value={filter.actorKind} onChange={set("actorKind")} options={actorKinds.map(([id, label]) => ({ id, label }))} />
            </div>
            <div className="grid grid-cols-2 gap-3 lg:ml-auto lg:flex">
              <Select label="Target" value={filter.target} onChange={(e) => set("target")(e.target.value)}>
                {[["", "Any target"] as [string, string], ...withSelected(targets, filter.target).map((t): [string, string] => [t, t])].map(([v, text]) => (
                  <option key={v} value={v}>
                    {text}
                  </option>
                ))}
              </Select>
              <Select label="Outcome" value={filter.outcome} onChange={(e) => set("outcome")(e.target.value)}>
                {outcomes.map(([v, text]) => (
                  <option key={v} value={v}>
                    {text}
                  </option>
                ))}
              </Select>
            </div>
          </div>
        }
      />
    </div>
  );
}

/** targets, keeping the selected one listed while a reload is on its way. */
const withSelected = (targets: string[], selected: string) =>
  selected === "" || targets.includes(selected) ? targets : [...targets, selected].sort();

/** The pages loaded so far: each starts at its token, the first at "". */
function Pages({ filter, onTargets, toolbar }: { filter: Filter; onTargets: (targets: string[]) => void; toolbar: ReactNode }) {
  const v = useVariant();
  const [pages, setPages] = useState<{ token: string; next?: string; chains?: AuditChain[]; error?: string }[]>([{ token: "" }]);
  const [shown, setShown] = useState<AuditChain>();
  const settled = (i: number, next: string, chains: AuditChain[], error?: string) =>
    setPages((ps) => (ps[i] && (ps[i].chains !== chains || ps[i].error !== error) ? ps.map((p, j) => (j === i ? { ...p, next, chains, error } : p)) : ps));
  const last = pages[pages.length - 1];
  const chains = withSampleHops(pages.flatMap((p) => p.chains ?? []));
  const loading = pages.some((p) => !p.chains && !p.error);
  const error = pages.find((p) => p.error)?.error;
  const older = last.next && (
    <div className="flex justify-center border-t border-border p-4">
      <Button variant="outline" onClick={() => setPages((ps) => [...ps, { token: last.next! }])}>
        Load older Actions
      </Button>
    </div>
  );
  const empty = !loading && chains.length === 0 && !error && (
    <EmptyState icon={ScrollText} title="No Actions match">
      Try Anyone, or another target or outcome.
    </EmptyState>
  );
  return (
    <>
      {pages.map((p, i) => (
        <Page key={p.token} request={{ ...filter, pageToken: p.token }} onSettled={(next, cs, err) => settled(i, next, cs, err)} onTargets={i === 0 ? onTargets : undefined} />
      ))}
      <Panel flush>
        {toolbar}
        {error && (
          <div className="p-5">
            <Notice tone="down" title="The Audit Log can't be read">
              {error}
            </Notice>
          </div>
        )}
        {v === "2" ? (
          <div className="p-5 sm:p-6">
            <Timeline chains={chains} />
            {empty}
          </div>
        ) : (
          <DataTable
            label="Chains"
            rows={chains}
            rowKey={(c) => c.id}
            onRowClick={setShown}
            empty={empty}
            columns={[
              { id: "action", header: "Action", rowHeader: true, cell: (c) => <ActionName entry={c.entries[0]} /> },
              { id: "time", header: "When", sortValue: (c) => timeOf(c.entries[0])?.getTime() ?? 0, cell: (c) => <When entry={c.entries[0]} /> },
              { id: "actor", header: "Actor", hideBelow: "sm", cell: (c) => <span className="text-muted-foreground">{actorOf(c.entries[0])}</span> },
              { id: "outcome", header: "Outcome", cell: (c) => <ChainOutcome chain={c} /> },
              { id: "hops", header: "Hops", align: "end", hideBelow: "sm", cell: (c) => c.entries.length },
              { id: "duration", header: "Took", align: "end", hideBelow: "sm", cell: (c) => (c.entries[0]?.duration ? formatDuration(durationMs(c.entries[0].duration)) : "—") },
            ]}
          />
        )}
        {loading && <div className="p-5"><Skeleton /></div>}
        {older}
      </Panel>
      {shown && <ChainSheet chain={shown} onClose={() => setShown(undefined)} />}
    </>
  );
}

/** PROTOTYPE: ?samplehops gives the first chains made-up hops, since `make dev` records few. */
function withSampleHops(chains: AuditChain[]): AuditChain[] {
  if (!new URLSearchParams(window.location.search).has("samplehops")) return chains;
  return chains.map((c, i) => {
    if (i > 2 || c.entries.length > 1 || !c.entries[0]) return c;
    const root = c.entries[0];
    const hop = (target: string, method: string, outcome: string, message = "", request = "{}") => ({ ...root, actorKind: "component", username: root.target, target, method, outcome, message, request });
    return {
      ...c,
      entries: [
        { ...root, outcome: i === 1 ? "failed" : root.outcome, message: i === 1 ? "1 of 2 hops failed" : "" },
        hop("proxmox-api", "Shutdown", "ok", "", '{\n  "node": "pve",\n  "vmid": 210,\n  "kind": "LXC"\n}'),
        hop("checks", "StartMaintenance", i === 1 ? "failed" : "ok", i === 1 ? "checks is down: connection refused" : "", '{\n  "checks": ["nginx", "nginx-proxy-manager"],\n  "until": "2026-10-25T19:30:00Z"\n}'),
      ],
    } as AuditChain;
  });
}

function Skeleton() {
  return (
    <div className="flex animate-pulse flex-col gap-3">
      {[0, 1, 2].map((i) => (
        <div key={i} className="h-4 rounded bg-muted" style={{ width: `${90 - i * 15}%` }} />
      ))}
    </div>
  );
}

/** One page of chains, reported up. */
function Page({
  request,
  onSettled,
  onTargets,
}: {
  request: Filter & { pageToken: string };
  onSettled: (next: string, chains: AuditChain[], error?: string) => void;
  onTargets?: (targets: string[]) => void;
}) {
  const { data, error } = useQuery(CoreService.method.listAuditLog, request);
  // The callbacks change every render; only new data is news.
  const report = useEffectEvent((d: NonNullable<typeof data>) => {
    onSettled(d.nextPageToken, d.chains);
    onTargets?.(d.targets);
  });
  const fail = useEffectEvent((m: string) => onSettled("", [], m));
  useEffect(() => {
    if (data) report(data);
  }, [data]);
  useEffect(() => {
    if (error && !data) fail(error.message);
  }, [error, data]);
  return null;
}

const timeOf = (e?: AuditEntry) => (e?.time ? timestampDate(e.time) : undefined);
const actorOf = (e?: AuditEntry) => (e ? (e.username ? `${e.actorKind}:${e.username}` : e.actorKind) : "");
const toneOfOutcome = (o: string): Tone => (o === "ok" ? "up" : o === "interrupted" ? "warn" : o === "" ? "idle" : "down");

function When({ entry: e }: { entry?: AuditEntry }) {
  const at = timeOf(e);
  return at ? (
    <time dateTime={at.toISOString()} className="text-xs text-muted-foreground tabular-nums">
      {at.toLocaleString(undefined, { month: "short", day: "numeric", hour: "2-digit", minute: "2-digit", second: "2-digit" })}
    </time>
  ) : null;
}

function ActionName({ entry: e }: { entry?: AuditEntry }) {
  if (!e) return null;
  return (
    <span className="inline-flex items-center gap-2">
      <span className="font-bold">{e.target}</span>
      <span className="font-mono text-xs font-normal text-muted-foreground">{e.method}</span>
      {e.disruptive && <Pill tone="warn">disruptive</Pill>}
    </span>
  );
}

function ChainOutcome({ chain }: { chain: AuditChain }) {
  const [root, ...rest] = chain.entries;
  const notOk = rest.filter((e) => e.outcome !== "ok").length;
  return (
    <span className="inline-flex items-center gap-2">
      <Outcome outcome={root?.outcome ?? ""} />
      {notOk > 0 && <span className="text-xs font-bold text-down">{notOk} {notOk === 1 ? "hop" : "hops"} not ok</span>}
    </span>
  );
}

/** Variant 1: a chain's hops, in a Sheet. */
function ChainSheet({ chain, onClose }: { chain: AuditChain; onClose: () => void }) {
  const root = chain.entries[0]!;
  return (
    <ProtoSheet wide kicker="Audit Log · Action" title={`${root.target} · ${root.method}`} subtitle={timeOf(root)?.toLocaleString()} onClose={onClose}>
      <div className="flex flex-col gap-6">
        {root.message && <Notice tone={toneOfOutcome(root.outcome) === "warn" ? "warn" : "down"}>{root.message}</Notice>}
        <Facts
          items={[
            { label: "Actor", value: actorOf(root) },
            { label: "Outcome", value: <ChainOutcome chain={chain} /> },
            { label: "Took", value: root.duration ? formatDuration(durationMs(root.duration)) : "still running" },
            { label: "Hops", value: chain.entries.length },
          ]}
        />
        <div>
          <div className="mb-3 text-[10px] font-extrabold tracking-[0.14em] text-muted-foreground uppercase">Hops</div>
          <Hops chain={chain} />
        </div>
      </div>
    </ProtoSheet>
  );
}

function Hops({ chain }: { chain: AuditChain }) {
  return (
    <StatusList
      timeline
      items={chain.entries.map((e) => ({
        tone: toneOfOutcome(e.outcome),
        title: (
          <span className="inline-flex items-center gap-2">
            {e.target} <span className="font-mono text-xs font-normal text-muted-foreground">{e.method}</span>
          </span>
        ),
        meta: [actorOf(e), e.message].filter(Boolean).join(" · "),
        aside: (
          <>
            <Outcome outcome={e.outcome} />
            {e.duration && formatDuration(durationMs(e.duration))}
          </>
        ),
        body: e.request && <CodeBlock label="Request">{e.request}</CodeBlock>,
      }))}
    />
  );
}

/** Variant 2: chains by day, hops unfolding in place. */
function Timeline({ chains }: { chains: readonly AuditChain[] }) {
  const days = new Map<string, AuditChain[]>();
  for (const c of chains) {
    const d = timeOf(c.entries[0])?.toLocaleDateString(undefined, { weekday: "long", month: "long", day: "numeric" }) ?? "";
    days.set(d, [...(days.get(d) ?? []), c]);
  }
  return (
    <div className="flex flex-col gap-8">
      {[...days].map(([day, cs]) => (
        <section key={day} aria-label={day}>
          <div className="mb-4 flex items-center gap-3 text-[10px] font-extrabold tracking-[0.14em] text-primary uppercase">
            {day}
            <span className="h-px flex-1 bg-border" />
            <span className="text-muted-foreground">{cs.length} Actions</span>
          </div>
          <ol className="relative flex flex-col gap-1 before:absolute before:top-4 before:bottom-4 before:left-[0.875rem] before:w-px before:bg-border">
            {cs.map((c) => (
              <ChainItem key={c.id} chain={c} />
            ))}
          </ol>
        </section>
      ))}
    </div>
  );
}

function ChainItem({ chain }: { chain: AuditChain }) {
  const [open, setOpen] = useState(false);
  const root = chain.entries[0];
  if (!root) return null;
  const tone = toneOfOutcome(root.outcome);
  return (
    <li className="relative">
      <button type="button" aria-expanded={open} onClick={() => setOpen((o) => !o)} className="flex w-full cursor-pointer items-start gap-3.5 rounded-lg py-2 pr-2 text-left hover:bg-primary/5">
        <span className={cx("relative z-1 grid h-7 w-7 shrink-0 place-items-center rounded-full border-3 border-card outline-1 outline-border", tintOf[tone])}>
          <span className="h-1.5 w-1.5 rounded-full bg-current" />
        </span>
        <span className="min-w-0 flex-1 pt-0.5">
          <span className="flex flex-wrap items-center gap-x-2.5 gap-y-1">
            <ActionName entry={root} />
            <ChainOutcome chain={chain} />
          </span>
          <span className="mt-0.5 block text-xs text-muted-foreground">
            {actorOf(root)}
            {chain.entries.length > 1 && ` · ${chain.entries.length} hops`}
            {root.duration && ` · ${formatDuration(durationMs(root.duration))}`}
          </span>
        </span>
        <span className="flex shrink-0 items-center gap-2 pt-1 text-xs font-bold text-muted-foreground tabular-nums">
          {timeOf(root)?.toLocaleTimeString(undefined, { hour: "2-digit", minute: "2-digit" })}
          <ChevronRight aria-hidden className={cx("h-4 w-4 transition-transform", open && "rotate-90")} />
        </span>
      </button>
      {open && (
        <div className="mt-2 mb-4 ml-10 rounded-ui border border-border bg-background/60 p-4">
          <Hops chain={chain} />
        </div>
      )}
    </li>
  );
}

function Outcome({ outcome }: { outcome: string }): ReactNode {
  const tone = toneOfOutcome(outcome);
  return (
    <Pill tone={tone} dot>
      {outcome === "" ? "running" : outcome}
    </Pill>
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
