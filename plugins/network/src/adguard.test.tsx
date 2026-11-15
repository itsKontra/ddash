import { cleanup, fireEvent, screen, within } from "@testing-library/react";
import { metricValue, renderScoped, settle, showWidget as show, unavailable, type Answers } from "@ddash/plugin/testing";
import { pauseLabel } from "./protection";
import { afterEach, expect, it } from "vitest";
import plugin from "./index";
import { withAdguard } from "./testing";

afterEach(cleanup);

// What adguard-api answers (Connect JSON: int64 as strings).
const on = { running: true, protectionEnabled: true, version: "v0.107.79" };
const stats = {
  dnsQueries: "18432",
  blockedFiltering: "2765",
  replacedSafebrowsing: "3",
  avgProcessingMs: 18.4,
  timeUnit: "hours",
  dnsQueriesSeries: ["612", "540", "498"],
  blockedFilteringSeries: ["92", "81", "75"],
  topBlockedDomains: [
    { name: "site2.example", count: "610" },
    { name: "site5.example", count: "402" },
  ],
  topClients: [{ name: "198.18.1.150", count: "6120" }],
};



const widget = () => plugin.widgets!.find((w) => w.id === "adguard")!;

const showWidget = (answers: Answers) => show(withAdguard, widget(), { answers });

it("is the adguard Widget, S only", () => {
  const w = widget();
  expect([w.title, w.sizes, w.defaultSize]).toEqual(["AdGuard", ["S"], "S"]);
});

it("is up while protection is on, with the share of the day's queries blocked", async () => {
  const { reported, calls } = await showWidget({ GetStatus: () => Response.json(on), GetStats: () => Response.json(stats) });

  const paths = calls.map((c) => c.url);
  expect(paths).toContain("/api/c/adguard-api/ddash.adguardapi.AdguardApiService/GetStatus");
  expect(reported.status).toEqual({ tone: "up", summary: "Protection on, 15% of 18,432 queries blocked" });
  const body = screen.getByRole("region", { name: "widget" });
  expect(metricValue("Queries, 24 h", body)).toMatch(/18,432/);
  expect(metricValue("Blocked", body)).toMatch(/15%/);
});

it("is warn while protection is off, and says how long a pause lasts", async () => {
  let { reported } = await showWidget({ GetStatus: () => Response.json({ ...on, protectionEnabled: false }) });
  expect(reported.status).toEqual({ tone: "warn", summary: "Protection off" });
  cleanup();

  ({ reported } = await showWidget({ GetStatus: () => Response.json({ ...on, protectionEnabled: false, protectionPausedFor: "600s" }) }));
  expect(reported.status).toEqual({ tone: "warn", summary: "Protection paused, back on in 10 min" });
});

it("is down when AdGuard's DNS server isn't running", async () => {
  const { reported } = await showWidget({ GetStatus: () => Response.json({ ...on, running: false }) });

  expect(reported.status).toEqual({ tone: "down", summary: "AdGuard isn't running" });
});

it("is warn when adguard-api is unavailable", async () => {
  const { reported } = await showWidget({
    GetStatus: () => unavailable("AdGuard Home unreachable: connection refused"),
  });

  expect(reported.status).toEqual({ tone: "warn", summary: "AdGuard unavailable: AdGuard Home unreachable: connection refused" });
});

async function showPage(answers: Answers) {
  const Page = plugin.pages![0].Component!;
  renderScoped(withAdguard, <Page />, { answers });
  await settle();
}

it("shows AdGuard's protection and the last 24 h's stats on the Network Page", async () => {
  await showPage({ GetStatus: () => Response.json(on), GetStats: () => Response.json(stats) });

  const section = screen.getByRole("region", { name: "AdGuard" });
  expect(within(section).getByText("Protection on")).toBeTruthy();
  expect(metricValue("queries, 24 h", section)).toMatch(/18,432/);
  expect(metricValue("blocked by filters", section)).toMatch(/2,765/);
  expect(metricValue("avg processing", section)).toMatch(/18\.4 ms/);
  expect(within(section).getByRole("img", { name: "Queries and blocked, per hour" })).toBeTruthy();
  expect(within(section).getByRole("img", { name: "Most blocked domains" })).toBeTruthy();
  const top = within(section).getByRole("list", { name: "Most blocked domains legend" });
  expect(within(top).getAllByRole("listitem").map((li) => li.textContent)).toEqual(["site2.example610", "site5.example402"]);
});

it("says on the Network Page when AdGuard isn't running", async () => {
  await showPage({ GetStatus: () => Response.json({ ...on, running: false }), GetStats: () => Response.json(stats) });

  expect(within(screen.getByRole("region", { name: "AdGuard" })).getByText("Not running")).toBeTruthy();
});

const paused = { ...on, protectionEnabled: false, protectionPausedFor: "420s" };

it("pauses protection for ten minutes from the Widget's switch, at once and without a confirm", async () => {
  let status: object = on;
  const { calls } = await showWidget({
    GetStatus: () => Response.json(status),
    GetStats: () => Response.json(stats),
    PauseProtection: () => ((status = { ...on, protectionEnabled: false, protectionPausedFor: "600s" }), Response.json({})),
  });

  const body = screen.getByRole("region", { name: "widget" });
  const toggle = within(body).getByRole("switch", { name: "AdGuard protection" });
  expect(toggle.getAttribute("aria-checked")).toBe("true");
  fireEvent.click(toggle);
  await settle();

  expect(screen.queryByRole("alertdialog")).toBeNull();
  expect(calls.filter((c) => c.method === "PauseProtection").map((c) => c.body)).toEqual([{ duration: "600s" }]);
  expect(within(body).getByText("Protection paused, back on in 10 min")).toBeTruthy();
  expect(within(body).getByRole("switch", { name: "AdGuard protection" }).getAttribute("aria-checked")).toBe("false");
  expect(within(body).getByRole("button", { name: "Resume" })).toBeTruthy();
});

it("offers ten minutes and an hour beside the switch", async () => {
  const { calls } = await showWidget({ GetStatus: () => Response.json(on), GetStats: () => Response.json(stats), PauseProtection: () => Response.json({}) });

  fireEvent.click(screen.getByRole("button", { name: "Pause protection for 1 h" }));
  await settle();
  fireEvent.click(screen.getByRole("button", { name: "Pause protection for 10 min" }));
  await settle();

  expect(calls.filter((c) => c.method === "PauseProtection").map((c) => c.body)).toEqual([{ duration: "3600s" }, { duration: "600s" }]);
});

it("shows AdGuard's time left while paused, and resumes from Resume or the switch", async () => {
  const { reported, calls } = await showWidget({ GetStatus: () => Response.json(paused), GetStats: () => Response.json(stats), ResumeProtection: () => Response.json({}) });

  expect(reported.status).toEqual({ tone: "warn", summary: "Protection paused, back on in 7 min" });
  expect(screen.queryByRole("button", { name: /Pause protection/ })).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: "Resume" }));
  await settle();
  fireEvent.click(screen.getByRole("switch", { name: "AdGuard protection" }));
  await settle();

  expect(calls.filter((c) => c.method === "ResumeProtection")).toHaveLength(2);
});

it("says why a pause failed", async () => {
  await showWidget({
    GetStatus: () => Response.json(on),
    GetStats: () => Response.json(stats),
    PauseProtection: () => unavailable("AdGuard Home unreachable: connection refused"),
  });

  fireEvent.click(screen.getByRole("switch", { name: "AdGuard protection" }));
  await settle();

  expect(screen.getByText("AdGuard Home unreachable: connection refused")).toBeTruthy();
});

it("has no switch while AdGuard's DNS server isn't running", async () => {
  await showWidget({ GetStatus: () => Response.json({ ...on, running: false }) });

  expect(screen.queryByRole("switch")).toBeNull();
});

it("leads the Network Page's AdGuard section with the switch and the two pauses", async () => {
  await showPage({ GetStatus: () => Response.json(on), GetStats: () => Response.json(stats) });

  const section = screen.getByRole("region", { name: "AdGuard" });
  expect(within(section).getByRole("switch", { name: "AdGuard protection" })).toBeTruthy();
  expect(within(section).getAllByRole("button", { name: /Pause protection/ }).map((b) => b.textContent)).toEqual(["10 min", "1 h"]);
});

it("names a pause in minutes or hours", () => {
  expect([10, 60, 90, 120].map(pauseLabel)).toEqual(["10 min", "1 h", "90 min", "2 h"]);
});
