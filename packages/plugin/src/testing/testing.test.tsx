import { cleanup, screen } from "@testing-library/react";
import { MetricTile } from "@ddash/ui";
import { afterEach, expect, it } from "vitest";
import { useQuery } from "../query";
import { FakeService } from "./fake-service";
import { metricValue, pending, renderScoped, settle, unavailable } from "./index";

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
