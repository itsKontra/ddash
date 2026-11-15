import { cleanup, screen, within } from "@testing-library/react";
import { pending, showPlugin, unavailable, type Answer } from "@ddash/plugin/testing";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { clock } from "./events";
import plugin from "./index";
import { calendar, listed, local, now, stopClock } from "./testing";

beforeEach(stopClock);
afterEach(() => {
  cleanup();
  vi.useRealTimers();
});

const show = (answer: Answer) => showPlugin(plugin, calendar, "/calendar", { answers: { ListEvents: answer } });

/** A day's heading as the Page writes it: its weekday and date in the browser's locale. */
const longDay = (n: number) => local(n).toLocaleDateString(undefined, { weekday: "long", day: "numeric", month: "long" });
const span = (from: Date, until: Date) => `${clock(from)} – ${clock(until)}`;

/** Each event in a region as "time | title · location | calendar". */
function rows(region: HTMLElement) {
  return within(region)
    .getAllByRole("listitem")
    .map((li) =>
      [...li.children]
        .map((c) => (c.tagName === "DIV" ? [...c.children].map((p) => p.textContent).join(" · ") : c.textContent))
        .join(" | "),
    );
}

it("is the Calendar Nav Entry at order 70 (its icon, calendar, is in plugin.yaml)", () => {
  expect(plugin.nav?.order).toBe(70);
});

it("lists the coming days, from Today, each in a section of its own", async () => {
  const calls = await show(() => Response.json(listed));

  expect(calls[0].url).toBe("/api/c/calendar-api/ddash.calendarapi.CalendarApiService/ListEvents");
  expect(screen.getByRole("heading", { level: 1, name: "Agenda" })).toBeTruthy();
  const days = screen
    .getAllByRole("region")
    .map((r) => r.getAttribute("aria-labelledby") && within(r).getByRole("heading", { level: 2 }).textContent)
    .filter(Boolean);
  expect(days).toEqual(["Today", "Tomorrow", longDay(2), longDay(3), longDay(4), longDay(5), longDay(6), "Just past"]);
});

it("puts all-day events first in a day, then the rest by time, each with its calendar", async () => {
  await show(() => Response.json(listed));

  // The stand-up is over: it is just past.
  expect(rows(screen.getByRole("region", { name: "Today" }))).toEqual([
    "All day | Put the bins out | Home",
    `${span(local(0, 18), local(0, 19, 30))} | Football training · Sports ground | Home`,
  ]);
});

it("lists an event spanning several days on each of them", async () => {
  await show(() => Response.json(listed));

  expect(rows(screen.getByRole("region", { name: longDay(3) }))).toEqual([`from ${clock(local(3, 9))} | Team offsite · Lakeside hotel | Work`]);
  expect(rows(screen.getByRole("region", { name: longDay(4) }))).toEqual([
    "All day | Weekend in the mountains | Home",
    `until ${clock(local(4, 17))} | Team offsite · Lakeside hotel | Work`,
  ]);
  expect(rows(screen.getByRole("region", { name: longDay(6) }))).toEqual(["All day | Weekend in the mountains | Home"]);
});

it("lists the events just past, the latest day first", async () => {
  await show(() => Response.json(listed));

  const past = screen.getByRole("region", { name: "Just past" });
  expect(within(past).getAllByRole("heading", { level: 3 }).map((h) => h.textContent)).toEqual(["Earlier today", "Yesterday"]);
  expect(rows(within(past).getByRole("region", { name: "Earlier today" }))).toEqual([`${span(local(0, 9, 30), local(0, 9, 45))} | Stand-up | Work`]);
  expect(rows(within(past).getByRole("region", { name: "Yesterday" }))).toEqual([`${span(local(-1, 10), local(-1, 11))} | Dentist · Main Street 12 | Home`]);
});

it("warns about a calendar that can't be read and lists the others", async () => {
  const calendars = [listed.calendars[0], { name: "Work", error: "calendar.google.com answered 404 Not Found" }];

  await show(() => Response.json({ ...listed, calendars, events: listed.events.filter((e) => e.calendar === "Home") }));

  const notice = screen.getByText("Work can't be read").closest('[role="status"]')!;
  expect(notice.textContent).toBe("Work can't be readcalendar.google.com answered 404 Not Found");
  expect(screen.getByRole("region", { name: "Today" })).toBeTruthy();
});

it("says nothing is coming up when no event is left", async () => {
  await show(() => Response.json({ ...listed, events: [] }));

  expect(screen.getByText("Nothing coming up")).toBeTruthy();
  expect(screen.getByText("No event in the next 14 days.")).toBeTruthy();
  expect(screen.queryByRole("region", { name: "Just past" })).toBeNull();
});

it("links to Plugins while no calendar is set up", async () => {
  await show(() => Response.json({ from: now.toISOString(), to: now.toISOString() }));

  const notice = screen.getByRole("status");
  expect(within(notice).getByText("No calendar is set up")).toBeTruthy();
  expect(within(notice).getByRole("link", { name: "Configure it in Plugins" }).getAttribute("href")).toBe("/plugins");
});

it("says why when calendar-api can't list the events", async () => {
  await show(() => unavailable("calendar-api is down"));

  expect(screen.getByRole("alert").textContent).toContain("calendar-api is down");
});

it("draws a skeleton until calendar-api first answers", async () => {
  await show(pending);

  expect(screen.getAllByRole("status", { name: "Loading" }).length).toBeGreaterThan(0);
});
