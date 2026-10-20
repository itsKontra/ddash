import { act, cleanup, render } from "@testing-library/react";
import type { ReactNode } from "react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { createQueryClient, PluginHost, PluginScope, useTail } from "./index";
import { FakeService } from "./testing/fake-service";

// The fake host answers every call with {n: <calls so far>}, or with the
// next queued failure.
let calls: { host?: string }[];
let failures: number[];

const fetchFake = vi.fn(async (_input: RequestInfo | URL, init?: RequestInit) => {
  calls.push(JSON.parse(new TextDecoder().decode(init?.body as Uint8Array)));
  const status = failures.shift();
  if (status) return Response.json({ code: "unavailable", message: "fake is down" }, { status });
  return Response.json({ n: calls.length });
});

beforeEach(() => {
  vi.useFakeTimers();
  calls = [];
  failures = [];
});

afterEach(() => {
  cleanup();
  vi.useRealTimers();
});

const settle = async () => {
  for (let i = 0; i < 5; i++) await act(() => vi.advanceTimersByTimeAsync(1));
};

const wait = async (ms: number) => {
  await act(() => vi.advanceTimersByTimeAsync(ms));
  await settle();
};

type Seen = { log?: number[]; error?: string };

// Tail keeps every n the fake answered: each request says the last n it
// has (as host), the way GetThroughput asks for samples after the newest.
function Tail({ seen }: { seen: Seen }) {
  const q = useTail(FakeService.method.live, {
    request: (log: number[] | undefined) => ({ host: log ? String(log.at(-1)) : "" }),
    merge: (log: number[] | undefined, next) => [...(log ?? []), (next as unknown as { n: number }).n],
  });
  seen.log = q.data;
  seen.error = q.error?.message;
  return null;
}

function mount(children: ReactNode) {
  render(
    <PluginHost client={createQueryClient()}>
      <PluginScope plugin={{ name: "docker", requires: ["fake"] }} fetch={fetchFake}>
        {children}
      </PluginScope>
    </PluginHost>,
  );
}

it("asks for everything first, then only for what's new, once per tick of the getter's tier", async () => {
  const seen: Seen = {};
  mount(<Tail seen={seen} />);
  await settle();
  expect(calls).toEqual([{}]);
  expect(seen.log).toEqual([1]);

  await wait(4_900);
  expect(calls).toHaveLength(1);
  await wait(100);
  await wait(5_000);

  expect(calls).toEqual([{}, { host: "1" }, { host: "2" }]);
  expect(seen.log).toEqual([1, 2, 3]);
});

it("shares one tail between hooks", async () => {
  const a: Seen = {};
  const b: Seen = {};
  mount(
    <>
      <Tail seen={a} />
      <Tail seen={b} />
    </>,
  );
  await settle();
  await wait(5_000);

  expect(calls).toHaveLength(2);
  expect([a.log, b.log]).toEqual([
    [1, 2],
    [1, 2],
  ]);
});

it("keeps what it has next to an error, and carries on from it", async () => {
  const seen: Seen = {};
  mount(<Tail seen={seen} />);
  await settle();

  failures.push(503);
  await wait(5_000);
  expect(seen).toEqual({ log: [1], error: "[unavailable] fake is down" });

  await wait(5_000);
  expect(calls.at(-1)).toEqual({ host: "1" });
  expect(seen).toEqual({ log: [1, 3], error: undefined });
});
