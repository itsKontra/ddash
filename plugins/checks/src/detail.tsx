import { timestampMs } from "@bufbuild/protobuf/wkt";
import type { Check } from "@ddash/proto/checks/checks_pb";
import { EmptyState, MetricTile, Notice, Pill, SectionHeading, Segmented, Sheet, Skeleton, StatusList, UptimeBars } from "@ddash/ui";
import { useState } from "react";
import { latency, shownName, stateOf } from "./checks";
import { bars, percent, useHistory, when, windowNames, type WindowName } from "./history";

/** A Check's history: uptime over 24 h, 30 d and 365 d, and one window's latency, UptimeBars and state changes. */
export function CheckDetail({ check, onClose }: { check: Check; onClose: () => void }) {
  const [name, setName] = useState<WindowName>("24 h");
  const { data, error } = useHistory(check.id, name);
  const selected = data?.uptime[windowNames.indexOf(name)];
  return (
    <Sheet title={shownName(check)} onClose={onClose}>
      <div className="flex flex-col gap-4">
        {error && !data && <Notice tone="down">{error.message}</Notice>}
        <div>
          <SectionHeading title="Uptime" />
          <div className="grid grid-cols-3 gap-3">
            {windowNames.map((w, i) => (
              <MetricTile key={w} label={w} value={data && percent(data.uptime[i]?.ratio)} />
            ))}
          </div>
        </div>
        <Segmented label="Window" value={name} options={windowNames} onChange={setName} />
        <div>
          <SectionHeading title="Latency" />
          {selected?.latency ? (
            <div className="grid grid-cols-3 gap-3">
              <MetricTile label="average" value={latency(selected.latency.avgMs)} />
              <MetricTile label="min" value={latency(selected.latency.minMs)} />
              <MetricTile label="max" value={latency(selected.latency.maxMs)} />
            </div>
          ) : data ? (
            <EmptyState icon="gauge" title={`No up beats in the last ${name}.`} />
          ) : (
            <Skeleton height="xl" />
          )}
        </div>
        {data && (
          <>
            <div className="flex flex-col gap-1">
              <UptimeBars label={`Uptime, last ${name}`} bars={bars(data, name)} />
              <div className="flex text-xs text-muted-foreground">
                <span>{name} ago</span>
                <span className="ml-auto">now</span>
              </div>
            </div>
            <div>
              <SectionHeading title="State changes" />
              {data.changes.length === 0 ? (
                <EmptyState icon="clock" title={`No state changes in the last ${name}.`} />
              ) : (
                <StatusList
                  items={data.changes.map((c) => ({
                    tone: stateOf(c.state).tone,
                    title: stateOf(c.state).word,
                    aside: c.at ? when(timestampMs(c.at)) : undefined,
                    meta: c.reason ? <span className="text-down-text">{c.reason}</span> : undefined,
                    body: c.maintenance ? <Pill tone="maint">in Maintenance</Pill> : undefined,
                  }))}
                />
              )}
            </div>
          </>
        )}
      </div>
    </Sheet>
  );
}
