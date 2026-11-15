import { Code, ConnectError } from "@connectrpc/connect";
import { expect, it } from "vitest";
import { isNotConfigured } from "./index";
import { notConfigured } from "./testing";

/** The ConnectError a Connect JSON error answer becomes. */
async function errorOf(res: Response): Promise<ConnectError> {
  const body = (await res.json()) as { message: string; details?: { type: string; value: string }[] };
  const err = new ConnectError(body.message, Code.FailedPrecondition);
  err.details = (body.details ?? []).map((d) => ({ type: d.type, value: Uint8Array.from(atob(d.value), (c) => c.charCodeAt(0)) }));
  return err;
}

it("tells a Component without config by the error's detail", async () => {
  expect(isNotConfigured(await errorOf(notConfigured()))).toBe(true);
});

it("doesn't go by the message: the same words without the detail are a failure, and other words with it are not", async () => {
  expect(isNotConfigured(new ConnectError("not configured", Code.FailedPrecondition))).toBe(false);
  const reworded = new ConnectError("set it up first", Code.FailedPrecondition);
  reworded.details = (await errorOf(notConfigured())).details;
  expect(isNotConfigured(reworded)).toBe(true);
});

it("is false for no error and for anything that isn't a Connect error", () => {
  expect(isNotConfigured(undefined)).toBe(false);
  expect(isNotConfigured(null)).toBe(false);
  expect(isNotConfigured(new Error("not configured"))).toBe(false);
});
