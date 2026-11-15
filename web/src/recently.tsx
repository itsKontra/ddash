import { durationMs, timestampMs } from "@bufbuild/protobuf/wkt";
import { Refresh, useQuery } from "@ddash/plugin";
import { CoreService, type AuditChain, type AuditEntry } from "@ddash/proto/ddash/core/core_pb";
import { cx, Icon, Panel, Skeleton, type IconName, type Tone } from "@ddash/ui";
import { useEffect, useState } from "react";
import { Link } from "react-router";
import { formatDuration } from "./audit-log";

/** How an Action ended, or that it still runs, told apart by icon and tone as well as by word. */
export type Outcome = "running" | "done" | "failed" | "interrupted" | "partly";

/** An Action as Recently lists it. */
export interface Recent {
  id: string;
  /** What the actor started, e.g. "Run update all". */
  what: string;
  /** What it was done to, from its request, e.g. "restart paperless on media"; empty when the request names nothing. */
  subject: string;
  /** Where it ran: a Component's name, or "core". */
  target: string;
  outcome: Outcome;
  /** The outcome in a few words: "done in 12 s", "running for 8 s · Create snapshot", the error. */
  detail: string;
  /** The Operator's username, the Component's name, or "ddash". */
  who: string;
  /** When it started, in ms. */
  at: number;
}

/** "RunUpdateAll" as "Run update all", "AddSSHKey" as "Add SSH key". */
export function words(method: string): string {
  const parts = method.match(/[A-Z]+(?![a-z])|[A-Z]?[a-z0-9]+/g) ?? [method];
  return parts.map((p, i) => (i === 0 || (p.length > 1 && p === p.toUpperCase()) ? p : p.toLowerCase())).join(" ");
}

// The request fields that name what an Action was done to, the host it ran
// on and how long it lasts. Any Plugin's request is read by the same rule:
// the core knows no Plugin's methods.
// A Plan's step titles and labels come after these, so they aren't names.
const names = new Set(["name", "project", "plugin", "component", "app", "hostname", "device", "peer", "storage"]);
const ids = new Set(["vmid", "id", "mac", "address"]);
const hosts = new Set(["host", "node"]);

/** "600s" as "10 min", "3600s" as "1 h". */
function lasting(protoDuration: string): string {
  const s = Number(protoDuration.replace(/s$/, ""));
  if (!Number.isFinite(s) || s <= 0) return "";
  if (s % 3600 === 0) return `${s / 3600} h`;
  if (s % 60 === 0) return `${s / 60} min`;
  return formatDuration(s * 1000);
}

/** The most of subject a row shows. */
const subjectMax = 60;

/**
 * What an Action was done to, read from its request as the Audit Log keeps
 * it (compact protojson, perhaps cut short): the case of an "action" oneof
 * or enum, the first name (or else id), the host it ran on, and a duration.
 * {"action":{"restart":{"target":{"host":"media","project":"paperless"}}}}
 * reads "restart paperless on media", {"duration":"600s"} "10 min". Never
 * the raw request: empty when it names nothing.
 */
export function subject(request: string): string {
  const verb =
    /"action":\s*\{"(\w+)"/.exec(request)?.[1] ?? /"action":\s*"(?:[A-Z]+_)*([A-Z]+)"/.exec(request)?.[1]?.toLowerCase() ?? "";
  // Every string and number field, in the order the request has them.
  const fields: [string, string][] = [];
  for (const m of request.matchAll(/"(\w+)":\s*(?:"((?:[^"\\]|\\.)*)"|(-?\d+(?:\.\d+)?)(?=[,}\]]))/g)) {
    const value = m[2] ?? m[3];
    if (value && value !== "REDACTED") fields.push([m[1], value]);
  }
  const first = (keys: Set<string>) => fields.find(([k]) => keys.has(k))?.[1];
  const what = first(names) ?? first(ids);
  const host = first(hosts);
  const duration = fields.find(([k]) => k === "duration")?.[1];
  const text = [
    verb && words(verb).toLowerCase(),
    what,
    what && host && host !== what ? `on ${host}` : "",
    duration ? lasting(duration) : "",
  ]
    .filter(Boolean)
    .join(" ");
  return text.length > subjectMax ? `${text.slice(0, subjectMax - 1)}…` : text;
}

/**
 * Whether Recently lists a chain: what an Operator or ddash itself did to
 * the lab, not housekeeping. A Component's sync with another (e.g. apps
 * setting checks' App Checks) and the shell saving the Overview layout are
 * left to the Audit Log.
 */
export function listed(chain: AuditChain): boolean {
  const root = chain.entries[0];
  if (!root) return false;
  if (root.actorKind === "component") return false;
  return !(root.target === "core" && root.method === "SaveLayout");
}

/** How many chains Recently reads to find its four. */
export const read = 20;

const ended = (e: AuditEntry) => (e.time ? timestampMs(e.time) : 0) + (e.duration ? durationMs(e.duration) : 0);
const failed = (e: AuditEntry) => e.outcome !== "" && e.outcome !== "ok" && e.outcome !== "interrupted";
/** "PermissionDenied" as "permission denied". */
const code = (outcome: string) => words(outcome).toLowerCase();

/**
 * A chain as Recently lists it, from its root (what the actor started) and
 * its hops: running while any hop runs, failed when the root failed or a
 * job it started did (its CollectJob hop, ADR-0006), partly when it ended
 * well but another hop failed, interrupted when ddash stopped mid-way.
 */
export function recent(chain: AuditChain, now: number): Recent | undefined {
  const [root, ...hops] = chain.entries;
  if (!root) return undefined;
  const at = root.time ? timestampMs(root.time) : now;
  const base = {
    id: chain.id,
    what: words(root.method),
    subject: subject(root.request),
    target: root.target,
    who: root.actorKind === "core" ? "ddash" : root.username || root.actorKind,
    at,
  };
  const running = chain.entries.findLast((e) => e.outcome === "");
  if (running) {
    const step = running === root ? "" : ` · ${words(running.method)}`;
    return { ...base, outcome: "running", detail: `running for ${formatDuration(Math.max(0, now - at))}${step}` };
  }
  const why = (e: AuditEntry) => e.message || code(e.outcome);
  const job = hops.find((e) => e.method === "CollectJob" && failed(e));
  if (failed(root) || job) return { ...base, outcome: "failed", detail: why(failed(root) ? root : job!) };
  const interrupted = chain.entries.find((e) => e.outcome === "interrupted");
  if (interrupted) return { ...base, outcome: "interrupted", detail: interrupted.message || "ddash stopped before it ended" };
  const took = formatDuration(Math.max(...chain.entries.map(ended)) - at);
  const notOk = hops.filter(failed);
  if (notOk.length) {
    return { ...base, outcome: "partly", detail: `done in ${took}, but ${words(notOk[0].method)} on ${notOk[0].target} failed: ${why(notOk[0])}` };
  }
  return { ...base, outcome: "done", detail: `done in ${took}` };
}

const looks: Record<Outcome, { icon: IconName; tone: Tone }> = {
  running: { icon: "loader-circle", tone: "info" },
  done: { icon: "circle-check", tone: "up" },
  failed: { icon: "circle-x", tone: "down" },
  interrupted: { icon: "triangle-exclamation-point", tone: "warn" },
  partly: { icon: "triangle-exclamation-point", tone: "warn" },
};

/** How long ago at was: "just now", "4 min ago", "2 h ago", "3 d ago". */
export function ago(at: number, now: number): string {
  const mins = Math.floor((now - at) / 60_000);
  if (mins < 1) return "just now";
  if (mins < 60) return `${mins} min ago`;
  const hours = Math.floor(mins / 60);
  if (hours < 48) return `${hours} h ago`;
  return `${Math.floor(hours / 24)} d ago`;
}

/** How many Actions Recently lists. */
export const shown = 4;

/** Now, in ms, re-read every few seconds, for "running for 12 s" and "4 min ago". */
function useNow(): number {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    const id = setInterval(() => setNow(Date.now()), 5_000);
    return () => clearInterval(id);
  }, []);
  return now;
}

/**
 * The Overview's last Actions an Operator or ddash took, from the Audit
 * Log: what, to what, how it ended (or how far it got), who and when. It
 * reads the last 20 chains and lists the first four that pass listed. Read every 5 s while on screen,
 * and at once after an Action from this tab, so one started anywhere shows
 * without a reload.
 */
export function Recently() {
  const { data, error } = useQuery(CoreService.method.listAuditLog, { pageSize: read }, { refresh: Refresh.LIVE });
  const now = useNow();
  const items = (data?.chains ?? [])
    .filter(listed)
    .slice(0, shown)
    .flatMap((c) => recent(c, now) ?? []);
  return (
    <Panel
      title="Recently"
      actions={
        <Link to="/settings/audit-log?actor=anyone" className="inline-flex items-center gap-0.5 text-xs font-semibold text-muted-foreground hover:text-foreground">
          Audit Log
          <Icon name="chevron-right" size="sm" />
        </Link>
      }
    >
      {!data && error ? (
        <p className="text-sm text-muted-foreground">The Audit Log can&apos;t be read: {error.rawMessage}</p>
      ) : !data ? (
        <Skeleton lines={shown} />
      ) : items.length === 0 ? (
        <p className="text-sm text-muted-foreground">No Actions yet. Every Action ddash takes shows here and in the Audit Log.</p>
      ) : (
        <ul aria-label="Recent Actions" className="-my-2 divide-y divide-border">
          {items.map((r) => (
            <Row key={r.id} item={r} now={now} />
          ))}
        </ul>
      )}
    </Panel>
  );
}

function Row({ item: r, now }: { item: Recent; now: number }) {
  const { icon, tone } = looks[r.outcome];
  return (
    <li aria-label={`${r.what}: ${r.outcome}`} className="flex items-center gap-3 py-2.5">
      {r.outcome === "running" ? (
        <span role="img" aria-label="running" className="inline-grid size-7 shrink-0 place-items-center rounded-ui-sm bg-info-tint text-info-text">
          <span className="inline-grid motion-safe:animate-spin">
            <Icon name={icon} size="sm" />
          </span>
        </span>
      ) : (
        <Icon name={icon} tone={tone} size="sm" label={r.outcome} />
      )}
      <div className="min-w-0 flex-1 sm:flex sm:items-baseline sm:gap-3">
        <div className="truncate text-sm font-semibold sm:max-w-[55%] sm:shrink-0" title={`${r.target} · ${r.what}${r.subject ? ` · ${r.subject}` : ""}`}>
          {r.what}
          {r.subject ? <span className="font-normal"> · {r.subject}</span> : <span className="text-xs font-normal text-muted-foreground"> {r.target}</span>}
        </div>
        <div className={cx("truncate text-xs", r.outcome === "failed" ? "text-down-text" : "text-muted-foreground")} title={r.detail}>
          {r.detail}
        </div>
      </div>
      <div className="shrink-0 text-xs whitespace-nowrap text-muted-foreground tabular-nums">
        {r.who} · <time dateTime={new Date(r.at).toISOString()}>{ago(r.at, now)}</time>
      </div>
    </li>
  );
}
