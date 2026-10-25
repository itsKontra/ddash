import { timestampMs } from "@bufbuild/protobuf/wkt";
import { kindLabel } from "@ddash/display";
import { useAction, useQuery } from "@ddash/plugin";
import { ChecksService, type Check, type Maintenance, type MaintenanceScope } from "@ddash/proto/checks/checks_pb";
import { Kind, type WorkloadRef } from "@ddash/proto/ddash/workload/workload_pb";
import { Button, Field, FormActions, Notice, Panel, Pill, Segmented, Select, Sheet, StatusList } from "@ddash/ui";
import { useState } from "react";
import { shownName } from "./checks";

/** The Maintenance in force, refreshed on the getter's FAST tier. */
export function useMaintenance() {
  return useQuery(ChecksService.method.listMaintenance, {});
}

/** A Workload as the Checks Page names it: "LXC 110", "Docker sonarr on media", "External 198.18.1.5". */
export function workloadLabel(w: WorkloadRef): string {
  switch (w.kind) {
    case Kind.DOCKER:
      return `Docker ${w.id} on ${w.host}`;
    case Kind.EXTERNAL:
      return `External ${w.host}`;
    default:
      return `${kindLabel(w.kind)} ${w.id}`;
  }
}

/** A time of day, e.g. "14:30". */
const clock = (ms: number) => new Date(ms).toLocaleTimeString(undefined, { hour: "2-digit", minute: "2-digit" });

/** The Pill a Check in Maintenance carries: until when. */
export function MaintenancePill({ check: c }: { check: Check }) {
  if (!c.maintenance) return null;
  return (
    <>
      {" "}
      <Pill tone="maint">Maintenance until {c.maintenanceEnds ? clock(timestampMs(c.maintenanceEnds)) : "ended"}</Pill>
    </>
  );
}

/** What a Maintenance covers, as people read it. */
function scopeLabel(s: MaintenanceScope | undefined, checks: readonly Check[]): string {
  switch (s?.scope.case) {
    case "everything":
      return "Everything";
    case "workload":
      return workloadLabel(s.scope.value);
    case "checkId": {
      const id = s.scope.value;
      const c = checks.find((c) => c.id === id);
      return c ? shownName(c) : `Check ${id}`;
    }
  }
  return "";
}

/** When a Maintenance ends: "until 14:30", "until ended", plus "or until its Checks are up". */
function untilText(m: Maintenance): string {
  const at = m.ends ? `until ${clock(timestampMs(m.ends))}` : "until ended";
  return m.untilUp ? (m.ends ? `${at} or until its Checks are up` : "until its Checks are up") : at;
}

/** The Maintenance in force, each with an End button; nothing while there is none. */
export function MaintenanceList({ checks }: { checks: readonly Check[] }) {
  const { data } = useMaintenance();
  const end = useAction(ChecksService.method.endMaintenance);
  if (!data?.maintenance.length) return null;
  return (
    <Panel title="Maintenance">
      <div className="flex flex-col gap-3">
        {end.error && <Notice tone="down">{end.error.rawMessage}</Notice>}
        <StatusList
          items={data.maintenance.map((m) => ({
            tone: "maint",
            title: scopeLabel(m.scope, checks),
            meta: untilText(m),
            aside: (
              <Button variant="ghost" size="sm" disabled={end.pending} onClick={() => end.run({ scope: m.scope })}>
                End
              </Button>
            ),
          }))}
        />
      </div>
    </Panel>
  );
}

const lengths = ["30 min", "1 h", "4 h", "Until ended"] as const;
type Length = (typeof lengths)[number];
const seconds: Record<Length, bigint | undefined> = { "30 min": 1800n, "1 h": 3600n, "4 h": 14_400n, "Until ended": undefined };

const covers = ["Everything", "A Workload"] as const;
type Covers = (typeof covers)[number];

/** The Workloads the App Checks run on, by label, each once. */
function workloadsOf(checks: readonly Check[]): { label: string; ref: WorkloadRef }[] {
  const byLabel = new Map<string, WorkloadRef>();
  for (const c of checks) if (c.appId && c.workload) byLabel.set(workloadLabel(c.workload), c.workload);
  return [...byLabel].sort(([a], [b]) => a.localeCompare(b)).map(([label, ref]) => ({ label, ref }));
}

/**
 * Starts Maintenance for check, or (without one) for everything or a
 * Workload its App Checks run on, for 30 min, 1 h, 4 h or until ended.
 */
export function MaintenanceSheet({ check, checks, onClose }: { check?: Check; checks: readonly Check[]; onClose: () => void }) {
  const start = useAction(ChecksService.method.startMaintenance);
  const workloads = workloadsOf(checks);
  const [cover, setCover] = useState<Covers>("Everything");
  const [picked, setPicked] = useState(0);
  const [length, setLength] = useState<Length>("1 h");

  async function go() {
    const workload = workloads[picked]?.ref;
    const scope = check
      ? { scope: { case: "checkId" as const, value: check.id } }
      : cover === "A Workload" && workload
        ? { scope: { case: "workload" as const, value: workload } }
        : { scope: { case: "everything" as const, value: true } };
    const s = seconds[length];
    if (await start.run({ scope, duration: s === undefined ? undefined : { seconds: s } })) onClose();
  }

  return (
    <Sheet
      title={check ? `Maintenance for ${shownName(check)}` : "Maintenance"}
      subtitle="Beats in Maintenance are recorded as such and left out of uptime. The Checks keep being probed."
      onClose={onClose}
      footer={
        <FormActions>
          <Button variant="ghost" onClick={onClose}>
            Cancel
          </Button>
          <Button disabled={start.pending} onClick={go}>
            Start
          </Button>
        </FormActions>
      }
    >
      <div className="flex flex-col gap-4">
        {!check && (
          <Field label="Covers">
            <Segmented label="Covers" value={cover} options={workloads.length ? covers : covers.slice(0, 1)} onChange={setCover} />
          </Field>
        )}
        {!check && cover === "A Workload" && (
          <Field label="Workload" hint="Covers the Checks of every App running on it.">
            <Select value={picked} onChange={(e) => setPicked(Number(e.target.value))}>
              {workloads.map((w, i) => (
                <option key={w.label} value={i}>
                  {w.label}
                </option>
              ))}
            </Select>
          </Field>
        )}
        <Field label="For">
          <Segmented label="For" value={length} options={lengths} onChange={setLength} />
        </Field>
        {start.error && <Notice tone="down">{start.error.rawMessage}</Notice>}
      </div>
    </Sheet>
  );
}
