import { durationMs, timestampDate } from "@bufbuild/protobuf/wkt";
import { useQuery } from "@ddash/plugin";
import { CoreService, type AuditChain, type AuditEntry } from "@ddash/proto/ddash/core/core_pb";
import { Badge, cx, Icon, PageHeader, type Tone } from "@ddash/ui";
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
 */
export function AuditLog() {
  const [filter, setFilter] = useState<Filter>({ actorKind: "operator", target: "", outcome: "" });
  const [targets, setTargets] = useState<string[]>([]);
  const set = (key: keyof Filter) => (value: string) => setFilter((f) => ({ ...f, [key]: value }));
  return (
    <div>
      <PageHeader title="Audit Log" subtitle="Every Action ddash took, kept for a year." />
      <div className="mb-4 flex flex-wrap gap-3">
        <Select label="Actor" value={filter.actorKind} onChange={set("actorKind")} options={actorKinds} />
        <Select
          label="Target"
          value={filter.target}
          onChange={set("target")}
          options={[["", "Any target"], ...withSelected(targets, filter.target).map((t): [string, string] => [t, t])]}
        />
        <Select label="Outcome" value={filter.outcome} onChange={set("outcome")} options={outcomes} />
      </div>
      {/* A new filter starts again from the newest page. */}
      <Pages key={JSON.stringify(filter)} filter={filter} onTargets={setTargets} />
    </div>
  );
}

/** targets, keeping the selected one listed while a reload is on its way. */
const withSelected = (targets: string[], selected: string) =>
  selected === "" || targets.includes(selected) ? targets : [...targets, selected].sort();

function Select({ label, value, onChange, options }: { label: string; value: string; onChange: (v: string) => void; options: [string, string][] }) {
  return (
    <label className="flex items-center gap-2 text-sm">
      <span className="text-muted-foreground">{label}</span>
      <select
        aria-label={label}
        value={value}
        onChange={(e) => onChange(e.target.value)}
        className="rounded-ui border border-border bg-card px-2 py-1 text-foreground"
      >
        {options.map(([v, text]) => (
          <option key={v} value={v}>
            {text}
          </option>
        ))}
      </select>
    </label>
  );
}

/** The pages loaded so far: each starts at its token, the first at "". */
function Pages({ filter, onTargets }: { filter: Filter; onTargets: (targets: string[]) => void }) {
  const [pages, setPages] = useState<{ token: string; next?: string; empty?: boolean }[]>([{ token: "" }]);
  const settled = (i: number, next: string, empty: boolean) =>
    setPages((ps) => (ps[i] && (ps[i].next !== next || ps[i].empty !== empty) ? ps.map((p, j) => (j === i ? { ...p, next, empty } : p)) : ps));
  const last = pages[pages.length - 1];
  return (
    <div>
      <ul aria-label="Chains" className="divide-y divide-border rounded-ui border border-border bg-card">
        {pages.map((p, i) => (
          <Page
            key={p.token}
            request={{ ...filter, pageToken: p.token }}
            onSettled={(next, empty) => settled(i, next, empty)}
            onTargets={i === 0 ? onTargets : undefined}
          />
        ))}
      </ul>
      {pages.length === 1 && pages[0].empty && <p className="mt-3 text-sm text-muted-foreground">No Actions match.</p>}
      {last.next && (
        <button
          type="button"
          onClick={() => setPages((ps) => [...ps, { token: last.next! }])}
          className="mt-3 rounded-ui border border-border px-3 py-1.5 text-sm hover:bg-muted"
        >
          Older
        </button>
      )}
    </div>
  );
}

/** One page of chains, as list items. */
function Page({
  request,
  onSettled,
  onTargets,
}: {
  request: Filter & { pageToken: string };
  onSettled: (next: string, empty: boolean) => void;
  onTargets?: (targets: string[]) => void;
}) {
  const { data, error } = useQuery(CoreService.method.listAuditLog, request);
  // The callbacks change every render; only new data is news.
  const report = useEffectEvent((d: NonNullable<typeof data>) => {
    onSettled(d.nextPageToken, d.chains.length === 0);
    onTargets?.(d.targets);
  });
  useEffect(() => {
    if (data) report(data);
  }, [data]);
  if (error && !data) {
    return (
      <li role="alert" className="px-4 py-3 text-sm text-down-text">
        The Audit Log can't be read: {error.message}
      </li>
    );
  }
  return data?.chains.map((c) => <Chain key={c.id} chain={c} />);
}

/** A chain: its root, which expands to every hop. */
function Chain({ chain }: { chain: AuditChain }) {
  const [open, setOpen] = useState(false);
  const [root, ...rest] = chain.entries;
  if (!root) return null;
  const notOk = rest.filter((e) => e.outcome !== "ok").length;
  return (
    <li>
      <button
        type="button"
        aria-expanded={open}
        onClick={() => setOpen((o) => !o)}
        className="flex w-full flex-wrap items-center gap-x-3 gap-y-1 px-4 py-3 text-left text-sm hover:bg-muted"
      >
        <span className={cx("flex shrink-0 text-muted-foreground transition-transform", open && "rotate-90")}>
          <Icon name="chevron-right" />
        </span>
        <EntryLine entry={root} />
        {rest.length > 0 && (
          <span className="text-muted-foreground">
            {chain.entries.length} hops
            {notOk > 0 && <> · {notOk} not ok</>}
          </span>
        )}
      </button>
      {open && (
        <ol aria-label="Hops" className="mx-4 mb-3 flex flex-col gap-2 border-l-2 border-border pl-4">
          {chain.entries.map((e, i) => (
            <li key={i} className="text-sm">
              <div className="flex flex-wrap items-center gap-x-3 gap-y-1">
                <EntryLine entry={e} />
              </div>
              <pre className="mt-1 overflow-x-auto rounded-ui bg-muted px-3 py-2 text-xs whitespace-pre-wrap break-all">{e.request}</pre>
            </li>
          ))}
        </ol>
      )}
    </li>
  );
}

/** An entry's time, actor, target and method, disruptive, outcome (with its message) and duration. */
function EntryLine({ entry: e }: { entry: AuditEntry }) {
  const at = e.time ? timestampDate(e.time) : undefined;
  return (
    <>
      {at && (
        <time dateTime={at.toISOString()} className="tabular-nums text-muted-foreground">
          {at.toLocaleString()}
        </time>
      )}
      <span>{e.username ? `${e.actorKind}:${e.username}` : e.actorKind}</span>
      <span className="font-medium">
        {e.target} · {e.method}
      </span>
      {e.disruptive && <Badge tone="warn">disruptive</Badge>}
      <Outcome outcome={e.outcome} />
      {e.message && <span className="text-down-text">{e.message}</span>}
      {e.duration && <span className="tabular-nums text-muted-foreground">{formatDuration(durationMs(e.duration))}</span>}
    </>
  );
}

function Outcome({ outcome }: { outcome: string }): ReactNode {
  const [tone, label]: [Tone, string] =
    outcome === "ok" ? ["up", "ok"] : outcome === "interrupted" ? ["warn", "interrupted"] : outcome === "" ? ["idle", "running"] : ["down", outcome];
  return <Badge tone={tone}>{label}</Badge>;
}

/** 3 ms, 1.5 s, 12 s, 2 min 5 s. */
export function formatDuration(ms: number): string {
  if (ms < 1000) return `${Math.round(ms)} ms`;
  if (ms < 10_000) return `${(ms / 1000).toFixed(1)} s`;
  if (ms < 60_000) return `${Math.round(ms / 1000)} s`;
  const s = Math.round(ms / 1000);
  return `${Math.floor(s / 60)} min ${s % 60} s`;
}
