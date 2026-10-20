import { act, cleanup, fireEvent, render, screen } from "@testing-library/react";
import type { DescMethodUnary } from "@bufbuild/protobuf";
import type { ReactNode } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { createQueryClient, PluginHost, PluginScope, useAction, useClient, useQuery, type Action } from "./index";
import { FakeService } from "./testing/fake-service";

// The fake host answers every call with {n: <calls so far>}, or with the
// next queued failure.
let calls: { url: string; body: string }[];
let failures: number[];

const fetchFake = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
  calls.push({ url: String(input), body: new TextDecoder().decode(init?.body as Uint8Array) });
  const status = failures.shift();
  if (status) {
    return new Response(JSON.stringify({ code: "failed_precondition", message: "the fake refused" }), {
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
});

afterEach(() => {
  cleanup();
  vi.useRealTimers();
});

const settle = async () => {
  for (let i = 0; i < 5; i++) await act(() => vi.advanceTimersByTimeAsync(1));
};

// mount renders children as the docker Plugin's, which requires "fake".
function mount(children: ReactNode) {
  render(
    <PluginHost client={createQueryClient()}>
      <PluginScope plugin={{ name: "docker", requires: ["fake"] }} fetch={fetchFake}>
        {children}
      </PluginScope>
    </PluginHost>,
  );
}

// Actor hands out its useAction for the test to run.
let action: Action<typeof FakeService.method.zap>;
function Actor({ method }: { method: DescMethodUnary }) {
  action = useAction(method as typeof FakeService.method.zap);
  return action.error ? <p>{action.error.message}</p> : null;
}

const actionCalls = () => calls.filter((c) => !c.url.endsWith("/Slow"));

describe("useAction", () => {
  it("runs a non-disruptive Action at once", async () => {
    mount(<Actor method={FakeService.method.poke} />);

    let res: unknown;
    await act(async () => {
      res = await action.run({ host: "media" });
    });

    expect(res).toMatchObject({ n: 1 });
    expect(calls.map((c) => c.url)).toEqual(["/api/c/fake/ddash.fake.FakeService/Poke"]);
    expect(screen.queryByRole("alertdialog")).toBeNull();
  });

  it("asks before a disruptive Action, with the confirm text, and runs it once confirmed", async () => {
    mount(<Actor method={FakeService.method.zap} />);

    let done: Promise<unknown> | undefined;
    act(() => {
      done = action.run({ host: "media" }, { confirm: "Stop container plex on media?" });
    });
    await settle();
    const dialog = screen.getByRole("alertdialog");
    expect(dialog.textContent).toContain("Stop container plex on media?");
    expect(calls).toHaveLength(0);

    fireEvent.click(screen.getByRole("button", { name: "Confirm" }));
    await settle();

    await expect(done).resolves.toMatchObject({ n: 1 });
    expect(calls.map((c) => c.url)).toEqual(["/api/c/fake/ddash.fake.FakeService/Zap"]);
    expect(JSON.parse(calls[0].body)).toEqual({ host: "media" });
    expect(screen.queryByRole("alertdialog")).toBeNull();
  });

  it("doesn't call when the Operator cancels", async () => {
    mount(<Actor method={FakeService.method.zap} />);

    let done: Promise<unknown> | undefined;
    act(() => {
      done = action.run({ host: "media" }, { confirm: "Stop it?" });
    });
    await settle();
    fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
    await settle();

    await expect(done).resolves.toBeUndefined();
    expect(calls).toHaveLength(0);
    expect(screen.queryByRole("alertdialog")).toBeNull();
  });

  it("passes the confirm dialog's checkbox to the request", async () => {
    mount(<Actor method={FakeService.method.zap} />);

    act(() => {
      void action.run(({ checked }) => ({ host: checked ? "everything" : "media" }), {
        confirm: { text: "Uninstall Docker?", checkbox: "Also delete data", action: "Uninstall" },
      });
    });
    await settle();
    fireEvent.click(screen.getByRole("checkbox", { name: "Also delete data" }));
    fireEvent.click(screen.getByRole("button", { name: "Uninstall" }));
    await settle();

    expect(JSON.parse(calls[0].body)).toEqual({ host: "everything" });
  });

  it("is refused by the transport for a disruptive Action without confirm", async () => {
    mount(<Actor method={FakeService.method.zap} />);

    let res: unknown = "unset";
    await act(async () => {
      res = await action.run({ host: "media" });
    });

    expect(res).toBeUndefined();
    expect(calls).toHaveLength(0);
    expect(screen.queryByRole("alertdialog")).toBeNull();
    expect(screen.getByText(/ddash\.fake\.FakeService\.Zap is disruptive: run it with a confirm text/)).toBeTruthy();
  });

  it("is refused by the transport for a disruptive call outside useAction", async () => {
    let answer: Promise<unknown> | undefined;
    function Caller() {
      const client = useClient(FakeService.service) as unknown as Record<string, (req: object) => Promise<unknown>>;
      answer ??= client.zap({ host: "media" }).then(
        () => "called",
        (err: Error) => err.message,
      );
      return null;
    }
    mount(<Caller />);
    await settle();

    await expect(answer).resolves.toMatch(/^\[failed_precondition\] ddash\.fake\.FakeService\.Zap is disruptive/);
    expect(calls).toHaveLength(0);
  });

  it("invalidates every active query on success", async () => {
    function Reader() {
      useQuery(FakeService.method.slow, { host: "media" });
      return null;
    }
    mount(
      <>
        <Reader />
        <Actor method={FakeService.method.poke} />
      </>,
    );
    await settle();
    expect(calls).toHaveLength(1);

    await act(async () => {
      await action.run({ host: "media" });
    });
    await settle();

    expect(calls.map((c) => c.url.split("/").pop())).toEqual(["Slow", "Poke", "Slow"]);
  });

  it("keeps the queries and shows the error when the Action fails", async () => {
    function Reader() {
      useQuery(FakeService.method.slow, { host: "media" });
      return null;
    }
    mount(
      <>
        <Reader />
        <Actor method={FakeService.method.poke} />
      </>,
    );
    await settle();

    failures.push(400);
    let res: unknown = "unset";
    await act(async () => {
      res = await action.run({ host: "media" });
    });
    await settle();

    expect(res).toBeUndefined();
    expect(actionCalls()).toHaveLength(1);
    expect(calls).toHaveLength(2);
    expect(screen.getByText("[failed_precondition] the fake refused")).toBeTruthy();
  });
});
