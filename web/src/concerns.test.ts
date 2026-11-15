import type { Concern, ConcernTone } from "@ddash/plugin";
import { describe, expect, it } from "vitest";
import { needs, offOnPurpose } from "./concerns";

const concern = (id: string, tone: ConcernTone, quiet?: string): Concern => ({ id, tone, subject: id, title: `${id} is ${tone}`, quiet });

const ids = (listed: { plugin: string; concern: Concern }[]) => listed.map((l) => `${l.plugin}:${l.concern.id}`);

describe("needs", () => {
  it("orders every Plugin's Concerns down, warn, info, then by title", () => {
    const n = needs([
      { plugin: "network", concerns: [concern("new-device", "info"), concern("vpn", "warn")] },
      { plugin: "checks", concerns: [concern("router", "down"), concern("cert", "warn"), concern("nas", "down")] },
      { plugin: "apps", concerns: [concern("grafana", "down")] },
    ]);

    expect(ids(n.loud)).toEqual(["apps:grafana", "checks:nas", "checks:router", "checks:cert", "network:vpn", "network:new-device"]);
    expect(n.quiet).toEqual([]);
    expect(n.tone).toBe("down");
  });

  it("folds the quiet ones away, and they don't set the tone", () => {
    const n = needs([{ plugin: "checks", concerns: [concern("backup", "down", "muted"), concern("vpn", "warn"), concern("nvr", "down", "in Maintenance")] }]);

    expect(ids(n.loud)).toEqual(["checks:vpn"]);
    expect(ids(n.quiet)).toEqual(["checks:backup", "checks:nvr"]);
    expect(n.quiet.every((l) => l.concern.tone === "down")).toBe(true);
    expect(n.tone).toBe("warn");
  });

  it("has no tone when only quiet Concerns are reported, or none", () => {
    expect(needs([{ plugin: "checks", concerns: [concern("backup", "down", "muted")] }]).tone).toBeUndefined();
    expect(needs([])).toEqual({ loud: [], quiet: [], tone: undefined });
  });

  it("drops a Plugin's second Concern with the same id, but not another Plugin's", () => {
    const n = needs([
      { plugin: "checks", concerns: [concern("nas", "down"), concern("nas", "warn")] },
      { plugin: "apps", concerns: [concern("nas", "down")] },
    ]);

    expect(ids(n.loud).sort()).toEqual(["apps:nas", "checks:nas"]);
  });
});

describe("offOnPurpose", () => {
  it("counts the quiet Concerns by why, most first", () => {
    const { quiet } = needs([
      { plugin: "checks", concerns: [concern("nvr", "down", "in Maintenance"), concern("a", "down", "muted"), concern("b", "down", "muted")] },
      { plugin: "apps", concerns: [concern("c", "down", "muted")] },
    ]);

    expect(offOnPurpose(quiet)).toBe("3 muted, 1 in Maintenance");
  });
});
