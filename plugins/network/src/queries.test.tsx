import { cleanup, fireEvent, screen, within } from "@testing-library/react";
import { settle, unavailable, type Answers } from "@ddash/plugin/testing";
import { afterEach, expect, it } from "vitest";
import { show as showNetwork, withAdguard } from "./testing";

afterEach(cleanup);

const tv = {
  mac: "02:5e:00:00:10:01",
  name: "Living room TV",
  ip: "198.18.1.20",
  online: true,
  adopted: true,
  reservation: { interface: "lan", ip: "198.18.1.20", hostname: "living-room-tv", description: "Living room TV" },
};
const devices = { bypassAlias: "VPN_BYPASS", devices: [tv] };
const page = "/network/devices/02:5e:00:00:10:01";

const query = (domain: string, extra: Record<string, unknown> = {}) => ({
  time: new Date(Date.now() - 4 * 60_000).toISOString(),
  domain,
  type: "A",
  client: "198.18.1.20",
  reason: "NotFilteredNotFound",
  status: "NOERROR",
  ...extra,
});
const blockedQuery = (domain: string) => query(domain, { blocked: true, reason: "FilteredBlackList", rule: `||${domain}^` });

const recent = { queries: ["ads1.example", "ads2.example", "ads3.example", "ads4.example", "ads5.example"].map(blockedQuery), oldest: "c5" };
const log = {
  queries: [query("site1.example", { cached: true }), blockedQuery("ads1.example"), query("site3.example", { type: "AAAA", status: "NXDOMAIN" })],
  oldest: "",
};

type Request = { limit?: number; mode?: string; text?: string; type?: string; olderThan?: string };

// Shows the Device Page at path, answering the blocked-5 call with recent
// and the Sheet's calls with log unless given, and returns the calls.
const show = (path = page, answers: Answers = {}) =>
  showNetwork(
    path,
    {
      ListDevices: () => Response.json(devices),
      ListQueries: (b) => Response.json((b as Request).limit === 5 ? recent : log),
      ...answers,
    },
    withAdguard,
  );

const sheetCalls = (calls: { method: string; body: unknown }[]) =>
  calls.filter((c) => c.method === "ListQueries" && (c.body as Request).limit !== 5).map((c) => c.body);
const panel = () => screen.getByRole("region", { name: "Recently blocked" });
const sheet = () => screen.getByRole("dialog", { name: "198.18.1.20" });
const domains = () => within(within(sheet()).getByRole("table")).queryAllByRole("rowheader").map((r) => r.textContent);

it("lists the last 5 blocked queries, with their rules and how long ago", async () => {
  const calls = await show();

  expect(calls.filter((c) => c.method === "ListQueries").map((c) => c.body)).toEqual([{ client: "198.18.1.20", limit: 5, mode: "QUERY_MODE_BLOCKED" }]);
  expect(within(panel()).getByText("What AdGuard last blocked from 198.18.1.20.")).toBeTruthy();
  const items = within(panel()).getAllByRole("listitem");
  expect(items).toHaveLength(5);
  expect(items[0].textContent).toContain("ads1.example");
  expect(within(items[0]).getByText("||ads1.example^")).toBeTruthy();
  expect(within(items[0]).getByText("4 min ago")).toBeTruthy();
  expect(screen.queryByRole("dialog")).toBeNull();
});

it("says when nothing from the Device is blocked", async () => {
  await show(page, { ListQueries: () => Response.json({ queries: [] }) });

  expect(within(panel()).getByText("Nothing from 198.18.1.20 is blocked in AdGuard's log.")).toBeTruthy();
});

it("opens the full log in a Sheet from All queries, newest first", async () => {
  const calls = await show();

  fireEvent.click(within(panel()).getByRole("button", { name: "All queries" }));
  await settle();

  expect(sheetCalls(calls)).toEqual([{ client: "198.18.1.20", limit: 50 }]);
  expect(within(sheet()).getByText("Everything AdGuard logged from this Device, newest first.")).toBeTruthy();
  expect(domains()).toEqual(["site1.example", "ads1.example", "site3.example"]);
  const table = within(sheet()).getByRole("table");
  expect(within(within(table).getByRole("row", { name: /site1\.example/ })).getByText("NOERROR, cached")).toBeTruthy();
  const ads = within(table).getByRole("row", { name: /ads1\.example/ });
  expect(within(ads).getByText("Blocked")).toBeTruthy();
  expect(within(ads).getByText("||ads1.example^")).toBeTruthy();
  expect(within(ads).getByText(/^\d\d:\d\d:\d\d$/)).toBeTruthy();
});

it("opens the Sheet from the URL with its search, mode and type, and closing it clears them all", async () => {
  const calls = await show(`${page}?queries.open=1&queries.q=ads&queries.mode=blocked&queries.type=AAAA&queries.sort=domain&tab=x`);

  expect(sheetCalls(calls)).toEqual([{ client: "198.18.1.20", limit: 50, mode: "QUERY_MODE_BLOCKED", text: "ads", type: "AAAA" }]);
  expect((within(sheet()).getByRole("searchbox", { name: "Search queries" }) as HTMLInputElement).value).toBe("ads");
  expect(within(sheet()).getByRole("tab", { name: "Blocked" }).getAttribute("aria-selected")).toBe("true");
  expect((within(sheet()).getByRole("combobox", { name: "Type" }) as HTMLSelectElement).value).toBe("AAAA");
  expect(domains()).toEqual(["ads1.example", "site1.example", "site3.example"]);

  fireEvent.click(within(sheet()).getByRole("button", { name: "Close" }));
  await settle();
  expect(screen.queryByRole("dialog")).toBeNull();

  fireEvent.click(within(panel()).getByRole("button", { name: "All queries" }));
  await settle();
  // Reopened, it starts over: all queries, any type, no search.
  expect(sheetCalls(calls).at(-1)).toEqual({ client: "198.18.1.20", limit: 50 });
  expect((within(sheet()).getByRole("searchbox", { name: "Search queries" }) as HTMLInputElement).value).toBe("");
  expect(within(sheet()).getByRole("tab", { name: "All" }).getAttribute("aria-selected")).toBe("true");
  expect(domains()).toEqual(["site1.example", "ads1.example", "site3.example"]);
});

it("asks AdGuard for 50 blocked or allowed queries when a Tab picks them, without a budget", async () => {
  const calls = await show(`${page}?queries.open=1`);

  fireEvent.click(within(sheet()).getByRole("tab", { name: "Allowed" }));
  await settle();

  expect(sheetCalls(calls).at(-1)).toEqual({ client: "198.18.1.20", limit: 50, mode: "QUERY_MODE_ALLOWED" });
  expect(within(sheet()).queryByText(/of 50 found/)).toBeNull();
});

it("says how far it reached once 50 match", async () => {
  const fifty = { queries: Array.from({ length: 50 }, (_, i) => query(`site${i}.example`)), oldest: "c50" };
  await show(`${page}?queries.open=1`, { ListQueries: (b) => Response.json((b as Request).limit === 5 ? recent : fifty) });

  expect(domains()).toHaveLength(50);
  expect(within(sheet()).getByText(/^The newest 50 matches, back to \d\d:\d\d:\d\d\. Search or filter to reach older ones\.$/)).toBeTruthy();
});

it("says when the scan budget ran out first, and searches 2,000 older from where it stopped", async () => {
  const windows: Record<string, unknown> = {
    "": { queries: [blockedQuery("ads1.example")], oldest: "c2000", linesRead: 2000, reached: "2026-10-20T08:15:00Z" },
    c2000: { queries: [blockedQuery("ads9.example"), blockedQuery("ads8.example")], oldest: "c4000", linesRead: 2000, reached: "2026-10-20T06:40:00Z" },
  };
  const calls = await show(`${page}?queries.open=1`, {
    ListQueries: (b) => Response.json((b as Request).limit === 5 ? recent : windows[(b as Request).olderThan ?? ""]),
  });

  fireEvent.change(within(sheet()).getByRole("searchbox", { name: "Search queries" }), { target: { value: "ads" } });
  await settle();

  expect(sheetCalls(calls).at(-1)).toEqual({ client: "198.18.1.20", limit: 50, text: "ads" });
  let notice = within(sheet()).getByText("1 of 50 found").closest<HTMLElement>("[role=status]")!;
  expect(notice.textContent).toMatch(/ddash searched the newest 2,000 log lines, back to \d\d:\d\d\./);
  expect(domains()).toEqual(["ads1.example"]);

  fireEvent.click(within(notice).getByRole("button", { name: "Search 2,000 older" }));
  await settle();

  expect(sheetCalls(calls).at(-1)).toEqual({ client: "198.18.1.20", limit: 50, text: "ads", olderThan: "c2000" });
  notice = within(sheet()).getByText("2 of 50 found").closest<HTMLElement>("[role=status]")!;
  expect(notice.textContent).toMatch(/ddash searched the newest 4,000 log lines/);
  expect(domains()).toEqual(["ads9.example", "ads8.example"]);

  // A new filter starts from the newest again.
  fireEvent.change(within(sheet()).getByRole("combobox", { name: "Type" }), { target: { value: "AAAA" } });
  await settle();
  expect(sheetCalls(calls).at(-1)).toEqual({ client: "198.18.1.20", limit: 50, text: "ads", type: "AAAA" });
});

it("says what a search didn't find, and in how many log lines", async () => {
  await show(`${page}?queries.open=1&queries.mode=blocked&queries.type=AAAA&queries.q=tracker`, {
    ListQueries: (b) => Response.json((b as Request).limit === 5 ? recent : { queries: [], linesRead: 1234 }),
  });

  expect(within(sheet()).getByText("No blocked AAAA queries match “tracker” in the 1,234 log lines searched.")).toBeTruthy();
  // The log ended before the budget did.
  expect(within(sheet()).queryByText(/of 50 found/)).toBeNull();
});

it("says when the Device has no queries, or no blocked ones", async () => {
  await show(`${page}?queries.open=1`, { ListQueries: () => Response.json({ queries: [] }) });

  expect(within(sheet()).getByText("No queries from 198.18.1.20 in AdGuard's log.")).toBeTruthy();
  fireEvent.click(within(sheet()).getByRole("tab", { name: "Blocked" }));
  await settle();
  expect(within(sheet()).getByText("No blocked queries from 198.18.1.20 in AdGuard's log.")).toBeTruthy();
});

it("says it can't read AdGuard's query log, in the Panel and the Sheet", async () => {
  await show(`${page}?queries.open=1`, { ListQueries: () => unavailable("AdGuard Home unreachable: connection refused") });

  for (const view of [panel(), sheet()]) {
    const alert = within(view).getByRole("alert");
    expect(alert.textContent).toContain("Can't read AdGuard's query log");
    expect(alert.textContent).toContain("AdGuard Home unreachable");
  }
});

it("says AdGuard hides client addresses, in both views and without the Sheet's toolbar", async () => {
  await show(`${page}?queries.open=1`, { ListQueries: () => Response.json({ queries: [], anonymizeClientIp: true }) });

  for (const view of [panel(), sheet()]) {
    expect(within(view).getByText("AdGuard hides client addresses")).toBeTruthy();
    expect(within(view).getByText(/can't tell 198\.18\.1\.20's queries apart/)).toBeTruthy();
  }
  expect(within(sheet()).queryByRole("searchbox")).toBeNull();
  expect(within(sheet()).queryByRole("tablist")).toBeNull();
  expect(within(sheet()).queryByRole("table")).toBeNull();
});
