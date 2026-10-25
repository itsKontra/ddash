import { timestampMs } from "@bufbuild/protobuf/wkt";
import { useAction } from "@ddash/plugin";
import { CheckType, ChecksService, RecordType, State, type Check } from "@ddash/proto/checks/checks_pb";
import {
  Button,
  cx,
  Icon,
  Menu,
  MetricTile,
  PageTitle,
  Panel,
  Pill,
  SearchField,
  Segmented,
  SelectField,
  Sheet,
  Tabs,
  Timeline,
  UptimeBars,
  useVariant,
  type MenuItem,
} from "@ddash/ui";
import { useId, useMemo, useState, type FormEvent } from "react";
import { useParams } from "react-router";
import { latency, shownName, stateLabel, tally, toneOf, useChecks } from "./checks";
import { CheckDetail } from "./detail";
import { bars, percent, useHistory, when, windowNames, type WindowName } from "./history";
import { MaintenanceBadge, MaintenanceList, MaintenanceSheet } from "./maintenance";

// PROTOTYPE (wayfinder: "What should the shell, Overview and a Plugin Page
// look like in the new language?"): the Checks Page in three structures.

type Filter = "all" | "down" | "attention" | "maint" | "muted";
type TypeFilter = "any" | "HTTP" | "TCP" | "ICMP" | "DNS";

const typeLabels: Record<CheckType, string> = {
  [CheckType.UNSPECIFIED]: "",
  [CheckType.TCP]: "TCP",
  [CheckType.ICMP]: "ICMP",
  [CheckType.DNS]: "DNS",
  [CheckType.HTTP]: "HTTP",
};

/** What a Check probes, e.g. "nas.home.example:22" or "nas.home.example A @ 198.18.1.2". */
function target(c: Check): string {
  const s = c.spec;
  if (s?.type === CheckType.DNS && s.dns) return `${s.dns.name} ${RecordType[s.dns.recordType || RecordType.A]} @ ${s.target}`;
  return s?.target ?? "";
}

const matches = (c: Check, f: Filter) =>
  f === "all" ||
  (f === "down" && toneOf(c) === "down") ||
  (f === "attention" && (c.flapping || c.certExpiring || toneOf(c) === "warn")) ||
  (f === "maint" && !!c.maintenance) ||
  (f === "muted" && c.muted);

function median(xs: number[]) {
  if (!xs.length) return 0;
  const s = [...xs].sort((a, b) => a - b);
  return s[Math.floor(s.length / 2)];
}

/** The checks Page: every Check with its state and latency, and their editing. */
export function ChecksPage() {
  const v = useVariant() as string;
  const { data, error } = useChecks();
  const [editing, setEditing] = useState<Check | null | undefined>(undefined);
  const [maintaining, setMaintaining] = useState<Check | null | undefined>(undefined);
  const { id } = useParams();
  const [shown, setShown] = useState<bigint | undefined>(() => (id && /^\d+$/.test(id) ? BigInt(id) : undefined));
  const [filter, setFilter] = useState<Filter>("all");
  const [type, setType] = useState<TypeFilter>("any");
  const [query, setQuery] = useState("");
  const checks = data?.checks ?? [];
  const t = tally(checks);
  const visible = useMemo(
    () =>
      checks.filter(
        (c) =>
          matches(c, filter) &&
          (type === "any" || typeLabels[c.spec?.type ?? CheckType.UNSPECIFIED] === type) &&
          (`${shownName(c)} ${target(c)}`.toLowerCase().includes(query.toLowerCase())),
      ),
    [checks, filter, type, query],
  );
  const tabs = [
    { id: "all" as const, label: "All", count: checks.length },
    { id: "down" as const, label: "Down", count: checks.filter((c) => matches(c, "down")).length },
    { id: "attention" as const, label: "Attention", count: checks.filter((c) => matches(c, "attention")).length },
    { id: "maint" as const, label: "Maintenance", count: checks.filter((c) => matches(c, "maint")).length },
    { id: "muted" as const, label: "Muted", count: checks.filter((c) => matches(c, "muted")).length },
  ];
  const med = median(t.up.map((c) => c.latencyMs));
  const detail = checks.find((c) => c.id === shown);
  const actions = (
    <>
      <Button variant="outline" onClick={() => setMaintaining(null)}>
        <Icon name="wrench" /> Start Maintenance
      </Button>
      <Button onClick={() => setEditing(null)}>Add Check</Button>
    </>
  );
  const toolbar = (chips?: boolean) => (
    <div className="flex flex-col gap-3">
      <div className="flex flex-wrap gap-2.5">
        <SearchField label="Search Checks" value={query} onChange={setQuery} placeholder="Search by name or target…" />
        <SelectField
          label="Type"
          value={type}
          onChange={setType}
          options={[
            { id: "any", label: "Any type" },
            { id: "HTTP", label: "HTTP" },
            { id: "TCP", label: "TCP" },
            { id: "ICMP", label: "ICMP" },
            { id: "DNS", label: "DNS" },
          ]}
        />
      </div>
      <Tabs label="Filter" value={filter} options={tabs} onChange={setFilter} chips={chips} />
    </div>
  );
  const sheets = (
    <>
      {editing !== undefined && <CheckForm check={editing ?? undefined} onClose={() => setEditing(undefined)} />}
      {maintaining !== undefined && data && <MaintenanceSheet check={maintaining ?? undefined} checks={data.checks} onClose={() => setMaintaining(undefined)} />}
    </>
  );
  const subtitle = "Every App's HTTP(S) Check plus your TCP, ICMP and DNS ones, probed every minute: down after 3 failures in a row, up again on one success.";
  const empty = data && checks.length === 0 && <p className="text-sm text-muted-foreground">No Checks yet. Add one to watch any address.</p>;
  const failed = error && !data && <p className="text-sm text-down">{error.message}</p>;

  if (v === "B") {
    const selected = detail ?? visible[0];
    return (
      <div>
        <div className="mb-5 flex flex-wrap items-center gap-3">
          <Pill tone={t.down.length ? "down" : t.warn.length ? "warn" : "up"} dot>
            {t.up.length} of {t.enabled.length} up
          </Pill>
          <span className="text-xs text-muted-foreground">median latency {latency(med)}</span>
          <div className="ml-auto flex gap-2">{actions}</div>
        </div>
        {data && <MaintenanceList checks={data.checks} />}
        {failed}
        {empty}
        {checks.length > 0 && (
          <div className="grid gap-4 lg:grid-cols-[minmax(0,0.9fr)_minmax(0,1.1fr)]">
            <Panel flush>
              <div className="border-b border-border p-4">{toolbar()}</div>
              <ul aria-label="Checks" className="max-h-[calc(100vh-18rem)] overflow-y-auto">
                {visible.map((c) => (
                  <li key={String(c.id)}>
                    <button
                      type="button"
                      onClick={() => setShown(c.id)}
                      className={cx(
                        "flex w-full cursor-pointer items-center gap-3 border-b border-border px-4 py-3 text-left last:border-b-0 hover:bg-muted/60",
                        selected?.id === c.id && "bg-primary/8 shadow-[inset_3px_0_var(--primary)]",
                      )}
                    >
                      <StateIcon c={c} />
                      <div className="min-w-0 flex-1">
                        <div className="truncate text-sm font-bold">{shownName(c)}</div>
                        <div className="truncate font-mono text-[11px] text-muted-foreground">
                          {typeLabels[c.spec?.type ?? CheckType.UNSPECIFIED]} · {target(c)}
                        </div>
                      </div>
                      <div className="shrink-0 text-right text-xs font-bold tabular-nums">
                        {toneOf(c) === "up" ? latency(c.latencyMs) : <span className="text-down">{c.reason || stateLabel(c)}</span>}
                      </div>
                    </button>
                  </li>
                ))}
              </ul>
            </Panel>
            <div className="hidden lg:block">{selected && <InlineDetail check={selected} onEdit={() => setEditing(selected)} onMaintain={() => setMaintaining(selected)} />}</div>
          </div>
        )}
        {detail && (
          <div className="lg:hidden">
            <CheckDetail check={detail} onClose={() => setShown(undefined)} />
          </div>
        )}
        {sheets}
      </div>
    );
  }

  if (v === "C") {
    return (
      <div>
        <PageTitle kicker="Monitoring" title={`${t.up.length} of ${t.enabled.length} Checks up.`} subtitle={subtitle} size="xl" actions={actions} />
        {data && <MaintenanceList checks={data.checks} />}
        {failed}
        {empty}
        {checks.length > 0 && (
          <>
            <div className="mb-6">{toolbar(true)}</div>
            <ul aria-label="Checks" className="grid gap-4 sm:grid-cols-2 xl:grid-cols-3">
              {visible.map((c) => (
                <CheckCard key={String(c.id)} check={c} onShow={() => setShown(c.id)} onEdit={() => setEditing(c)} onMaintain={() => setMaintaining(c)} />
              ))}
            </ul>
          </>
        )}
        {sheets}
        {detail && <CheckDetail check={detail} onClose={() => setShown(undefined)} />}
      </div>
    );
  }

  return (
    <div>
      <PageTitle kicker="Monitoring" title="Checks" subtitle={subtitle} actions={actions} />
      <div className="mb-5 grid grid-cols-2 gap-4 xl:grid-cols-4">
        <MetricTile label="Up" value={t.up.length} icon="up" tone="up" desc={`of ${t.enabled.length} enabled`} />
        <MetricTile label="Down" value={t.down.length} icon="down" tone="down" desc="3 failures in a row" />
        <MetricTile label="Maintenance" value={t.maint.length} icon="wrench" tone="maint" desc="not counted toward uptime" />
        <MetricTile label="Median latency" value={t.up.length ? latency(med) : "–"} icon="gauge" tone="up" desc="across up Checks" />
      </div>
      {data && <MaintenanceList checks={data.checks} />}
      {failed}
      {empty}
      {checks.length > 0 && (
        <Panel flush>
          <div className="border-b border-border p-5">{toolbar()}</div>
          <div className="overflow-x-auto">
            <table aria-label="Checks" className="w-full text-sm whitespace-nowrap">
              <thead className="bg-muted/50 text-left text-[11px] font-bold text-muted-foreground">
                <tr className="border-b border-border">
                  <th className="px-5 py-3 font-bold">Check</th>
                  <th className="px-3 py-3 font-bold">State</th>
                  <th className="px-3 py-3 font-bold">Type</th>
                  <th className="px-3 py-3 font-bold">Target</th>
                  <th className="px-3 py-3 font-bold">Latency</th>
                  <th className="px-5 py-3" />
                </tr>
              </thead>
              <tbody>
                {visible.map((c) => (
                  <Row key={String(c.id)} check={c} onEdit={() => setEditing(c)} onShow={() => setShown(c.id)} onMaintain={() => setMaintaining(c)} />
                ))}
              </tbody>
            </table>
            {visible.length === 0 && <p className="p-8 text-center text-sm text-muted-foreground">No Check matches.</p>}
          </div>
        </Panel>
      )}
      {sheets}
      {detail && <CheckDetail check={detail} onClose={() => setShown(undefined)} />}
    </div>
  );
}

function StateIcon({ c }: { c: Check }) {
  const tone = toneOf(c);
  const tint = { up: "bg-up/10 text-up", down: "bg-down/10 text-down", warn: "bg-warn/12 text-warn", maint: "bg-maint/10 text-maint", idle: "bg-muted text-muted-foreground" }[tone];
  return (
    <span role="img" aria-label={stateLabel(c)} className={cx("grid h-8 w-8 shrink-0 place-items-center rounded-lg", tint)}>
      <Icon name={tone === "up" ? "up" : tone === "down" ? "down" : tone === "maint" ? "wrench" : "alert"} />
    </span>
  );
}

function Flags({ c }: { c: Check }) {
  return (
    <>
      {c.muted && <Pill>muted</Pill>}
      {c.flapping && <Pill tone="warn">flapping</Pill>}
      {c.certExpiring && <Pill tone="warn">certificate expiring</Pill>}
      <MaintenanceBadge check={c} />
    </>
  );
}

/** The Actions on one Check, as Menu items. */
function useCheckActions(c: Check, onEdit: () => void, onMaintain: () => void) {
  const mute = useAction(ChecksService.method.setMuted);
  const disable = useAction(ChecksService.method.setDisabled);
  const remove = useAction(ChecksService.method.deleteCheck);
  const failure = mute.error ?? disable.error ?? remove.error;
  const busy = mute.pending || disable.pending || remove.pending;
  const items: MenuItem[] = [
    ...(!c.appId ? [{ label: "Edit", onSelect: onEdit }] : []),
    { label: "Maintenance", onSelect: onMaintain },
    { label: c.muted ? "Unmute" : "Mute", onSelect: () => void mute.run({ id: c.id, muted: !c.muted }) },
    { label: c.disabled ? "Enable" : "Disable", onSelect: () => void disable.run({ id: c.id, disabled: !c.disabled }) },
    ...(!c.appId
      ? [
          {
            label: "Delete",
            danger: true,
            onSelect: () => void remove.run({ id: c.id }, { confirm: { text: `Delete the Check ${shownName(c)}? Its history goes with it.`, action: "Delete" } }),
          },
        ]
      : []),
  ];
  return { items, failure, busy };
}

function Row({ check: c, onEdit, onShow, onMaintain }: { check: Check; onEdit: () => void; onShow: () => void; onMaintain: () => void }) {
  const { items, failure, busy } = useCheckActions(c, onEdit, onMaintain);
  const tone = toneOf(c);
  const up = !c.disabled && !c.maintenance && c.state === State.UP;
  return (
    <tr className="cursor-pointer border-b border-border transition-colors last:border-b-0 hover:bg-primary/5" onClick={onShow}>
      <th scope="row" className="px-5 py-3.5 text-left">
        <span className="flex items-center gap-3">
          <StateIcon c={c} />
          <span className="font-bold">{shownName(c)}</span>
          <Flags c={c} />
        </span>
      </th>
      <td className="px-3 py-3.5">
        <Pill tone={tone} dot>
          {stateLabel(c)}
        </Pill>
      </td>
      <td className="px-3 py-3.5">
        <span className="rounded-md bg-muted px-2 py-0.5 text-[11px] font-bold text-muted-foreground">{typeLabels[c.spec?.type ?? CheckType.UNSPECIFIED]}</span>
      </td>
      <td className="max-w-72 truncate px-3 py-3.5 font-mono text-xs text-muted-foreground">{target(c)}</td>
      <td className="px-3 py-3.5 font-bold tabular-nums">
        {up && c.lastBeat ? latency(c.latencyMs) : !c.disabled && c.reason ? <span className="font-medium text-down">{c.reason}</span> : null}
      </td>
      <td className="px-5 py-3.5 text-right" onClick={(e) => e.stopPropagation()}>
        {failure && (
          <span role="alert" className="mr-2 text-xs text-down">
            {failure.rawMessage}
          </span>
        )}
        <Menu label={`Actions for ${shownName(c)}`} items={items} disabled={busy} />
      </td>
    </tr>
  );
}

function CheckCard({ check: c, onShow, onEdit, onMaintain }: { check: Check; onShow: () => void; onEdit: () => void; onMaintain: () => void }) {
  const { items, failure, busy } = useCheckActions(c, onEdit, onMaintain);
  const tone = toneOf(c);
  return (
    <li className="group flex flex-col rounded-2xl bg-card p-5 ring-1 ring-border transition-shadow hover:shadow-panel">
      <div className="flex items-start gap-3">
        <button type="button" onClick={onShow} className="min-w-0 flex-1 cursor-pointer text-left">
          <div className="truncate text-base font-extrabold tracking-tight group-hover:text-primary">{shownName(c)}</div>
          <div className="mt-0.5 truncate font-mono text-[11px] text-muted-foreground">
            {typeLabels[c.spec?.type ?? CheckType.UNSPECIFIED]} · {target(c)}
          </div>
        </button>
        <Menu label={`Actions for ${shownName(c)}`} items={items} disabled={busy} />
      </div>
      <div className="mt-5 flex items-end gap-3">
        <div className={cx("text-3xl font-extrabold tracking-tight tabular-nums", tone === "down" && "text-down")}>
          {tone === "up" ? latency(c.latencyMs) : tone === "down" ? "down" : stateLabel(c)}
        </div>
        <div className="mb-1 ml-auto flex flex-wrap justify-end gap-1.5">
          <Pill tone={tone} dot>
            {tone === "maint" ? "maintenance" : tone}
          </Pill>
          <Flags c={c} />
        </div>
      </div>
      {(c.reason || failure) && <p className="mt-3 truncate text-xs text-down">{failure?.rawMessage ?? c.reason}</p>}
    </li>
  );
}

/** Variant B's right pane: the selected Check's history, inline. */
function InlineDetail({ check, onEdit, onMaintain }: { check: Check; onEdit: () => void; onMaintain: () => void }) {
  const [name, setName] = useState<WindowName>("24 h");
  const { data } = useHistory(check.id, name);
  const { items } = useCheckActions(check, onEdit, onMaintain);
  const sel = data?.uptime[windowNames.indexOf(name)];
  return (
    <Panel
      kicker={`${typeLabels[check.spec?.type ?? CheckType.UNSPECIFIED]} · ${target(check)}`}
      title={
        <span className="flex items-center gap-2">
          {shownName(check)}
          <Pill tone={toneOf(check)} dot>
            {stateLabel(check)}
          </Pill>
        </span>
      }
      actions={
        <>
          <Segmented label="Window" value={name} options={windowNames} onChange={setName} />
          <Menu label={`Actions for ${shownName(check)}`} items={items} />
        </>
      }
    >
      <div className="grid grid-cols-3 gap-3">
        {windowNames.map((w, i) => (
          <div key={w} className="rounded-xl bg-muted/60 p-3">
            <div className="text-[10px] font-extrabold tracking-widest text-muted-foreground uppercase">uptime {w}</div>
            <div className="mt-1 text-xl font-extrabold tabular-nums">{data ? percent(data.uptime[i]?.ratio) : "…"}</div>
          </div>
        ))}
      </div>
      <div className="mt-5 flex items-baseline gap-6 text-sm">
        <span className="text-muted-foreground">
          avg <b className="text-foreground tabular-nums">{sel?.latency ? latency(sel.latency.avgMs) : "–"}</b>
        </span>
        <span className="text-muted-foreground">
          min <b className="text-foreground tabular-nums">{sel?.latency ? latency(sel.latency.minMs) : "–"}</b>
        </span>
        <span className="text-muted-foreground">
          max <b className="text-foreground tabular-nums">{sel?.latency ? latency(sel.latency.maxMs) : "–"}</b>
        </span>
      </div>
      {data && (
        <>
          <div className="mt-4">
            <UptimeBars label={`Uptime, last ${name}`} bars={bars(data, name)} />
            <div className="mt-1 flex text-[11px] text-muted-foreground">
              <span>{name} ago</span>
              <span className="ml-auto">now</span>
            </div>
          </div>
          <div className="mt-6 mb-3 text-[10px] font-extrabold tracking-[0.12em] text-muted-foreground uppercase">State changes</div>
          {data.changes.length === 0 ? (
            <p className="text-sm text-muted-foreground">No state changes in the last {name}.</p>
          ) : (
            <Timeline
              items={data.changes.slice(0, 8).map((ch) => ({
                tone: ch.state === State.UP ? "up" : ch.state === State.DOWN ? "down" : "warn",
                title: ch.state === State.UP ? "Recovered" : ch.state === State.DOWN ? "Went down" : "Pending",
                meta: ch.reason || (ch.maintenance ? "in Maintenance" : undefined),
                aside: ch.at ? when(timestampMs(ch.at)) : "",
              }))}
            />
          )}
        </>
      )}
    </Panel>
  );
}

type TypeName = "TCP" | "ICMP" | "DNS";
type RecordName = "A" | "AAAA" | "MX" | "NS" | "TXT";

const typeNames: readonly TypeName[] = ["TCP", "ICMP", "DNS"];
const recordNames: readonly RecordName[] = ["A", "AAAA", "MX", "NS", "TXT"];

/** What the target means, by type. */
const targetFields: Record<TypeName, { label: string; placeholder: string }> = {
  TCP: { label: "Host and port", placeholder: "198.18.1.10:22" },
  ICMP: { label: "Host", placeholder: "198.18.1.10" },
  DNS: { label: "DNS server", placeholder: "198.18.1.1" },
};

/** Adds a Check, or edits check. */
function CheckForm({ check, onClose }: { check?: Check; onClose: () => void }) {
  const create = useAction(ChecksService.method.createCheck);
  const update = useAction(ChecksService.method.updateCheck);
  const [name, setName] = useState(check?.spec?.name ?? "");
  const [type, setType] = useState<TypeName>((typeLabels[check?.spec?.type ?? CheckType.TCP] || "TCP") as TypeName);
  const [targetValue, setTarget] = useState(check?.spec?.target ?? "");
  const [dnsName, setDnsName] = useState(check?.spec?.dns?.name ?? "");
  const [record, setRecord] = useState<RecordName>(RecordType[check?.spec?.dns?.recordType || RecordType.A] as RecordName);
  const failure = create.error ?? update.error;

  async function save(e: FormEvent) {
    e.preventDefault();
    const spec = {
      name: name.trim(),
      type: CheckType[type],
      target: targetValue.trim(),
      dns: type === "DNS" ? { name: dnsName.trim(), recordType: RecordType[record] } : undefined,
    };
    const done = check ? await update.run({ id: check.id, spec }) : await create.run({ spec });
    if (done) onClose();
  }

  return (
    <Sheet title={check ? `Edit ${shownName(check)}` : "Add Check"} onClose={onClose}>
      <form className="flex flex-col gap-4" onSubmit={save}>
        <Field label="Name" value={name} onChange={setName} placeholder="Optional; the target is shown otherwise" />
        <div className="flex flex-col gap-1">
          <span className="text-sm font-medium">Type</span>
          <Segmented label="Type" value={type} options={typeNames} onChange={setType} />
        </div>
        <Field label={targetFields[type].label} value={targetValue} onChange={setTarget} placeholder={targetFields[type].placeholder} />
        {type === "DNS" && (
          <>
            <Field label="Name to resolve" value={dnsName} onChange={setDnsName} placeholder="jellyfin.home.example" />
            <div className="flex flex-col gap-1">
              <span className="text-sm font-medium">Record type</span>
              <Segmented label="Record type" value={record} options={recordNames} onChange={setRecord} />
            </div>
          </>
        )}
        <p className="text-xs text-muted-foreground">
          Probed every 60 s with a 10 s timeout. {type === "DNS" ? "Up when the server answers with at least one record." : ""}
        </p>
        {failure && (
          <p role="alert" className="text-sm text-down">
            {failure.rawMessage}
          </p>
        )}
        <div className="flex gap-2">
          <Button type="submit" disabled={create.pending || update.pending}>
            Save
          </Button>
          <Button variant="ghost" onClick={onClose}>
            Cancel
          </Button>
        </div>
      </form>
    </Sheet>
  );
}

function Field({ label, value, onChange, placeholder }: { label: string; value: string; onChange: (v: string) => void; placeholder?: string }) {
  const id = useId();
  return (
    <div className="flex flex-col gap-1">
      <label htmlFor={id} className="text-sm font-medium">
        {label}
      </label>
      <input
        id={id}
        value={value}
        placeholder={placeholder}
        onChange={(e) => onChange(e.target.value)}
        className="rounded-ui border border-border bg-background px-3 py-1.5 text-sm placeholder:text-muted-foreground"
      />
    </div>
  );
}
