import { create } from "@bufbuild/protobuf";
import { AppSchema } from "@ddash/proto/apps/apps_pb";
import { expect, it } from "vitest";
import { moved, pinIds, pinned, withPin } from "./pins";

// a pinned third, b unpinned, c pinned first, d Hidden and pinned second, e pinned fourth.
const apps = [
  create(AppSchema, { id: "a", pin: 3 }),
  create(AppSchema, { id: "b" }),
  create(AppSchema, { id: "c", pin: 1 }),
  create(AppSchema, { id: "d", pin: 2, hidden: true }),
  create(AppSchema, { id: "e", pin: 4 }),
];

it("orders the pins by the Operator's order, the Widget leaving out Hidden and orphaned Apps", () => {
  expect(pinIds(apps)).toEqual(["c", "d", "a", "e"]);
  expect(pinned(apps).map((a) => a.id)).toEqual(["c", "a", "e"]);
  expect(pinned([...apps, create(AppSchema, { id: "f", pin: 5, orphaned: true })]).map((a) => a.id)).toEqual(["c", "a", "e"]);
  expect(pinned([apps[1]])).toEqual([]);
});

it("pins an App last and unpins it, keeping the other pins", () => {
  expect(withPin(apps, "b", true)).toEqual(["c", "d", "a", "e", "b"]);
  expect(withPin(apps, "a", false)).toEqual(["c", "d", "e"]);
  expect(withPin(apps, "d", false)).toEqual(["c", "a", "e"]);
});

it("moves a shown pin past its shown neighbour, keeping the Hidden one's place", () => {
  expect(moved(apps, "a", -1)).toEqual(["a", "c", "d", "e"]);
  expect(moved(apps, "c", 1)).toEqual(["d", "a", "c", "e"]);
  expect(moved(apps, "a", 1)).toEqual(["c", "d", "e", "a"]);
  expect(moved(apps, "c", -1)).toBeUndefined();
  expect(moved(apps, "e", 1)).toBeUndefined();
  expect(moved(apps, "b", 1)).toBeUndefined();
});
