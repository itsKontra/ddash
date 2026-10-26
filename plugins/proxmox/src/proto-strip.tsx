// PROTOTYPE (wayfinder: "What does a row's inline action strip look like and
// hold, for Devices and Workloads?"): a Workload row's strip, its arrow's
// aside and its Status cell in each variant (see @ddash/ui's proto-strip.tsx).
// Throwaway; prototype/row-action-strip only.
import type { Workload } from "@ddash/proto/proxmox-api/proxmox_api_pb";
import { Button, Icon, InlineConfirm, Notice, Pill, TwoStepButton, useStripVariant } from "@ddash/ui";
import { useState } from "react";
import { ProtectedLock, usePower, type Item } from "./power";
import { StatusPill } from "./workloads";

export function WorkloadStrip({ workload: w }: { workload: Workload }) {
  const v = useStripVariant();
  const p = usePower(w);
  const [asking, setAsking] = useState<Item>();
  if (!p.known) return <span className="text-sm text-muted-foreground">Reading ddash's permissions…</span>;
  if (p.protected)
    return (
      <div className="flex flex-wrap items-center gap-3 text-sm">
        <ProtectedLock />
        <span className="text-muted-foreground">ddash doesn't hold VM.PowerMgmt on it. Start, stop or restart it in Proxmox.</span>
      </div>
    );
  const error = p.error && <Notice tone="down">{p.error.rawMessage}</Notice>;
  const none = p.items.length === 0 && (
    <span className="text-sm text-muted-foreground">No power Actions for a {w.template ? "template" : w.status} Workload.</span>
  );

  if (v === "2") {
    if (asking)
      return (
        <InlineConfirm
          text={asking.ask ?? `${asking.label}?`}
          action={asking.label}
          onCancel={() => setAsking(undefined)}
          onConfirm={() => {
            setAsking(undefined);
            asking.now();
          }}
        />
      );
    return (
      <div className="flex flex-col gap-3 pt-2">
        <div className="flex flex-wrap items-center gap-2">
          {p.items.map((i) => (
            <Button key={i.label} variant={i.danger ? "danger" : "default"} disabled={p.busy} onClick={() => (i.danger ? setAsking(i) : i.now())}>
              {i.label}
            </Button>
          ))}
          {none}
          {p.task && (
            <span className="inline-flex items-center gap-2 text-sm text-muted-foreground">
              <span className="flex motion-safe:animate-spin">
                <Icon name="loader-circle" size="sm" />
              </span>
              Proxmox is {p.task} it…
            </span>
          )}
        </div>
        {error}
      </div>
    );
  }

  if (v === "3") {
    return (
      <div className="flex flex-col gap-2">
        <div className="flex flex-wrap items-center justify-between gap-x-6 gap-y-2">
          <p className="text-xs text-muted-foreground">
            {p.task ? `Proxmox is ${p.task} it` : p.items.some((i) => i.danger) ? "Stop and Restart ask for a second click." : "Power"}
          </p>
          <div className="flex flex-wrap items-center gap-2">
            {p.items.map((i) =>
              i.danger ? (
                <TwoStepButton key={i.label} label={i.label} armed={`Click again to ${i.label.toLowerCase()}`} disabled={p.busy} onRun={i.now} />
              ) : (
                <Button key={i.label} size="sm" disabled={p.busy} onClick={i.now}>
                  {i.label}
                </Button>
              ),
            )}
            {none}
          </div>
        </div>
        {error}
      </div>
    );
  }

  return (
    <div className="flex flex-col gap-2">
      <div className="flex flex-wrap items-center gap-2">
        {p.items.map((i) => (
          <Button key={i.label} size="sm" icon={i.label === "Start" ? "play" : i.label === "Stop" ? "square" : "rotate-cw"} variant={i.danger ? "danger" : "default"} disabled={p.busy} onClick={i.run}>
            {i.label}
          </Button>
        ))}
        {none}
        {p.task && <Pill tone="maint">{p.task}</Pill>}
      </div>
      {error}
    </div>
  );
}

/** Left of the arrow: 1 the busy pill (and the lock in place of the arrow), 3 a spinner, 2 nothing. */
export function WorkloadAside({ workload: w }: { workload: Workload }) {
  const v = useStripVariant();
  const p = usePower(w);
  if (v === "1" && p.protected) return <ProtectedLock />;
  if (!p.task) return null;
  if (v === "1") return <Pill tone="maint">{p.task}</Pill>;
  if (v === "3")
    return (
      <span title={`Proxmox is ${p.task} it`} className="flex text-maint-text motion-safe:animate-spin">
        <Icon name="loader-circle" size="sm" label={p.task} />
      </span>
    );
  return null;
}

/** The Status cell: in 2, a running power Action replaces the status. */
export function WorkloadStatus({ workload: w }: { workload: Workload }) {
  const v = useStripVariant();
  const p = usePower(w);
  if (v === "2" && p.task)
    return (
      <Pill tone="maint" dot>
        {p.task}
      </Pill>
    );
  return <StatusPill workload={w} />;
}
