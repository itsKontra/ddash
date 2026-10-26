import { cleanup, screen } from "@testing-library/react";
import { MetricTile } from "@ddash/ui";
import { useEffect } from "react";
import { afterEach, expect, it } from "vitest";
import { useQuery } from "../query";
import { useClient } from "../scope";
import { FakeService } from "./fake-service";
import { metricValue, pending, renderScoped, settle, streamed, unavailable } from "./index";

afterEach(cleanup);

const fake = { name: "lab", requires: ["fake"] };

/** The fast getter's answer as a MetricTile, loading until it comes. */
function Tile() {
  const { data, error } = useQuery(FakeService.method.fast, { host: "media" });
  if (error) return <p>{error.rawMessage}</p>;
  return <MetricTile label="Pongs" value={(data as { n?: number } | undefined)?.n} />;
}

const later = (ms: number, answer: () => Response) => () => new Promise<Response>((r) => setTimeout(() => r(answer()), ms));

it("renderScoped answers by method name and records each call's method, path and body", async () => {
  const calls = renderScoped(fake, <Tile />, { answers: { Fast: () => Response.json({ n: 7 }) } });
  await settle();

  expect(calls).toEqual([{ method: "Fast", url: "/api/c/fake/ddash.fake.FakeService/Fast", body: { host: "media" } }]);
  expect(metricValue("Pongs")).toBe("7");
});

it("settle waits for an answer however long it takes to come", async () => {
  renderScoped(fake, <Tile />, { answers: { Fast: later(150, () => Response.json({ n: 3 })) } });
  await settle();

  expect(metricValue("Pongs")).toBe("3");
});

it("settle doesn't wait for a pending answer, which keeps the tile loading", async () => {
  renderScoped(fake, <Tile />, { answers: { Fast: pending } });
  await settle();

  expect(metricValue("Pongs")).toBeUndefined();
});

it("unavailable is the core's refusal while a Component is down", async () => {
  renderScoped(fake, <Tile />, { answers: { Fast: () => unavailable("fake is down") } });
  await settle();

  expect(screen.getByText("fake is down")).toBeTruthy();
});

it("streamed answers a streamed call message by message, and renderScoped records its body as JSON", async () => {
  const got: string[] = [];
  function Counter() {
    // The fake service is built at runtime, so its client is untyped.
    const client = useClient(FakeService.service) as unknown as Record<string, (req: object) => AsyncIterable<{ n: number }>>;
    useEffect(() => {
      void (async () => {
        try {
          for await (const m of client.count({ host: "media" })) got.push(`n=${m.n}`);
        } catch (err) {
          got.push((err as Error).message);
        }
      })();
    }, [client]);
    return null;
  }
  const calls = renderScoped(fake, <Counter />, {
    answers: { Count: () => streamed([{ n: 1 }, { n: 2 }], { error: { code: "failed_precondition", message: "the fake refused" } }) },
  });
  await settle();

  expect(calls).toEqual([{ method: "Count", url: "/api/c/fake/ddash.fake.FakeService/Count", body: { host: "media" } }]);
  expect(got).toEqual(["n=1", "n=2", "[failed_precondition] the fake refused"]);
});
