import { cleanup, screen, within } from "@testing-library/react";
import { pending, showWidget, unavailable, type Answer } from "@ddash/plugin/testing";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { clock } from "./events";
import plugin from "./index";
import { allDayEvent, calendar, listed, local, now, stopClock, timedEvent } from "./testing";

beforeEach(stopClock);
afterEach(() => {
  cleanup();
  vi.useRealTimers();
});

const nextEvents = () => plugin.widgets!.find((w) => w.id === "next-events")!;

// Renders the Widget's body at size, against calendar-api answering with
// answer, and returns what its useStatus reported last.
async function show(answer: Answer, size?: "S" | "M") {
  const { reported } = await showWidget(calendar, nextEvents(), { size, answers: { ListEvents: answer } });
  return reported;
}

const widget = () => screen.getByRole("region", { name: "widget" });

/** Each listed event as "title | when", with its meta when shown. */
const items = () =>
  within(widget())
    .getAllByRole("listitem")
    .map((li) => {
      const dot = within(li).getByRole("img").getAttribute("aria-label");
      return `${dot} ${li.textContent}`;
    });

it("is the Next events Widget, S or M, S by default", () => {
  const w = nextEvents();
  expect([w.title, w.sizes, w.defaultSize]).toEqual(["Next events", ["S", "M"], "S"]);
});

it("reports up with how many events are left today", async () => {
  const reported = await show(() => Response.json(listed));

  // The bins and the training: the stand-up is over.
  expect(reported.status).toEqual({ tone: "up", summary: "2 today" });
});

it("shows the next three events at S, today's highlighted", async () => {
  await show(() => Response.json(listed));

  const tomorrow = `Tomorrow ${clock(local(1, 19, 30))}`;
  expect(items()).toEqual([
    "info Put the bins outToday",
    `info Football training${clock(local(0, 18))}`,
    `idle Dinner with friends${tomorrow}`,
  ]);
});

it("shows the next five at M, with each one's calendar and place", async () => {
  await show(() => Response.json(listed), "M");

  const lines = items();
  expect(lines).toHaveLength(5);
  expect(lines[1]).toBe(`info Football training${clock(local(0, 18))}Home · Sports ground`);
  expect(lines[4]).toContain("Team offsite");
  expect(lines[4]).toContain("Work · Lakeside hotel");
});

it("says until when an event that has begun runs", async () => {
  await show(() => Response.json({ ...listed, events: [timedEvent("Work", "Workshop", local(0, 11), local(0, 13))] }));

  expect(items()).toEqual([`info Workshopuntil ${clock(local(0, 13))}`]);
});

it("reports idle when nothing is left today", async () => {
  const reported = await show(() => Response.json({ ...listed, events: [allDayEvent("Home", "Holiday", 3, 4)] }));

  expect(reported.status).toEqual({ tone: "idle", summary: "Nothing today" });
});

it("reports warn, naming it, when a calendar can't be read", async () => {
  const calendars = [listed.calendars[0], { name: "Work", error: "calendar.google.com answered 404 Not Found" }];

  const reported = await show(() => Response.json({ ...listed, calendars }));

  expect(reported.status).toEqual({ tone: "warn", summary: "Work unreadable · 2 today" });
});

it("reports idle and links to Plugins while no calendar is set up", async () => {
  const reported = await show(() => Response.json({ from: now.toISOString(), to: now.toISOString() }));

  expect(reported.status).toEqual({ tone: "idle", summary: "No calendar set up" });
  expect(within(widget()).getByRole("link", { name: "Add one in Plugins" }).getAttribute("href")).toBe("/plugins");
});

it("says so when nothing is coming up", async () => {
  await show(() => Response.json({ ...listed, events: [] }));

  expect(within(widget()).getByText("Nothing coming up.")).toBeTruthy();
});

it("reports warn when calendar-api is unavailable", async () => {
  const reported = await show(() => unavailable("calendar-api is down"));

  expect(reported.status).toEqual({ tone: "warn", summary: "calendar-api unavailable: calendar-api is down" });
});

it("reports nothing until calendar-api first answers", async () => {
  const reported = await show(pending);

  expect(reported.status).toBeUndefined();
});
