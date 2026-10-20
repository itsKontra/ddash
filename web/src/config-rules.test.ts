// @vitest-environment node
/// <reference types="node" />
import { create, type MessageInitShape } from "@bufbuild/protobuf";
import { FormField_Kind, FormFieldSchema } from "@ddash/proto/ddash/core/core_pb";
import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";
import { checkFormat, validate } from "./config-rules";

// The cases the host's core/config runs too, so both check the same rules.
const cases: Record<string, { message: string; ok: string[]; bad: string[] }> = JSON.parse(
  readFileSync(new URL("../../core/config/testdata/formats.json", import.meta.url), "utf8"),
);
delete cases._comment;

describe.each(Object.entries(cases))("format %s", (format, { message, ok, bad }) => {
  it.each(ok)("accepts %j", (value) => expect(checkFormat(format, value)).toBe(""));
  it.each(bad)("refuses %j", (value) => expect(checkFormat(format, value)).toBe(message));
});

const field = (init: MessageInitShape<typeof FormFieldSchema>) => create(FormFieldSchema, { kind: FormField_Kind.STRING, ...init });

const fields = [
  field({ name: "target", required: true }),
  field({ name: "greeting", required: true, default: "hello" }),
  field({ name: "times", kind: FormField_Kind.INT, min: 1n, max: 10n }),
  field({ name: "token", secret: true, required: true }),
  field({ name: "tags", repeated: true, format: "hostname" }),
  field({
    name: "peers",
    kind: FormField_Kind.TABLE,
    repeated: true,
    required: true,
    columns: [field({ name: "name", required: true }), field({ name: "port", kind: FormField_Kind.INT, min: 1n, max: 65535n })],
  }),
];

it("checks required, format and min/max like the host, keyed by the host's paths", () => {
  const errors = validate(fields, { times: "11", tags: ["pve", "not a host"], peers: [{ port: "22" }, { name: "b", port: 0 }] }, []);

  expect(errors).toEqual({
    target: "is required",
    times: "must be at most 10",
    token: "is required",
    "tags[1]": "must be a hostname like pve.home.example",
    "peers[0].name": "is required",
    "peers[1].port": "must be at least 1",
  });
});

it("takes a default or a stored secret as set", () => {
  expect(validate(fields, { target: "world", peers: [{ name: "a" }] }, ["token"])).toEqual({});
});

it("requires a list to have an entry", () => {
  expect(validate(fields, { target: "world", peers: [] }, ["token"])).toEqual({ peers: "is required" });
});
