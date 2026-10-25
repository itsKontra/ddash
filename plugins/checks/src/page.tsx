import { useAction } from "@ddash/plugin";
import { CheckType, ChecksService, RecordType, State, type Check } from "@ddash/proto/checks/checks_pb";
import {
  Button,
  DataTable,
  EmptyState,
  Field,
  FormActions,
  Icon,
  MetricTile,
  Notice,
  PageTitle,
  Panel,
  Pill,
  SearchField,
  Segmented,
  Select,
  Sheet,
  Tabs,
  TextInput,
  useQueryState,
  type Column,
  type IconName,
  type MenuItem,
  type Tone,
} from "@ddash/ui";
import { useId, useState, type FormEvent } from "react";
import { useParams } from "react-router";
import { latency, shownName, stateLabel, tally, toneOf, useChecks } from "./checks";
import { CheckDetail } from "./detail";
import { MaintenanceList, MaintenancePill, MaintenanceSheet } from "./maintenance";

/** The checks Page: every Check with its state and latency, and their editing. */
export function ChecksPage() {
  const { data, error } = useChecks();
  // undefined: no form; null: adding; a Check: editing it.
  const [editing, setEditing] = useState<Check | null | undefined>(undefined);
  // undefined: no Maintenance sheet; null: for everything or a Workload; a Check: for it.
  const [maintaining, setMaintaining] = useState<Check | null | undefined>(undefined);
  // The Check whose detail is open, by id, so it follows the list's refreshes.
  // A Notification's link (/checks/<id>) opens it.
  const { id } = useParams();
  const [shown, setShown] = useState<bigint | undefined>(() => (id && /^\d+$/.test(id) ? BigInt(id) : undefined));
  const detail = data?.checks.find((c) => c.id === shown);
  const t = data && tally(data.checks);
  const beaten = t?.up.filter(showsLatency).map((c) => c.latencyMs);
  return (
    <div>
      <PageTitle
        kicker="Monitoring"
        title="Checks"
        subtitle="Every App's HTTP(S) Check plus your TCP, ICMP and DNS ones, probed every minute: down after 3 failures in a row, up again on one success."
        actions={
          <>
            <Button variant="outline" icon="wrench" onClick={() => setMaintaining(null)}>
              Start Maintenance
            </Button>
            <Button icon="plus" onClick={() => setEditing(null)}>
              Add Check
            </Button>
          </>
        }
      />
      <div className="flex flex-col gap-5">
        <div className="grid grid-cols-2 gap-4 xl:grid-cols-4">
          <MetricTile label="Up" icon="circle-check" tone="up" value={t?.up.length} desc={t && `of ${t.enabled.length} enabled`} />
          <MetricTile label="Down" icon="circle-x" tone={t?.down.length ? "down" : "idle"} value={t?.down.length} desc="after 3 failures in a row" />
          <MetricTile
            label="Maintenance"
            icon="wrench"
            tone={t?.maint.length ? "maint" : "idle"}
            value={t?.maint.length}
            desc="left out of uptime"
          />
          <MetricTile
            label="Median latency"
            icon="gauge"
            value={beaten && (beaten.length ? latency(median(beaten)) : "–")}
            desc="across up Checks"
          />
        </div>
        {data && <MaintenanceList checks={data.checks} />}
        {error && !data ? (
          <Notice tone="down">{error.message}</Notice>
        ) : data?.checks.length === 0 ? (
          <Panel>
            <EmptyState
              icon="activity"
              title="No Checks yet"
              action={
                <Button icon="plus" onClick={() => setEditing(null)}>
                  Add Check
                </Button>
              }
            >
              Add one to watch any address.
            </EmptyState>
          </Panel>
        ) : (
          <Explorer checks={data?.checks} onEdit={setEditing} onShow={(c) => setShown(c.id)} onMaintain={setMaintaining} />
        )}
      </div>
      {editing !== undefined && <CheckForm check={editing ?? undefined} onClose={() => setEditing(undefined)} />}
      {maintaining !== undefined && data && (
        <MaintenanceSheet check={maintaining ?? undefined} checks={data.checks} onClose={() => setMaintaining(undefined)} />
      )}
      {detail && <CheckDetail check={detail} onClose={() => setShown(undefined)} />}
    </div>
  );
}

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

/** The tinted chip each state's icon sits in, by tone. */
const stateChip: Record<Tone, string> = {
  up: "bg-up-tint text-up-text",
  down: "bg-down-tint text-down-text",
  warn: "bg-warn-tint text-warn-text",
  maint: "bg-maint-tint text-maint-text",
  info: "bg-info-tint text-info-text",
  idle: "bg-muted text-muted-foreground",
};

const stateIcon: Record<Tone, IconName> = {
  up: "circle-check",
  down: "circle-x",
  warn: "circle-alert",
  maint: "wrench",
  info: "info",
  idle: "circle-minus",
};

/** A Check's state at a glance: its tone's icon on its tint, named by the state. */
function StateIcon({ check: c }: { check: Check }) {
  const tone = toneOf(c);
  return (
    <span role="img" aria-label={stateLabel(c)} className={`inline-grid size-7 shrink-0 place-items-center rounded-ui-sm align-middle ${stateChip[tone]}`}>
      <Icon name={stateIcon[tone]} size="sm" />
    </span>
  );
}

/**
 * Each Check's row key, which also names its ⋯ Menu: its shown name, with
 * its id after it when two Checks share the name.
 */
function keysOf(checks: readonly Check[]): (c: Check) => string {
  const count = new Map<string, number>();
  for (const c of checks) count.set(shownName(c), (count.get(shownName(c)) ?? 0) + 1);
  return (c) => (count.get(shownName(c))! > 1 ? `${shownName(c)} (${c.id})` : shownName(c));
}

/**
 * The Checks explorer: search, type and tab (each kept in the URL) over a
 * table with one row per Check, its Actions in the row's ⋯ Menu. Without
 * checks it is still loading.
 */
function Explorer({
  checks: listed,
  onEdit,
  onShow,
  onMaintain,
}: {
  checks?: readonly Check[];
  onEdit: (c: Check) => void;
  onShow: (c: Check) => void;
  onMaintain: (c: Check) => void;
}) {
  const loading = !listed;
  const checks = listed ?? [];
  const mute = useAction(ChecksService.method.setMuted);
  const disable = useAction(ChecksService.method.setDisabled);
  const remove = useAction(ChecksService.method.deleteCheck);
  const failure = mute.error ?? disable.error ?? remove.error;
  const busy = mute.pending || disable.pending || remove.pending;

  const actions = (c: Check): MenuItem[] => [
    // An App Check follows its App: it is edited and removed in apps.
    ...(c.appId ? [] : [{ label: "Edit", onSelect: () => onEdit(c), disabled: busy }]),
    { label: "Maintenance", onSelect: () => onMaintain(c), disabled: busy },
    { label: c.muted ? "Unmute" : "Mute", onSelect: () => void mute.run({ id: c.id, muted: !c.muted }), disabled: busy },
    { label: c.disabled ? "Enable" : "Disable", onSelect: () => void disable.run({ id: c.id, disabled: !c.disabled }), disabled: busy },
    ...(c.appId
      ? []
      : [
          {
            label: "Delete",
            danger: true,
            disabled: busy,
            onSelect: () =>
              void remove.run({ id: c.id }, { confirm: { text: `Delete the Check ${shownName(c)}? Its history goes with it.`, action: "Delete" } }),
          },
        ]),
  ];

  const columns: Column<Check>[] = [
    {
      id: "check",
      header: "Check",
      rowHeader: true,
      sortable: true,
      sortValue: shownName,
      cell: (c) => (
        <>
          <StateIcon check={c} />{" "}
          <button type="button" title="Show its history" onClick={() => onShow(c)}>
            {shownName(c)}
          </button>
          {c.muted && <> <Pill>muted</Pill></>}
          {c.flapping && <> <Pill tone="warn">flapping</Pill></>}
          {c.certExpiring && <> <Pill tone="warn">certificate expiring</Pill></>}
          <MaintenancePill check={c} />
        </>
      ),
    },
    {
      id: "state",
      header: "State",
      sortable: true,
      sortValue: stateLabel,
      cell: (c) => (
        <Pill tone={toneOf(c)} dot>
          {stateLabel(c)}
        </Pill>
      ),
    },
    { id: "type", header: "Type", sortable: true, sortValue: typeOf, cell: (c) => <Pill>{typeOf(c)}</Pill> },
    { id: "target", header: "Target", mono: true, cell: target },
    {
      id: "latency",
      header: "Latency",
      align: "right",
      sortable: true,
      sortValue: (c) => (showsLatency(c) ? c.latencyMs : Number.MAX_SAFE_INTEGER),
      cell: (c) => (showsLatency(c) ? latency(c.latencyMs) : !c.disabled && c.reason ? <span className="text-down-text">{c.reason}</span> : null),
    },
  ];

  const [query, setQuery] = useQueryState("q", "");
  const [type, setType] = useQueryState("type", "");
  const [tab, setTab] = useQueryState("tab", "all");
  const shown = tabOf(tab);
  const needle = query.trim().toLowerCase();
  const searched = checks.filter(
    (c) => (!type || typeOf(c) === type) && (!needle || `${shownName(c)} ${target(c)}`.toLowerCase().includes(needle)),
  );
  const tabs = tabNames.map((t) => ({ ...t, count: searched.filter(t.has).length }));

  return (
    <div className="flex flex-col gap-4">
      {failure && <Notice tone="down">{failure.rawMessage}</Notice>}
      <Panel
        flush
        toolbar={
          <>
            <SearchField label="Search Checks" value={query} onChange={setQuery} placeholder="Search by name or target" />
            <Select label="Type" value={type} onChange={(e) => setType(e.target.value)}>
              <option value="">Any type</option>
              {typeFilters.map((t) => (
                <option key={t} value={t}>
                  {t}
                </option>
              ))}
            </Select>
            <Tabs label="Show" value={shown.id} options={tabs} onChange={setTab} />
          </>
        }
      >
        <DataTable
          rows={searched.filter(shown.has)}
          rowKey={keysOf(checks)}
          columns={columns}
          actions={actions}
          onRowClick={onShow}
          sortKey="sort"
          empty="No Check matches."
          loading={loading}
        />
      </Panel>
    </div>
  );
}

type TabId = "all" | "down" | "attention" | "maint" | "muted";

/** The explorer's tabs, each with the Checks it shows. */
const tabNames: readonly { id: TabId; label: string; has: (c: Check) => boolean }[] = [
  { id: "all", label: "All", has: () => true },
  { id: "down", label: "Down", has: (c) => toneOf(c) === "down" },
  { id: "attention", label: "Attention", has: (c) => !c.disabled && (toneOf(c) === "warn" || c.certExpiring) },
  { id: "maint", label: "Maintenance", has: (c) => c.maintenance },
  { id: "muted", label: "Muted", has: (c) => c.muted },
];

/** The tab a URL names, All for one it doesn't know. */
const tabOf = (id: string) => tabNames.find((t) => t.id === id) ?? tabNames[0];

const typeFilters = ["HTTP", "TCP", "ICMP", "DNS"] as const;

/** The middle of xs, or the mean of the middle two; xs isn't empty. */
function median(xs: readonly number[]): number {
  const s = [...xs].sort((a, b) => a - b);
  const mid = Math.floor(s.length / 2);
  return s.length % 2 ? s[mid] : (s[mid - 1] + s[mid]) / 2;
}

/** Whether a Check's row shows its latency: it is up and has beaten. */
const showsLatency = (c: Check) => !c.disabled && !c.maintenance && c.state === State.UP && !!c.lastBeat;

/** A Check's type as people read it: "HTTP", "TCP", "ICMP" or "DNS". */
const typeOf = (c: Check) => typeLabels[c.spec?.type ?? CheckType.UNSPECIFIED];

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

  const formId = useId();
  return (
    <Sheet
      title={check ? `Edit ${shownName(check)}` : "Add Check"}
      onClose={onClose}
      footer={
        <FormActions>
          <Button variant="ghost" onClick={onClose}>
            Cancel
          </Button>
          <Button type="submit" form={formId} disabled={create.pending || update.pending}>
            Save
          </Button>
        </FormActions>
      }
    >
      <form id={formId} className="flex flex-col gap-4" onSubmit={save}>
        <Field label="Name">
          <TextInput value={name} onChange={(e) => setName(e.target.value)} placeholder="Optional; the target is shown otherwise" />
        </Field>
        <Field label="Type">
          <Segmented label="Type" value={type} options={typeNames} onChange={setType} />
        </Field>
        <Field
          label={targetFields[type].label}
          hint={`Probed every 60 s with a 10 s timeout.${type === "DNS" ? " Up when the server answers with at least one record." : ""}`}
        >
          <TextInput value={targetValue} onChange={(e) => setTarget(e.target.value)} placeholder={targetFields[type].placeholder} />
        </Field>
        {type === "DNS" && (
          <>
            <Field label="Name to resolve">
              <TextInput value={dnsName} onChange={(e) => setDnsName(e.target.value)} placeholder="jellyfin.home.example" />
            </Field>
            <Field label="Record type">
              <Segmented label="Record type" value={record} options={recordNames} onChange={setRecord} />
            </Field>
          </>
        )}
        {failure && <Notice tone="down">{failure.rawMessage}</Notice>}
      </form>
    </Sheet>
  );
}
