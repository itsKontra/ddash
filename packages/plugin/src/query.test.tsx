import { act, cleanup, render } from "@testing-library/react";
import type { DescMethodUnary } from "@bufbuild/protobuf";
import type { ReactNode } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { createQueryClient, PluginHost, PluginScope, useClient, useQuery } from "./index";
import { FakeService } from "./testing/fake-service";

// The fake host answers every call with {n: <calls so far>}, or with the
// next queued failure.
let calls: { url: string; headers: Headers; body: string }[];
let failures: number[];

const fetchFake = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
  calls.push({ url: String(input), headers: new Headers(init?.headers), body: new TextDecoder().decode(init?.body as Uint8Array) });
  const status = failures.shift();
  if (status) {
    return new Response(JSON.stringify({ code: "unavailable", message: "fake is down" }), {
      status,
      headers: { "Content-Type": "application/json" },
    });
  }
  return new Response(JSON.stringify({ n: calls.length }), { headers: { "Content-Type": "application/json" } });
});

beforeEach(() => {
  vi.useFakeTimers();
  calls = [];
  failures = [];
  setVisibility("visible");
});

afterEach(() => {
  cleanup();
  vi.useRealTimers();
});

function setVisibility(state: DocumentVisibilityState) {
  Object.defineProperty(document, "visibilityState", { value: state, configurable: true });
  Object.defineProperty(document, "hidden", { value: state === "hidden", configurable: true });
}

type Seen = { n?: number; error?: string };

// Probe renders what one useQuery hook sees into seen.
function Probe({ method, seen }: { method: DescMethodUnary; seen: Seen }) {
  const q = useQuery(method, { host: "media" });
  seen.n = (q.data as { n?: number } | undefined)?.n;
  seen.error = q.error?.message;
  return null;
}

// mount renders children as the docker Plugin's, which requires "fake".
function mount(children: ReactNode) {
  const client = createQueryClient();
  render(
    <PluginHost client={client}>
      <PluginScope plugin={{ name: "docker", requires: ["fake"] }} fetch={fetchFake}>
        {children}
      </PluginScope>
    </PluginHost>,
  );
}

// settle lets due calls answer and the hooks re-render, which takes
// TanStack Query a few rounds of short timers, advancing the clock by 5 ms.
const settle = async () => {
  for (let i = 0; i < 5; i++) await act(() => vi.advanceTimersByTimeAsync(1));
};

// wait advances the clock by ms, then lets the calls due by then answer.
const wait = async (ms: number) => {
  await act(() => vi.advanceTimersByTimeAsync(ms));
  await settle();
};

describe("useQuery", () => {
  it.each([
    ["LIVE", FakeService.method.live, 5_000],
    ["FAST", FakeService.method.fast, 15_000],
    ["SLOW", FakeService.method.slow, 60_000],
  ])("refetches a %s getter on its tier", async (_, method, interval) => {
    mount(<Probe method={method} seen={{}} />);
    await settle();
    expect(calls).toHaveLength(1);

    await wait(interval - 100);
    expect(calls).toHaveLength(1);
    await wait(100);
    expect(calls).toHaveLength(2);
    await wait(interval);
    expect(calls).toHaveLength(3);
  });

  it("fetches a NONE getter on mount only", async () => {
    mount(<Probe method={FakeService.method.none} seen={{}} />);
    await settle();

    await wait(10 * 60_000);

    expect(calls).toHaveLength(1);
  });

  it("calls the Plugin's Component as Connect JSON, naming the Plugin", async () => {
    const seen: Seen = {};
    mount(<Probe method={FakeService.method.fast} seen={seen} />);
    await settle();

    expect(calls[0].url).toBe("/api/c/fake/ddash.fake.FakeService/Fast");
    expect(calls[0].headers.get("Ddash-Plugin")).toBe("docker");
    expect(calls[0].headers.get("Connect-Protocol-Version")).toBe("1");
    expect(calls[0].headers.get("Content-Type")).toBe("application/json");
    expect(JSON.parse(calls[0].body)).toEqual({ host: "media" });
    expect(seen.n).toBe(1);
  });

  it("dedupes hooks with the same getter and request into one call per tick", async () => {
    const a: Seen = {};
    const b: Seen = {};
    mount(
      <>
        <Probe method={FakeService.method.fast} seen={a} />
        <Probe method={FakeService.method.fast} seen={b} />
      </>,
    );
    await settle();
    expect(calls).toHaveLength(1);

    await wait(15_000);

    expect(calls).toHaveLength(2);
    expect([a.n, b.n]).toEqual([2, 2]);
  });

  it("pauses while the tab is hidden and refetches when it shows", async () => {
    mount(<Probe method={FakeService.method.fast} seen={{}} />);
    await settle();

    setVisibility("hidden");
    window.dispatchEvent(new Event("visibilitychange"));
    await wait(60_000);
    expect(calls).toHaveLength(1);

    setVisibility("visible");
    window.dispatchEvent(new Event("visibilitychange"));
    await settle();
    expect(calls).toHaveLength(2);
  });

  it("refetches when the browser reconnects", async () => {
    mount(<Probe method={FakeService.method.slow} seen={{}} />);
    await settle();

    window.dispatchEvent(new Event("offline"));
    await wait(1_000);
    window.dispatchEvent(new Event("online"));
    await settle();

    expect(calls).toHaveLength(2);
  });

  it("doesn't retry a failed call and keeps the last good data next to the error", async () => {
    const seen: Seen = {};
    mount(<Probe method={FakeService.method.fast} seen={seen} />);
    await settle();
    expect(seen.n).toBe(1);

    failures.push(503);
    await wait(15_000);
    expect(calls).toHaveLength(2);
    expect(seen).toEqual({ n: 1, error: "[unavailable] fake is down" });

    await wait(14_000);
    expect(calls).toHaveLength(2);
    await wait(1_000);
    expect(seen).toEqual({ n: 3, error: undefined });
  });

  it("lets useClient call on the Plugin's transport", async () => {
    let answer: Promise<unknown> | undefined;
    function Caller() {
      // The fake service is built at runtime, so its client is untyped.
      const client = useClient(FakeService.service) as unknown as Record<string, (req: object) => Promise<unknown>>;
      answer ??= client.poke({ host: "media" });
      return null;
    }
    mount(<Caller />);
    await settle();

    await expect(answer).resolves.toMatchObject({ n: 1 });
    expect(calls[0].url).toBe("/api/c/fake/ddash.fake.FakeService/Poke");
    expect(calls[0].headers.get("Ddash-Plugin")).toBe("docker");
  });

  it("refuses an Action", () => {
    const quiet = vi.spyOn(console, "error").mockImplementation(() => {});
    expect(() => mount(<Probe method={FakeService.method.poke} seen={{}} />)).toThrow(/is an Action, not a getter/);
    quiet.mockRestore();
  });

  it("refuses a getter of a Component the Plugin doesn't require", async () => {
    const seen: Seen = {};
    const client = createQueryClient();
    render(
      <PluginHost client={client}>
        <PluginScope plugin={{ name: "docker", requires: ["docker-api"] }} fetch={fetchFake}>
          <Probe method={FakeService.method.fast} seen={seen} />
        </PluginScope>
      </PluginHost>,
    );
    await settle();

    expect(calls).toHaveLength(0);
    expect(seen.error).toBe("[permission_denied] plugin docker doesn't require a Component serving ddash.fake.FakeService");
  });
});
