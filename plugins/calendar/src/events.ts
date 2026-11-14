import { timestampDate } from "@bufbuild/protobuf/wkt";
import { useQuery } from "@ddash/plugin";
import { CalendarApiService, type Event } from "@ddash/proto/calendar-api/calendar_api_pb";
import { useEffect, useState } from "react";

/** calendar-api's events and calendars, refreshed as its getter says. */
export function useEvents() {
  return useQuery(CalendarApiService.method.listEvents, {});
}

/** The time, moving on every minute, so an event drops into the past (and Today into the next day) on screen. */
export function useNow(): Date {
  const [now, setNow] = useState(() => new Date());
  useEffect(() => {
    const id = setInterval(() => setNow(new Date()), 60_000);
    return () => clearInterval(id);
  }, []);
  return now;
}

/** An event in the browser's time: an all-day event from its first day's local midnight to its end day's. */
export interface Timed {
  event: Event;
  start: Date;
  end: Date;
}

const localDay = (ymd: string) => {
  const [y, m, d] = ymd.split("-").map(Number);
  return new Date(y, m - 1, d);
};

export function timed(event: Event): Timed {
  if (event.allDay) return { event, start: localDay(event.startDate), end: localDay(event.endDate || event.startDate) };
  const start = event.start ? timestampDate(event.start) : new Date(0);
  return { event, start, end: event.end ? timestampDate(event.end) : start };
}

export const startOfDay = (d: Date) => new Date(d.getFullYear(), d.getMonth(), d.getDate());
const nextDay = (d: Date) => new Date(d.getFullYear(), d.getMonth(), d.getDate() + 1);

/** Whether it is over by now. */
export const ended = (t: Timed, now: Date) => t.end.getTime() <= now.getTime();

/** One event on one day: the whole day, or from and until a time on it. */
export interface Entry {
  timed: Timed;
  wholeDay: boolean;
  /** Its start, when it starts on this day. */
  from?: Date;
  /** Its end, when it ends on this day. */
  until?: Date;
}

/** One day of the agenda and what happens on it: whole days first, then by time. */
export interface Day {
  date: Date;
  entries: Entry[];
}

/**
 * The days the events fall on, from the day of since on, earliest first.
 * An event spanning several days is on each of them.
 */
export function byDay(events: readonly Timed[], since: Date): Day[] {
  const days = new Map<number, Day>();
  const first = startOfDay(since);
  for (const t of events) {
    // A zero-length event is on the day it happens.
    const last = t.end > t.start ? t.end : new Date(t.start.getTime() + 1);
    const begin = startOfDay(t.start);
    for (let d = begin < first ? first : begin; d < last; d = nextDay(d)) {
      const end = nextDay(d);
      const wholeDay = t.event.allDay || (t.start <= d && t.end >= end);
      const entry: Entry = { timed: t, wholeDay };
      if (!wholeDay) {
        if (t.start >= d) entry.from = t.start;
        if (t.end < end) entry.until = t.end;
      }
      const day = days.get(d.getTime()) ?? { date: d, entries: [] };
      day.entries.push(entry);
      days.set(d.getTime(), day);
    }
  }
  const out = [...days.values()].sort((a, b) => a.date.getTime() - b.date.getTime());
  for (const day of out) {
    day.entries.sort((a, b) => Number(b.wholeDay) - Number(a.wholeDay) || (a.from ?? day.date).getTime() - (b.from ?? day.date).getTime());
  }
  return out;
}

const DAY = 24 * 60 * 60 * 1000;

/** How many days d is from today: 0 today, 1 tomorrow, -1 yesterday. */
export const daysFrom = (d: Date, now: Date) => Math.round((startOfDay(d).getTime() - startOfDay(now).getTime()) / DAY);

/** A day's heading: Today, Tomorrow, Yesterday, else its weekday and date in the browser's locale. */
export function dayLabel(d: Date, now: Date): string {
  switch (daysFrom(d, now)) {
    case 0:
      return "Today";
    case 1:
      return "Tomorrow";
    case -1:
      return "Yesterday";
  }
  return d.toLocaleDateString(undefined, { weekday: "long", day: "numeric", month: "long" });
}

/** A day in short: Today, Tomorrow, else e.g. "Mon, Nov 16" in the browser's locale. */
export function shortDay(d: Date, now: Date): string {
  const n = daysFrom(d, now);
  if (n === 0) return "Today";
  if (n === 1) return "Tomorrow";
  return d.toLocaleDateString(undefined, { weekday: "short", day: "numeric", month: "short" });
}

/** A time of day in the browser's locale, e.g. "18:30" or "06:30 PM". */
export const clock = (d: Date) => d.toLocaleTimeString(undefined, { hour: "2-digit", minute: "2-digit" });

/** When an entry happens on its day: "All day", "18:00 – 19:30", "from 09:00" or "until 17:00". */
export function entryTime(e: Entry): string {
  if (e.wholeDay) return "All day";
  if (e.from && e.until) return e.from.getTime() === e.until.getTime() ? clock(e.from) : `${clock(e.from)} – ${clock(e.until)}`;
  if (e.from) return `from ${clock(e.from)}`;
  return e.until ? `until ${clock(e.until)}` : "";
}
