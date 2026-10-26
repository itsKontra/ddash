import { Code, createContextValues } from "@connectrpc/connect";
import { createAsyncIterable } from "@connectrpc/connect/protocol";
import { describe, expect, it, vi } from "vitest";
import { streamed } from "./testing";
import { FakeService } from "./testing/fake-service";
import { confirmed, pluginTransport, type Fetch } from "./transport";

const docker = { name: "docker", requires: ["fake"] };

// read collects a stream's messages, or its error's message.
async function read(stream: Promise<{ message: AsyncIterable<unknown> }>) {
  const got: unknown[] = [];
  try {
    for await (const m of (await stream).message) got.push(m);
  } catch (err) {
    got.push((err as Error).message);
  }
  return got;
}

describe("pluginTransport's stream", () => {
  it("sends a server-streaming Action to its Component's base URL, naming the Plugin", async () => {
    const fetch = vi.fn<Fetch>(async () => streamed([{ n: 1 }, { n: 2 }]));
    const t = pluginTransport(docker, fetch);

    const got = await read(t.stream(FakeService.method.count, undefined, undefined, undefined, createAsyncIterable([{ host: "media" }])));

    expect(got).toMatchObject([{ n: 1 }, { n: 2 }]);
    const [url, init] = fetch.mock.calls[0];
    expect(url).toBe("/api/c/fake/ddash.fake.FakeService/Count");
    const headers = new Headers(init?.headers);
    expect(headers.get("Ddash-Plugin")).toBe("docker");
    expect(headers.get("Content-Type")).toBe("application/connect+json");
  });

  it.each([
    ["client", FakeService.method.upload],
    ["bidirectional", FakeService.method.chat],
  ])("refuses a %s streaming method with Unimplemented, without a call", async (_, method) => {
    const fetch = vi.fn<Fetch>(async () => streamed([]));
    const t = pluginTransport(docker, fetch);

    // The fake's client-streaming methods are typed apart from server-streaming.
    const stream = t.stream(method as unknown as typeof FakeService.method.count, undefined, undefined, undefined, createAsyncIterable([{}]));

    await expect(stream).rejects.toMatchObject({ code: Code.Unimplemented });
    expect(fetch).not.toHaveBeenCalled();
  });

  it("refuses a disruptive stream without a confirm, and sends one with it", async () => {
    const fetch = vi.fn<Fetch>(async () => streamed([{ n: 1 }]));
    const t = pluginTransport(docker, fetch);

    const refused = await read(t.stream(FakeService.method.blast, undefined, undefined, undefined, createAsyncIterable([{}])));
    expect(refused).toEqual([expect.stringMatching(/^\[failed_precondition\] ddash\.fake\.FakeService\.Blast is disruptive/)]);
    expect(fetch).not.toHaveBeenCalled();

    const values = createContextValues().set(confirmed, "Blast it?");
    const sent = await read(t.stream(FakeService.method.blast, undefined, undefined, undefined, createAsyncIterable([{}]), values));
    expect(sent).toMatchObject([{ n: 1 }]);
    expect(fetch).toHaveBeenCalledOnce();
  });
});
