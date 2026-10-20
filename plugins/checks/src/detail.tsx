import { timestampMs } from "@bufbuild/protobuf/wkt";
import { State, type Check } from "@ddash/proto/checks/checks_pb";
import { Segmented, Sheet, Stat, StatusDot, UptimeBars } from "@ddash/ui";
import { useId, useState, type ReactNode } from "react";
import { latency, shownName } from "./checks";
import { bars, percent, useHistory, when, windowNames, type WindowName } from "./history";

/** A Check's history: uptime over 24 h, 30 d and 365 d, and one window's latency, UptimeBars and state changes. */
export function CheckDetail({ check, onClose }: { check: Check; onClose: () => void }) {
  const [name, setName] = useState<WindowName>("24 h");
  const { data, error } = useHistory(check.id, name);
  const selected = data?.uptime[windowNames.indexOf(name)];
  return (
    <Sheet title={shownName(check)} onClose={onClose}>
      <div className="flex flex-col gap-5">
        {error && !data && <p className="text-sm text-down">{error.message}</p>}
        <Section title="Uptime">
          <div className="grid grid-cols-3 gap-3">
            {windowNames.map((w, i) => (
              <Stat key={w} label={w} value={data ? percent(data.uptime[i]?.ratio) : "…"} />
            ))}
          </div>
        </Section>
        <Segmented label="Window" value={name} options={windowNames} onChange={setName} />
        <Section title="Latency">
          {selected?.latency ? (
            <div className="grid grid-cols-3 gap-3">
              <Stat label="average" value={latency(selected.latency.avgMs)} />
              <Stat label="min" value={latency(selected.latency.minMs)} />
              <Stat label="max" value={latency(selected.latency.maxMs)} />
            </div>
          ) : (
            <p className="text-sm text-muted-foreground">{data ? `No up beats in the last ${name}.` : "…"}</p>
          )}
        </Section>
        {data && (
          <>
            <div className="flex flex-col gap-1">
              <UptimeBars label={`Uptime, last ${name}`} bars={bars(data, name)} />
              <div className="flex text-xs text-muted-foreground">
                <span>{name} ago</span>
                <span className="ml-auto">now</span>
              </div>
            </div>
            <Section title="State changes">
              {data.changes.length === 0 ? (
                <p className="text-sm text-muted-foreground">No state changes in the last {name}.</p>
              ) : (
                <ul aria-label="State changes" className="flex flex-col gap-1.5 text-sm">
                  {data.changes.map((c, i) => (
                    <li key={i} className="flex items-center gap-2">
                      <StatusDot tone={c.state === State.UP ? "up" : c.state === State.DOWN ? "down" : "warn"} label={stateWord(c.state)} />
                      <span className="font-medium">{stateWord(c.state)}</span>
                      <span className="text-muted-foreground">{c.at ? when(timestampMs(c.at)) : ""}</span>
                      {c.reason && <span className="truncate text-down">{c.reason}</span>}
                    </li>
                  ))}
                </ul>
              )}
            </Section>
          </>
        )}
      </div>
    </Sheet>
  );
}

function stateWord(s: State): string {
  return s === State.UP ? "up" : s === State.DOWN ? "down" : "pending";
}

function Section({ title, children }: { title: string; children: ReactNode }) {
  const id = useId();
  return (
    <section aria-labelledby={id} className="flex flex-col gap-2">
      <h3 id={id} className="text-sm font-semibold">
        {title}
      </h3>
      {children}
    </section>
  );
}
