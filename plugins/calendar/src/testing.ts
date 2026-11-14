import { vi } from "vitest";

/** The calendar Plugin's scope in tests. */
export const calendar = { name: "calendar", requires: ["calendar-api"] };

/** The tests' clock: Saturday 14 November 2026, noon, in the browser's zone. */
export const now = new Date(2026, 10, 14, 12, 0);

/** Stops Date at now (only Date: the fake core's timers still run). */
export function stopClock() {
  vi.useFakeTimers({ toFake: ["Date"] });
  vi.setSystemTime(now);
}

/** The local time h:m on the day n days from now. */
export const local = (n: number, h = 0, m = 0) => new Date(2026, 10, 14 + n, h, m);

const pad = (n: number) => String(n).padStart(2, "0");
/** The local date n days from now as YYYY-MM-DD. */
export const date = (n: number) => {
  const d = local(n);
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`;
};

/** A timed event (Connect JSON) from start to end. */
export const timedEvent = (calendar: string, title: string, start: Date, end: Date, location?: string) => ({
  calendar,
  id: `${calendar}/${title}/${start.toISOString()}`,
  title,
  location,
  start: start.toISOString(),
  end: end.toISOString(),
});

/** An all-day event (Connect JSON) from day first to the day before dayAfter. */
export const allDayEvent = (calendar: string, title: string, first: number, dayAfter: number) => ({
  calendar,
  id: `${calendar}/${title}/${date(first)}`,
  title,
  allDay: true,
  startDate: date(first),
  endDate: date(dayAfter),
});

/** What calendar-api answers ListEvents with: two calendars, the last two days to two weeks ahead. */
export const listed = {
  calendars: [
    { name: "Home", readAt: now.toISOString() },
    { name: "Work", readAt: now.toISOString() },
  ],
  events: [
    timedEvent("Home", "Dentist", local(-1, 10), local(-1, 11), "Main Street 12"),
    allDayEvent("Home", "Put the bins out", 0, 1),
    timedEvent("Work", "Stand-up", local(0, 9, 30), local(0, 9, 45)),
    timedEvent("Home", "Football training", local(0, 18), local(0, 19, 30), "Sports ground"),
    timedEvent("Home", "Dinner with friends", local(1, 19, 30), local(1, 22)),
    allDayEvent("Home", "Grandma's birthday", 2, 3),
    timedEvent("Work", "Team offsite", local(3, 9), local(4, 17), "Lakeside hotel"),
    allDayEvent("Home", "Weekend in the mountains", 4, 7),
  ],
  from: local(-2, 12).toISOString(),
  to: local(14, 12).toISOString(),
};
