import { useAction } from "@ddash/plugin";
import { CheckType, ChecksService, RecordType, type Check } from "@ddash/proto/checks/checks_pb";
import { Badge, Button, PageHeader, Segmented, Sheet, StatusDot } from "@ddash/ui";
import { useId, useState, type FormEvent } from "react";
import { useParams } from "react-router";
import { latency, shownName, stateLabel, toneOf, useChecks } from "./checks";
import { CheckDetail } from "./detail";

/** The checks Page: every Check with its state and latency, and their editing. */
export function ChecksPage() {
  const { data, error } = useChecks();
  // undefined: no form; null: adding; a Check: editing it.
  const [editing, setEditing] = useState<Check | null | undefined>(undefined);
  // The Check whose detail is open, by id, so it follows the list's refreshes.
  // A Notification's link (/checks/<id>) opens it.
  const { id } = useParams();
  const [shown, setShown] = useState<bigint | undefined>(() => (id && /^\d+$/.test(id) ? BigInt(id) : undefined));
  const detail = data?.checks.find((c) => c.id === shown);
  return (
    <div>
      <PageHeader
        title="Checks"
        subtitle="Every App's HTTP(S) Check plus your TCP, ICMP and DNS ones, probed every minute: down after 3 failures in a row, up again on one success."
        actions={<Button onClick={() => setEditing(null)}>Add Check</Button>}
      />
      {error && !data && <p className="text-sm text-down">{error.message}</p>}
      {data && data.checks.length === 0 && <p className="text-sm text-muted-foreground">No Checks yet. Add one to watch any address.</p>}
      {data && data.checks.length > 0 && (
        <div className="rounded-ui border border-border bg-card">
          <table aria-label="Checks" className="w-full text-sm">
            <thead className="text-left text-xs text-muted-foreground">
              <tr className="border-b border-border">
                <th className="w-6 py-2 pl-4" />
                <th className="px-2 py-2 font-medium">Check</th>
                <th className="px-2 py-2 font-medium">Type</th>
                <th className="px-2 py-2 font-medium">Target</th>
                <th className="px-2 py-2 font-medium">Latency</th>
                <th className="py-2 pr-4" />
              </tr>
            </thead>
            <tbody>
              {data.checks.map((c) => (
                <Row key={String(c.id)} check={c} onEdit={() => setEditing(c)} onShow={() => setShown(c.id)} />
              ))}
            </tbody>
          </table>
        </div>
      )}
      {editing !== undefined && <CheckForm check={editing ?? undefined} onClose={() => setEditing(undefined)} />}
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

function Row({ check: c, onEdit, onShow }: { check: Check; onEdit: () => void; onShow: () => void }) {
  const mute = useAction(ChecksService.method.setMuted);
  const disable = useAction(ChecksService.method.setDisabled);
  const remove = useAction(ChecksService.method.deleteCheck);
  const failure = mute.error ?? disable.error ?? remove.error;
  const busy = mute.pending || disable.pending || remove.pending;
  const up = !c.disabled && toneOf(c) === "up";
  return (
    <tr className="border-b border-border last:border-b-0">
      <td className="w-6 py-2 pl-4">
        <StatusDot tone={toneOf(c)} label={stateLabel(c)} />
      </td>
      <th scope="row" className="px-2 py-2 text-left font-medium">
        <span className="flex items-center gap-2">
          <button type="button" className="cursor-pointer text-left hover:underline" title="Show its history" onClick={onShow}>
            {shownName(c)}
          </button>
          {c.muted && <Badge>muted</Badge>}
          {c.certExpiring && <Badge tone="warn">certificate expiring</Badge>}
        </span>
      </th>
      <td className="px-2 py-2 text-muted-foreground">{typeLabels[c.spec?.type ?? CheckType.UNSPECIFIED]}</td>
      <td className="truncate px-2 py-2 text-muted-foreground">{target(c)}</td>
      <td className="px-2 py-2 text-muted-foreground tabular-nums">
        {up && c.lastBeat ? latency(c.latencyMs) : !c.disabled && c.reason ? <span className="text-down">{c.reason}</span> : null}
      </td>
      <td className="py-2 pr-4">
        <div className="flex items-center justify-end gap-1">
          {failure && (
            <span role="alert" className="text-xs text-down">
              {failure.rawMessage}
            </span>
          )}
          {/* An App Check follows its App: it is edited and removed in apps. */}
          {!c.appId && (
            <Button variant="ghost" size="sm" disabled={busy} onClick={onEdit}>
              Edit
            </Button>
          )}
          <Button variant="ghost" size="sm" disabled={busy} onClick={() => mute.run({ id: c.id, muted: !c.muted })}>
            {c.muted ? "Unmute" : "Mute"}
          </Button>
          <Button variant="ghost" size="sm" disabled={busy} onClick={() => disable.run({ id: c.id, disabled: !c.disabled })}>
            {c.disabled ? "Enable" : "Disable"}
          </Button>
          {!c.appId && (
            <Button
              variant="ghost"
              size="sm"
              disabled={busy}
              onClick={() =>
                remove.run({ id: c.id }, { confirm: { text: `Delete the Check ${shownName(c)}? Its history goes with it.`, action: "Delete" } })
              }
            >
              Delete
            </Button>
          )}
        </div>
      </td>
    </tr>
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
