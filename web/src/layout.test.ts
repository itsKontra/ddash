import type { Widget, WidgetSize } from "@ddash/plugin";
import { expect, it } from "vitest";
import { reconcile, type Layout } from "./layout";

// A Widget offering sizes, the first being its default.
const widget = (id: string, ...sizes: WidgetSize[]): Widget => ({
  id,
  title: id,
  sizes,
  defaultSize: sizes[0],
  Body: () => null,
  useStatus: () => undefined,
});

const empty: Layout = { entries: [], seen: [] };

it("appends every unseen Widget at its default size, keyed <plugin>.<widget-id>", () => {
  const got = reconcile(empty, new Map([["docker", [widget("containers", "S", "M")]], ["network", [widget("throughput", "M"), widget("vpn", "S")]]]));

  expect(got).toEqual({
    entries: [
      { widget: "docker.containers", size: "S" },
      { widget: "network.throughput", size: "M" },
      { widget: "network.vpn", size: "S" },
    ],
    seen: ["docker.containers", "network.throughput", "network.vpn"],
  });
});

it("keeps a Widget the Operator removed removed", () => {
  const layout: Layout = { entries: [{ widget: "docker.containers", size: "M" }], seen: ["docker.containers", "network.vpn"] };

  const got = reconcile(layout, new Map([["docker", [widget("containers", "S", "M")]], ["network", [widget("vpn", "S")]]]));

  expect(got).toEqual(layout);
});

it("drops a placed Widget its Plugin no longer offers, and forgets it", () => {
  const layout: Layout = {
    entries: [
      { widget: "network.devices", size: "S" },
      { widget: "network.vpn", size: "S" },
    ],
    seen: ["network.devices", "network.vpn", "network.old"],
  };

  const got = reconcile(layout, new Map([["network", [widget("vpn", "S")]]]));

  expect(got).toEqual({ entries: [{ widget: "network.vpn", size: "S" }], seen: ["network.vpn"] });
});

it("resets a size the Widget no longer supports to its default", () => {
  const layout: Layout = { entries: [{ widget: "checks.summary", size: "L" }], seen: ["checks.summary"] };

  const got = reconcile(layout, new Map([["checks", [widget("summary", "S", "M")]]]));

  expect(got.entries).toEqual([{ widget: "checks.summary", size: "S" }]);
});

it("forgets an uninstalled Plugin's Widgets, so a reinstall appends them again", () => {
  const layout: Layout = {
    entries: [
      { widget: "docker.containers", size: "M" },
      { widget: "checks.summary", size: "S" },
    ],
    seen: ["docker.containers", "checks.summary", "docker.hosts"],
  };
  const checks = [widget("summary", "S", "M")];

  const uninstalled = reconcile(layout, new Map([["checks", checks]]));
  const reinstalled = reconcile(uninstalled, new Map([["checks", checks], ["docker", [widget("containers", "S", "M"), widget("hosts", "S")]]]));

  expect(uninstalled).toEqual({ entries: [{ widget: "checks.summary", size: "S" }], seen: ["checks.summary"] });
  expect(reinstalled.entries).toEqual([
    { widget: "checks.summary", size: "S" },
    { widget: "docker.containers", size: "S" },
    { widget: "docker.hosts", size: "S" },
  ]);
});

it("leaves an installed Plugin's entries alone while its bundle loads or after it failed", () => {
  const layout: Layout = { entries: [{ widget: "docker.containers", size: "L" }], seen: ["docker.containers", "docker.hosts"] };

  expect(reconcile(layout, new Map([["docker", undefined]]))).toEqual(layout);
});

it("places each Widget at most once", () => {
  const layout: Layout = {
    entries: [
      { widget: "docker.containers", size: "S" },
      { widget: "docker.containers", size: "M" },
    ],
    seen: ["docker.containers"],
  };

  const got = reconcile(layout, new Map([["docker", [widget("containers", "S", "M")]]]));

  expect(got).toEqual({ entries: [{ widget: "docker.containers", size: "S" }], seen: ["docker.containers"] });
});
