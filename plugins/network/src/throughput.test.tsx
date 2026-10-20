import { act, cleanup, render, screen, within } from "@testing-library/react";
import { createQueryClient, PluginHost, PluginScope, type Status, type Widget } from "@ddash/plugin";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import plugin from "./index";

beforeEach(() => vi.useFakeTimers({ now: new Date("2026-10-20T08:00:00Z") }));

afterEach(() => {
  cleanup();
  vi.useRealTimers();
});

const settle = async () => {
  for (let i = 0; i < 5; i++) await act(() => vi.advanceTimersByTimeAsync(1));
};

// A sample (Connect JSON) at seconds past 07:00, in bytes per second.
const sample = (secs: number, wanIn: number, wanOut: number, lanIn = 300_000, lanOut = 2_400_000) => ({
  at: new Date(Date.UTC(2026, 9, 20, 7, 0, secs)).toISOString().replace(".000Z", "Z"),
  wanInBytesPerSecond: wanIn,
  wanOutBytesPerSecond: wanOut,
  lanInBytesPerSecond: lanIn,
  lanOutBytesPerSecond: lanOut,
});

const throughput = () => plugin.widgets!.find((w) => w.id === "throughput")!;

// Renders the Widget's body against network answering GetThroughput with
// each answer in turn (the last one repeating), and records the requests.
async function show(...answers: (() => Response)[]) {
  const w = throughput();
  const requests: { since?: string }[] = [];
  const fetch = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    expect(new URL(String(input), "http://ddash.test").pathname).toBe("/api/c/network/ddash.network.NetworkService/GetThroughput");
    requests.push(JSON.parse(new TextDecoder().decode(init?.body as Uint8Array)));
    return (answers.length > 1 ? answers.shift()! : answers[0])();
  });
  const reported: { status?: Status } = {};
  function Probe({ widget }: { widget: Widget }) {
    reported.status = widget.useStatus();
    return null;
  }
  render(
    <PluginHost client={createQueryClient()}>
      <PluginScope plugin={{ name: "network", requires: ["network", "pfsense-api"] }} fetch={fetch}>
        <Probe widget={w} />
        <section aria-label="widget">
          <w.Body size="M" />
        </section>
      </PluginScope>
    </PluginHost>,
  );
  await settle();
  return { reported, requests };
}

it("is the throughput Widget, M only", () => {
  const w = throughput();
  expect([w.title, w.sizes, w.defaultSize]).toEqual(["Throughput", ["M"], "M"]);
});

it("fetches the whole hour once, then only the samples after the newest it has", async () => {
  const hour = [sample(0, 1_000_000, 100_000), sample(5, 2_000_000, 200_000)];
  const { requests } = await show(
    () => Response.json({ samples: hour }),
    () => Response.json({ samples: [sample(10, 2_500_000, 250_000)] }),
    () => Response.json({ samples: [] }),
  );
  expect(requests).toEqual([{}]);

  await act(() => vi.advanceTimersByTimeAsync(5_000));
  await settle();
  await act(() => vi.advanceTimersByTimeAsync(5_000));
  await settle();

  expect(requests).toEqual([{}, { since: hour[1].at }, { since: sample(10, 0, 0).at }]);
  const body = screen.getByRole("region", { name: "widget" });
  const wan = within(body).getByRole("listitem", { name: "WAN" });
  expect(wan.textContent).toMatch(/↓ 20 Mbit\/s.*↑ 2 Mbit\/s/);
  const line = within(wan).getByRole("img", { name: "WAN over the last hour" });
  expect(line.querySelectorAll("polyline")[0].getAttribute("points")?.split(" ")).toHaveLength(3);
});

it("shows LAN download as what it sends to the home network", async () => {
  await show(() => Response.json({ samples: [sample(5, 2_500_000, 250_000, 300_000, 2_400_000)] }));

  const lan = within(screen.getByRole("region", { name: "widget" })).getByRole("listitem", { name: "LAN" });
  expect(lan.textContent).toMatch(/↓ 19 Mbit\/s.*↑ 2.4 Mbit\/s/);
});

it("keeps only the last hour", async () => {
  const old = sample(0, 9_000_000, 9_000_000);
  const { reported } = await show(
    () => Response.json({ samples: [old, sample(5, 1_000_000, 100_000)] }),
    () => Response.json({ samples: [{ ...sample(5, 2_500_000, 250_000), at: "2026-10-20T08:00:03Z" }] }),
  );

  await act(() => vi.advanceTimersByTimeAsync(5_000));
  await settle();

  const wan = within(screen.getByRole("region", { name: "widget" })).getByRole("listitem", { name: "WAN" });
  // 07:00:00 is over an hour before 08:00:03: only the two newer samples remain.
  expect(within(wan).getByRole("img", { name: "WAN over the last hour" }).querySelector("polyline")?.getAttribute("points")?.split(" ")).toHaveLength(2);
  expect(reported.status).toEqual({ tone: "up", summary: "WAN ↓ 20 Mbit/s ↑ 2 Mbit/s" });
});

it("reports up with the WAN rates", async () => {
  const { reported } = await show(() => Response.json({ samples: [sample(5, 2_500_000, 250_000)] }));

  expect(reported.status).toEqual({ tone: "up", summary: "WAN ↓ 20 Mbit/s ↑ 2 Mbit/s" });
});

it("reports up while the first samples are being taken", async () => {
  const { reported } = await show(() => Response.json({}));

  expect(reported.status).toEqual({ tone: "up", summary: "Collecting the first samples" });
  expect(screen.getByRole("region", { name: "widget" }).textContent).toMatch(/Collecting the first samples/);
});

it("reports warn when network can't read the counters", async () => {
  const { reported } = await show(() => Response.json({ samples: [sample(5, 1, 1)], error: "can't read the interface counters: pfSense unreachable" }));

  expect(reported.status).toEqual({ tone: "warn", summary: "Throughput unknown: can't read the interface counters: pfSense unreachable" });
});

it("reports warn when network is unavailable", async () => {
  const { reported } = await show(() => Response.json({ code: "unavailable", message: "network is down" }, { status: 503 }));

  expect(reported.status).toEqual({ tone: "warn", summary: "network unavailable: network is down" });
});
