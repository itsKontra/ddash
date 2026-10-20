import { act, cleanup, render, screen, within } from "@testing-library/react";
import { createQueryClient, PluginHost, PluginScope, type Status, type Widget } from "@ddash/plugin";
import { MemoryRouter } from "react-router";
import { afterEach, expect, it, vi } from "vitest";
import plugin from "./index";

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

type Answers = Record<string, () => Response>;

function fakeFetch(answers: Answers) {
  return vi.fn(async (input: RequestInfo | URL) => {
    const method = String(input).split("/").pop()!;
    return (answers[method] ?? (() => Response.json({})))();
  });
}

const settle = () => act(() => new Promise((r) => setTimeout(r, 50)));
const scope = { name: "network", requires: ["network", "pfsense-api", "adguard-api"] };

const widget = () => plugin.widgets!.find((w) => w.id === "adguard")!;

async function showWidget(answers: Answers) {
  const w = widget();
  const fetch = fakeFetch(answers);
  const reported: { status?: Status } = {};
  function Probe({ widget }: { widget: Widget }) {
    reported.status = widget.useStatus();
    return null;
  }
  render(
    <PluginHost client={createQueryClient()}>
      <PluginScope plugin={scope} fetch={fetch}>
        <Probe widget={w} />
        <section aria-label="widget">
          <w.Body size="S" />
        </section>
      </PluginScope>
    </PluginHost>,
  );
  await settle();
  return { reported, fetch };
}

it("is the adguard Widget, S only", () => {
  const w = widget();
  expect([w.title, w.sizes, w.defaultSize]).toEqual(["AdGuard", ["S"], "S"]);
});

it("is up while protection is on, with the share of the day's queries blocked", async () => {
  const { reported, fetch } = await showWidget({ GetStatus: () => Response.json(on), GetStats: () => Response.json(stats) });

  const paths = fetch.mock.calls.map((c) => new URL(String(c[0]), "http://ddash.test").pathname);
  expect(paths).toContain("/api/c/adguard-api/ddash.adguardapi.AdguardApiService/GetStatus");
  expect(reported.status).toEqual({ tone: "up", summary: "Protection on, 15% of 18,432 queries blocked" });
  const body = screen.getByRole("region", { name: "widget" });
  expect(within(body).getByText("queries, 24 h").parentElement?.textContent).toMatch(/18,432/);
  expect(within(body).getByText("blocked").parentElement?.textContent).toMatch(/15%/);
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
    GetStatus: () => Response.json({ code: "unavailable", message: "AdGuard Home unreachable: connection refused" }, { status: 503 }),
  });

  expect(reported.status).toEqual({ tone: "warn", summary: "AdGuard unavailable: AdGuard Home unreachable: connection refused" });
});

async function showPage(answers: Answers) {
  const Page = plugin.pages![0].Component!;
  render(
    <PluginHost client={createQueryClient()}>
      <PluginScope plugin={scope} fetch={fakeFetch(answers)}>
        <MemoryRouter>
          <Page />
        </MemoryRouter>
      </PluginScope>
    </PluginHost>,
  );
  await settle();
}

it("shows AdGuard's protection and the last 24 h's stats on the Network Page", async () => {
  await showPage({ GetStatus: () => Response.json(on), GetStats: () => Response.json(stats) });

  const section = screen.getByRole("region", { name: "AdGuard" });
  expect(within(section).getByText("Protection on")).toBeTruthy();
  expect(within(section).getByText("queries, 24 h").parentElement?.textContent).toMatch(/18,432/);
  expect(within(section).getByText("blocked by filters").parentElement?.textContent).toMatch(/2,765/);
  expect(within(section).getByText("avg processing").parentElement?.textContent).toMatch(/18\.4 ms/);
  expect(within(section).getByRole("img", { name: "Queries and blocked, per hour" })).toBeTruthy();
  const top = within(section).getByRole("list", { name: "Most blocked domains" });
  expect(within(top).getAllByRole("listitem").map((li) => li.textContent)).toEqual(["site2.example610", "site5.example402"]);
});

it("says on the Network Page when AdGuard isn't running", async () => {
  await showPage({ GetStatus: () => Response.json({ ...on, running: false }), GetStats: () => Response.json(stats) });

  expect(within(screen.getByRole("region", { name: "AdGuard" })).getByText("Not running")).toBeTruthy();
});
