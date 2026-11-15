import type { Status, Widget, WidgetSize } from "@ddash/plugin";
import type { ListEventsResponse } from "@ddash/proto/calendar-api/calendar_api_pb";
import { Skeleton, StatusList } from "@ddash/ui";
import { Link } from "react-router";
import { clock, daysFrom, ended, shortDay, timed, useEvents, useNow, type Timed } from "./events";

/** The events not over yet, in the browser's time, soonest first. */
export function upcoming(data: ListEventsResponse, now: Date): Timed[] {
  return data.events.map(timed).filter((t) => !ended(t, now));
}

/** Whether it happens today (and isn't over). */
const today = (t: Timed, now: Date) => daysFrom(t.start, now) <= 0;

/**
 * Up with how many events are left today, idle with none (or without a
 * calendar). Warn when a calendar can't be read or calendar-api is
 * unavailable.
 */
function useStatus(): Status | undefined {
  const { data, error } = useEvents();
  const now = useNow();
  if (error) return { tone: "warn", summary: `calendar-api unavailable: ${error.rawMessage}` };
  if (!data) return undefined;
  if (data.calendars.length === 0) return { tone: "idle", summary: "No calendar set up" };
  const n = upcoming(data, now).filter((t) => today(t, now)).length;
  const unreadable = data.calendars.filter((c) => c.error).map((c) => `${c.name} unreadable`);
  const left = n ? `${n} today` : "Nothing today";
  if (unreadable.length) return { tone: "warn", summary: [...unreadable, left].join(" · ") };
  return n ? { tone: "up", summary: left } : { tone: "idle", summary: left };
}

/** A time in short: "18:00" today, else "Tomorrow 19:30" or "Mon, Nov 16 09:00". */
function at(d: Date, now: Date): string {
  const day = shortDay(d, now);
  return day === "Today" ? clock(d) : `${day} ${clock(d)}`;
}

/** When it happens, in short: "Today", "18:00", "Tomorrow 19:30", or "until 17:00" once it has begun. */
function when(t: Timed, now: Date): string {
  if (t.event.allDay) return t.start <= now ? "Today" : shortDay(t.start, now);
  return t.start <= now ? `until ${at(t.end, now)}` : at(t.start, now);
}

/** S: the next three events. M: the next five, with each one's calendar and place. Today's are highlighted. */
function Body({ size }: { size: WidgetSize }) {
  const { data, error } = useEvents();
  const now = useNow();
  // Unavailable: the Widget's status says why.
  if (error && !data) return null;
  if (!data) return <Skeleton lines={3} />;
  if (data.calendars.length === 0)
    return (
      <p className="text-sm text-muted-foreground">
        No calendar is set up.{" "}
        <Link to="/plugins" className="font-semibold text-primary hover:underline">
          Add one in Plugins
        </Link>
      </p>
    );
  const next = upcoming(data, now).slice(0, size === "S" ? 3 : 5);
  if (next.length === 0) return <p className="text-sm text-muted-foreground">Nothing coming up.</p>;
  return (
    <StatusList
      items={next.map((t) => ({
        tone: today(t, now) ? "info" : "idle",
        title: t.event.title || "(no title)",
        aside: when(t, now),
        meta: size === "S" ? undefined : [t.event.calendar, t.event.location].filter(Boolean).join(" · "),
      }))}
    />
  );
}

/** The Overview Widget: the next events across every calendar. */
export const nextEvents: Widget = {
  id: "next-events",
  title: "Next events",
  sizes: ["S", "M"],
  defaultSize: "S",
  defaultOrder: 70,
  Body,
  useStatus,
};
