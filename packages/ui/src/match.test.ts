import { expect, it } from "vitest";
import { matches } from "./index";

it("matches ignores case", () => {
  expect(matches("NAS", "nas-01")).toBe(true);
  expect(matches("nas", "NAS-01")).toBe(true);
  expect(matches("router", "nas-01")).toBe(false);
});

it("matches needs every word of the query in some field, not all in one", () => {
  expect(matches("nas 10.0.0", "nas-01", "10.0.0.2")).toBe(true);
  expect(matches("  nas   10.0.0  ", "nas-01", "10.0.0.2")).toBe(true);
  expect(matches("nas 10.0.1", "nas-01", "10.0.0.2")).toBe(false);
});

it("matches finds a MAC with or without its separators", () => {
  expect(matches("aa:bb", "AA:BB:CC:DD:EE:FF")).toBe(true);
  expect(matches("aabb", "aa:bb:cc:dd:ee:ff")).toBe(true);
  expect(matches("aabbcc", "aa-bb-cc-dd-ee-ff")).toBe(true);
  expect(matches("aa-bb-cc", "aa:bb:cc:dd:ee:ff")).toBe(true);
  expect(matches("aacc", "aa:bb:cc:dd:ee:ff")).toBe(false);
});

it("matches leaves separators significant in a field that isn't a MAC", () => {
  expect(matches("nasbox", "nas-box")).toBe(false);
});

it("matches everything with an empty query", () => {
  expect(matches("", "nas-01")).toBe(true);
  expect(matches("   ")).toBe(true);
});

it("matches skips undefined fields", () => {
  expect(matches("nas", undefined, "nas-01")).toBe(true);
  expect(matches("nas", undefined)).toBe(false);
});
