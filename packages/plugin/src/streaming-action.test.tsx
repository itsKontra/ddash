import { create, toBinary } from "@bufbuild/protobuf";
import { base64Encode } from "@bufbuild/protobuf/wire";
import { Code } from "@connectrpc/connect";
import { act, cleanup, fireEvent, screen } from "@testing-library/react";
import { useState } from "react";
import { afterEach, describe, expect, it } from "vitest";
import { useQuery, useStreamingAction, type StreamingAction } from "./index";
import { renderScoped, settle, streamed, type Answers } from "./testing";
import { FakeService } from "./testing/fake-service";

afterEach(cleanup);

const docker = { name: "docker", requires: ["fake"] };
type Method = typeof FakeService.method.count;
const Pong = FakeService.method.count.output;

// Streamer hands out its useStreamingAction for the test to run, and keeps
// each n its last showed, in order.
let action: StreamingAction<Method>;
let shown: (number | undefined)[];
function Streamer({ method = FakeService.method.count }: { method?: Method }) {
  action = useStreamingAction(method);
  const n = (action.last as { n?: number } | undefined)?.n;
  if (shown.at(-1) !== n) shown.push(n);
  return action.error ? <p>{action.error.message}</p> : null;
}

// Reader keeps a getter on screen, so a call to Slow is a refetch.
function Reader() {
  useQuery(FakeService.method.slow, { host: "media" });
  return null;
}

// mount renders a Reader and a Streamer, which a Close button unmounts.
function mount(answers: Answers, method?: Method) {
  shown = [undefined];
  function Page() {
    const [open, setOpen] = useState(true);
    return (
      <>
        <Reader />
        {open && <Streamer method={method} />}
        <button onClick={() => setOpen(false)}>Close</button>
      </>
    );
  }
  return renderScoped(docker, <Page />, { answers: { Slow: () => Response.json({}), ...answers } });
}

const names = (calls: { method: string }[]) => calls.map((c) => c.method);

describe("useStreamingAction", () => {
  it("shows each message as it comes, resolves to the last, and refetches every query", async () => {
    const calls = mount({ Count: () => streamed([{ n: 1 }, { n: 2 }, { n: 3 }]) });
    await settle();

    let done: Promise<unknown> | undefined;
    act(() => {
      done = action.run({ host: "media" });
    });
    await settle();

    await expect(done).resolves.toMatchObject({ n: 3 });
    expect(shown).toEqual([undefined, 1, 2, 3]);
    expect(action.pending).toBe(false);
    expect(action.error).toBeUndefined();
    expect(names(calls)).toEqual(["Slow", "Count", "Slow"]);
    expect(calls[1]).toEqual({ method: "Count", url: "/api/c/fake/ddash.fake.FakeService/Count", body: { host: "media" } });
  });

  it("keeps the last message and sets the error, with its details, when the stream ends in one", async () => {
    const detail = { type: Pong.typeName, value: base64Encode(toBinary(Pong, create(Pong, { n: 9 })), "std_raw") };
    const calls = mount({
      Count: () => streamed([{ n: 1 }, { n: 2 }], { error: { code: "failed_precondition", message: "stopped at a step", details: [detail] } }),
    });
    await settle();

    let res: unknown = "unset";
    await act(async () => {
      res = await action.run({ host: "media" });
    });
    await settle();

    expect(res).toBeUndefined();
    expect(action.last).toMatchObject({ n: 2 });
    expect(action.error?.code).toBe(Code.FailedPrecondition);
    expect(action.error?.findDetails(Pong)).toMatchObject([{ n: 9 }]);
    expect(screen.getByText("[failed_precondition] stopped at a step")).toBeTruthy();
    expect(names(calls)).toEqual(["Slow", "Count"]);
  });

  it("clears the last message and the error when the next run starts", async () => {
    let answer = () => streamed([{ n: 1 }], { error: { code: "unavailable", message: "fake is down" } });
    mount({ Count: () => answer() });
    await settle();
    await act(async () => {
      await action.run({ host: "media" });
    });
    expect(action.error).toBeDefined();

    answer = () => streamed([{ n: 7 }]);
    act(() => {
      void action.run({ host: "media" });
    });
    await settle();

    expect(shown).toEqual([undefined, 1, undefined, 7]);
    expect(action.error).toBeUndefined();
  });

  it("fails as internal when the stream ends without a message", async () => {
    const calls = mount({ Count: () => streamed([]) });
    await settle();

    let res: unknown = "unset";
    await act(async () => {
      res = await action.run({ host: "media" });
    });
    await settle();

    expect(res).toBeUndefined();
    expect(action.error?.code).toBe(Code.Internal);
    expect(screen.getByText("[internal] Count ended without a result")).toBeTruthy();
    expect(names(calls)).toEqual(["Slow", "Count"]);
  });

  it("aborts the stream when unmounted mid-run, resolving to undefined and refetching every query", async () => {
    let signal: AbortSignal | null | undefined;
    const calls = mount({
      Count: (_, init) => {
        signal = init?.signal;
        return streamed([{ n: 1 }], { openUntil: init?.signal });
      },
    });
    await settle();

    let done: Promise<unknown> | undefined;
    act(() => {
      done = action.run({ host: "media" });
    });
    await settle();
    expect(shown).toEqual([undefined, 1]);
    expect(action.pending).toBe(true);
    expect(signal?.aborted).toBe(false);

    fireEvent.click(screen.getByRole("button", { name: "Close" }));
    await settle();

    expect(signal?.aborted).toBe(true);
    await expect(done).resolves.toBeUndefined();
    expect(names(calls)).toEqual(["Slow", "Count", "Slow"]);
    expect(screen.queryByText(/canceled/)).toBeNull();
  });

  describe("a disruptive streaming Action", () => {
    it("asks first, with the confirm text, and streams once confirmed", async () => {
      const calls = mount({ Blast: () => streamed([{ n: 1 }]) }, FakeService.method.blast);
      await settle();

      let done: Promise<unknown> | undefined;
      act(() => {
        done = action.run({ host: "media" }, { confirm: "Blast media?" });
      });
      await settle();
      expect(screen.getByRole("alertdialog").textContent).toContain("Blast media?");
      expect(names(calls)).toEqual(["Slow"]);

      fireEvent.click(screen.getByRole("button", { name: "Confirm" }));
      await settle();

      await expect(done).resolves.toMatchObject({ n: 1 });
      expect(names(calls)).toEqual(["Slow", "Blast", "Slow"]);
    });

    it("makes no call when the Operator declines", async () => {
      const calls = mount({ Blast: () => streamed([{ n: 1 }]) }, FakeService.method.blast);
      await settle();

      let done: Promise<unknown> | undefined;
      act(() => {
        done = action.run({ host: "media" }, { confirm: "Blast media?" });
      });
      await settle();
      fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
      await settle();

      await expect(done).resolves.toBeUndefined();
      expect(names(calls)).toEqual(["Slow"]);
      expect(action.error).toBeUndefined();
    });

    it("is refused by the transport without a confirm", async () => {
      const calls = mount({ Blast: () => streamed([{ n: 1 }]) }, FakeService.method.blast);
      await settle();

      let res: unknown = "unset";
      await act(async () => {
        res = await action.run({ host: "media" });
      });

      expect(res).toBeUndefined();
      expect(names(calls)).toEqual(["Slow"]);
      expect(screen.queryByRole("alertdialog")).toBeNull();
      expect(screen.getByText(/ddash\.fake\.FakeService\.Blast is disruptive: run it with a confirm text through useStreamingAction/)).toBeTruthy();
    });
  });
});
