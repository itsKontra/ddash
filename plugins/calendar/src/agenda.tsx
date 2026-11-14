import type { ListEventsResponse } from "@ddash/proto/calendar-api/calendar_api_pb";
import { timestampDate } from "@bufbuild/protobuf/wkt";
import { EmptyState, Notice, PageTitle, Panel, Pill, Skeleton } from "@ddash/ui";
import { Link } from "react-router";
import { byDay, dayLabel, daysFrom, ended, entryTime, timed, useEvents, useNow, type Day } from "./events";

/** The way to set up a calendar: calendar-api's config in Plugins. */
export function NotSetUp() {
  return (
    <Notice tone="info" title="No calendar is set up">
      Give calendar-api each calendar's secret iCal address in{" "}
      <Link to="/plugins" className="font-semibold text-primary hover:underline">
        Plugins
      </Link>
      . ddash only reads it.
    </Notice>
  );
}

/** Why calendar-api gave no events: not configured yet, or the failure. */
function Unlisted({ error }: { error: { rawMessage: string } }) {
  if (error.rawMessage === "not configured") return <NotSetUp />;
  return (
    <Notice tone="down" title="calendar-api can't list the events">
      {error.rawMessage}
    </Notice>
  );
}

/** One day's events: whole days first, then by time, each with its calendar. */
function Entries({ day }: { day: Day }) {
  return (
    <ul className="flex flex-col divide-y divide-border">
      {day.entries.map((e) => (
        <li key={e.timed.event.id} className="flex items-start gap-4 py-2.5 first:pt-0 last:pb-0">
          <span className="w-32 shrink-0 text-sm text-muted-foreground tabular-nums">{entryTime(e)}</span>
          <div className="min-w-0 flex-1">
            <p className="text-sm font-semibold break-words">{e.timed.event.title || "(no title)"}</p>
            {e.timed.event.location && <p className="mt-0.5 text-xs break-words text-muted-foreground">{e.timed.event.location}</p>}
          </div>
          <Pill>{e.timed.event.calendar}</Pill>
        </li>
      ))}
    </ul>
  );
}

/** A day's full date under Today, Tomorrow or Yesterday. */
const fullDate = (d: Date, now: Date) =>
  Math.abs(daysFrom(d, now)) <= 1 ? d.toLocaleDateString(undefined, { weekday: "long", day: "numeric", month: "long" }) : undefined;

/** A past day's heading: "Earlier today", "Yesterday", else as dayLabel. */
const pastLabel = (d: Date, now: Date) => (daysFrom(d, now) === 0 ? "Earlier today" : dayLabel(d, now));

function Agenda({ data }: { data: ListEventsResponse }) {
  const now = useNow();
  const all = data.events.map(timed);
  const coming = byDay(all.filter((t) => !ended(t, now)), now);
  const past = byDay(all.filter((t) => ended(t, now)), data.from ? timestampDate(data.from) : now).reverse();
  const ahead = data.to ? Math.round((timestampDate(data.to).getTime() - now.getTime()) / 86_400_000) : 0;
  return (
    <div className="flex flex-col gap-5">
      {coming.length === 0 ? (
        <Panel>
          <EmptyState icon="calendar" title="Nothing coming up">
            No event in the next {ahead} days.
          </EmptyState>
        </Panel>
      ) : (
        coming.map((day) => (
          <Panel key={day.date.getTime()} title={dayLabel(day.date, now)} description={fullDate(day.date, now)}>
            <Entries day={day} />
          </Panel>
        ))
      )}
      {past.length > 0 && (
        <Panel title="Just past" description="The events of the last two days that are over.">
          <div className="flex flex-col gap-5">
            {past.map((day) => (
              <section key={day.date.getTime()} aria-label={pastLabel(day.date, now)}>
                <h3 className="mb-2 text-xs font-bold text-muted-foreground">{pastLabel(day.date, now)}</h3>
                <Entries day={day} />
              </section>
            ))}
          </div>
        </Panel>
      )}
    </div>
  );
}

/**
 * The calendar Page: the coming days' events grouped by day (Today,
 * Tomorrow, then weekday and date), then those just past, each with its
 * calendar, in the browser's time and locale.
 */
export function AgendaPage() {
  const { data, error } = useEvents();
  const unreadable = data?.calendars.filter((c) => c.error) ?? [];
  return (
    <div>
      <PageTitle kicker="Calendar" title="Agenda" subtitle="The coming days and the events just past, from every calendar. ddash only reads them." />
      {error && !data ? (
        <Unlisted error={error} />
      ) : !data ? (
        <Panel>
          <Skeleton lines={6} />
        </Panel>
      ) : data.calendars.length === 0 ? (
        <NotSetUp />
      ) : (
        <div className="flex flex-col gap-5">
          {unreadable.map((c) => (
            <Notice key={c.name} tone="warn" title={`${c.name} can't be read`}>
              {c.error}
            </Notice>
          ))}
          <Agenda data={data} />
        </div>
      )}
    </div>
  );
}
