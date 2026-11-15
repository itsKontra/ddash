import type { Widget, WidgetSize } from "@ddash/plugin";
import { expect, it } from "vitest";
import { reconcile, waits, type Layout } from "./layout";

// A Widget offering sizes, the first being its default, placed when first
// offered (defaultOrder 0: in the order offered).
const widget = (id: string, ...sizes: WidgetSize[]): Widget => ({
  id,
  title: id,
  sizes,
  defaultSize: sizes[0],
  defaultOrder: 0,
  Body: () => null,
  useStatus: () => undefined,
});
// The same Widget at another defaultOrder, or none.
const at = (w: Widget, defaultOrder: number | undefined): Widget => ({ ...w, defaultOrder });

const empty: Layout = { entries: [], seen: [] };

it("appends every unseen Widget with a defaultOrder at its default size, keyed <plugin>.<widget-id>", () => {
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

it("forgets an uninstalled Plugin's Widgets, so a reinstall places them again", () => {
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

it("places a fresh Overview's default Widgets by their defaultOrder across Plugins, and leaves the others seen, under Add Widget", () => {
  const got = reconcile(
    empty,
    new Map([
      ["checks", [at(widget("summary", "S", "M"), 60)]],
      ["network", [at(widget("network", "M", "S"), 30), at(widget("vpn", "S"), undefined), at(widget("devices", "S"), undefined)]],
      ["apps", [at(widget("apps", "L", "M"), 10)]],
      ["cloudflare-tunnel", [at(widget("tunnel", "S", "M"), undefined)]],
    ]),
  );

  expect(got.entries).toEqual([
    { widget: "apps.apps", size: "L" },
    { widget: "network.network", size: "M" },
    { widget: "checks.summary", size: "S" },
  ]);
  expect(got.seen.sort()).toEqual(["apps.apps", "checks.summary", "cloudflare-tunnel.tunnel", "network.devices", "network.network", "network.vpn"]);
});

it("places a later-installed Plugin's default Widgets after the Operator's, and never moves those", () => {
  const layout: Layout = {
    entries: [
      { widget: "checks.summary", size: "M" },
      { widget: "apps.apps", size: "M" },
    ],
    seen: ["checks.summary", "apps.apps"],
  };

  const got = reconcile(
    layout,
    new Map([
      ["apps", [at(widget("apps", "L", "M"), 10)]],
      ["checks", [at(widget("summary", "S", "M"), 60)]],
      ["network", [at(widget("vpn", "S"), undefined), at(widget("network", "M", "S"), 30)]],
    ]),
  );

  expect(got).toEqual({
    entries: [...layout.entries, { widget: "network.network", size: "M" }],
    seen: ["checks.summary", "apps.apps", "network.vpn", "network.network"],
  });
});

it("leaves a saved layout as it is when nothing new is offered, whatever the defaultOrders say", () => {
  const layout: Layout = {
    entries: [
      { widget: "network.vpn", size: "S" },
      { widget: "network.devices", size: "S" },
    ],
    seen: ["network.vpn", "network.devices", "network.network"],
  };

  expect(reconcile(layout, new Map([["network", [at(widget("network", "M", "S"), 30), at(widget("vpn", "S"), undefined), at(widget("devices", "S"), undefined)]]]))).toEqual(layout);
});

it("waits with a layout never saved until every bundle loaded or failed, and never with a saved one", () => {
  const loaded = new Set(["apps"]);
  const settled = (p: string) => loaded.has(p);

  expect(waits(empty, ["apps", "network"], settled)).toBe(true);
  expect(waits(empty, ["apps"], settled)).toBe(false);
  expect(waits(empty, [], settled)).toBe(false);
  expect(waits({ entries: [], seen: ["apps.apps"] }, ["apps", "network"], settled)).toBe(false);
});
