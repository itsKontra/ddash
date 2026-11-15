import type { Concern, Status } from "@ddash/plugin";
import { expect, it } from "vitest";
import type { Listed } from "./concerns";
import { hero, oldestRead, readAgo, said, type Reporting } from "./hero";

const widget = (plugin: string, title: string, status?: Status): Reporting => ({ plugin, title, status });
const up = (fact?: Status["fact"]): Status => ({ tone: "up", summary: "fine", fact });
const concern = (plugin: string, subject: string, tone: Concern["tone"], quiet?: string): Listed => ({
  plugin,
  concern: { id: subject, tone, subject, title: `${subject} is ${tone}`, quiet },
});

const placed = [
  widget("checks", "Checks", up({ value: 22, label: "of 22 Checks up" })),
  widget("proxmox", "Guests", up({ value: 12, label: "guests running" })),
  widget("calendar", "Next events", up()),
];

it("says everything watched is up when nothing is down or warn, with the Widgets' facts in grid order", () => {
  const h = hero(placed, [], false);

  expect(said(h.sentence)).toBe("Everything you watch is up.");
  expect(h.tone).toBe("up");
  expect(h.facts).toEqual([
    { value: 22, label: "of 22 Checks up" },
    { value: 12, label: "guests running" },
  ]);
  // A second Widget counting the same thing isn't said twice.
  expect(hero([...placed, widget("proxmox", "Hosts", up({ value: 12, label: "guests running" }))], [], false).facts).toHaveLength(2);
});

it("names the one thing down by its Concern's subject, the rest of the sentence quieter", () => {
  const h = hero(placed, [concern("checks", "Paperless", "down")], false);

  expect(h.sentence).toEqual([{ text: "Paperless is down." }, { text: "Everything else is up.", quiet: true }]);
  expect(h.tone).toBe("down");
});

it("names up to three things down and counts the rest, each once", () => {
  const two = hero(placed, [concern("checks", "Paperless", "down"), concern("apps", "Paperless", "down"), concern("proxmox", "nas", "down")], false);
  expect(said(two.sentence)).toBe("Paperless and nas are down. Everything else is up.");

  const five = hero(
    placed,
    ["a", "b", "c", "d", "e"].map((s) => concern("checks", s, "down")),
    false,
  );
  expect(said(five.sentence)).toBe("a, b, c and 2 more are down. Everything else is up.");
});

it("with only warnings says everything is up and counts what is getting tight in words up to twelve", () => {
  const one = hero(placed, [concern("network", "nas", "warn")], false);
  expect(one.sentence).toEqual([{ text: "Everything is up.", quiet: true }, { text: "One thing is getting tight." }]);
  expect(one.tone).toBe("warn");

  const three = hero(placed, ["a", "b", "c"].map((s) => concern("network", s, "warn")), false);
  expect(said(three.sentence)).toBe("Everything is up. Three things are getting tight.");

  const many = hero(placed, Array.from({ length: 13 }, (_, i) => concern("network", `d${i}`, "warn")), false);
  expect(said(many.sentence)).toBe("Everything is up. 13 things are getting tight.");
});

it("leaves quiet Concerns out of the sentence and the tone", () => {
  const h = hero(placed, [concern("checks", "backup", "down", "muted"), concern("checks", "nvr", "down", "in Maintenance")], false);

  expect(said(h.sentence)).toBe("Everything you watch is up.");
  expect(h.tone).toBe("up");
});

it("leaves news, Maintenance and unknown out of the sentence", () => {
  const h = hero(
    [widget("lab", "Alpha", { tone: "maint", summary: "patching" }), widget("lab", "Bravo", { tone: "idle", summary: "?" })],
    [concern("network", "New Device", "info")],
    false,
  );

  expect(said(h.sentence)).toBe("Everything you watch is up.");
  expect(h.tone).toBe("up");
});

it("names a Widget that is down without a Concern behind it by its title, so nothing red is hidden", () => {
  const h = hero([...placed, widget("docker", "Containers", { tone: "down", summary: "docker-api gone" })], [], false);

  expect(said(h.sentence)).toBe("Containers is down. Everything else is up.");
  expect(h.tone).toBe("down");
});

it("doesn't name a Widget's trouble twice when its Plugin's loud Concerns already cover it", () => {
  const h = hero(
    [widget("checks", "Checks", { tone: "down", summary: "1 down: Paperless" }), widget("network", "VPN", { tone: "warn", summary: "on the fallback" })],
    [concern("checks", "Paperless", "down")],
    false,
  );

  // The Checks Widget is covered; VPN, at warn without a Concern, isn't named but makes no difference to a down sentence.
  expect(said(h.sentence)).toBe("Paperless is down. Everything else is up.");

  // A quiet Concern covers nothing: the Widget still counts.
  const quiet = hero([widget("checks", "Checks", { tone: "down", summary: "x" })], [concern("checks", "backup", "down", "muted")], false);
  expect(said(quiet.sentence)).toBe("Checks is down. Everything else is up.");

  // Nor does a warn Concern cover a Widget that is down.
  const milder = hero([widget("docker", "Containers", { tone: "down", summary: "x" })], [concern("docker", "media", "warn")], false);
  expect(said(milder.sentence)).toBe("Containers is down. Everything else is up.");
});

it("says plainly that ddash can't be reached, at warn unless something is down, and keeps the facts", () => {
  const h = hero(placed, [], true);
  expect(h.sentence).toEqual([{ text: "ddash can't be reached." }, { text: "What you see may be out of date.", quiet: true }]);
  expect(h.tone).toBe("warn");
  expect(h.facts).toHaveLength(2);

  expect(hero(placed, [concern("checks", "Paperless", "down")], true).tone).toBe("down");
});

it("waits while nothing has reported, and says when nothing is on the Overview", () => {
  const waiting = hero([widget("checks", "Checks")], undefined, false);
  expect(said(waiting.sentence)).toBe("Waiting for the Widgets to report.");
  expect(waiting.tone).toBe("idle");

  expect(said(hero([], undefined, false).sentence)).toBe("No Widgets on the Overview.");
  // Concerns alone are enough to speak about the network.
  expect(said(hero([], [], false).sentence)).toBe("Everything you watch is up.");
});

it("never says Widget while one reports", () => {
  const cases = [
    hero(placed, [], false),
    hero(placed, [concern("checks", "Paperless", "down")], false),
    hero(placed, [concern("network", "nas", "warn")], false),
    hero([widget("lab", "Alpha", { tone: "warn", summary: "x" })], undefined, false),
  ];
  for (const h of cases) expect(said(h.sentence)).not.toMatch(/Widget/);
});

it("reads the age of the oldest data in seconds, minutes or hours", () => {
  expect(readAgo(12_400)).toBe("read 12 s ago");
  expect(readAgo(-5)).toBe("read 0 s ago");
  expect(readAgo(3 * 60_000 + 5_000)).toBe("read 3 min ago");
  expect(readAgo(2 * 3_600_000)).toBe("read 2 h ago");
});

it("takes the oldest read among the queries that refetch and have data", () => {
  expect(
    oldestRead([
      { readAt: 1_000, refetches: false },
      { readAt: 0, refetches: true },
      { readAt: 5_000, refetches: true },
      { readAt: 3_000, refetches: true },
    ]),
  ).toBe(3_000);
  expect(oldestRead([{ readAt: 1_000, refetches: false }])).toBeUndefined();
});
