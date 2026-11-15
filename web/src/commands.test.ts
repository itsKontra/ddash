import type { Command } from "@ddash/plugin";
import { describe, expect, it, vi } from "vitest";
import { commandEntries, concernEntries, listed, pageEntries, score, search, type Entry } from "./commands";

const entry = (title: string, extra: Partial<Entry> = {}): Entry => ({ key: title, group: "Apps", title, run: () => {}, ...extra });

const titles = (entries: readonly Entry[]) => entries.map((e) => e.title);

describe("search", () => {
  const all = [
    entry("Open Grafana", { hint: "grafana.home.example", keywords: ["Monitoring"] }),
    entry("Open Jellyfin", { keywords: ["media.home.example"] }),
    entry("Restart paperless", { group: "Docker", hint: "Compose Project on media" }),
    entry("Restart immich", { group: "Docker", hint: "Compose Project on media" }),
    entry("Network", { group: "Go to" }),
    entry("Audit Log", { group: "Go to" }),
  ];

  it("finds an App by the start of its name", () => {
    expect(titles(search("graf", all))).toEqual(["Open Grafana"]);
  });

  it("needs every word to start a word: “restart pap” is paperless's restart", () => {
    expect(titles(search("restart pap", all))).toEqual(["Restart paperless"]);
    expect(titles(search("restart", all))).toEqual(["Restart immich", "Restart paperless"]);
  });

  it("matches word starts only, never inside a word", () => {
    expect(search("rafana", all)).toEqual([]);
    expect(search("log", all).map((e) => e.title)).toEqual(["Audit Log"]);
  });

  it("finds by hint, keywords and group too, below a title match", () => {
    expect(titles(search("media", all))).toEqual(["Open Jellyfin", "Restart immich", "Restart paperless"]);
    expect(titles(search("monitoring", all))).toEqual(["Open Grafana"]);
    expect(titles(search("go", all))).toEqual(["Network", "Audit Log"]);
  });

  it("ranks a word starting the title above one starting a later word", () => {
    const both = [entry("Open Network settings"), entry("Network")];
    expect(titles(search("net", both))).toEqual(["Network", "Open Network settings"]);
  });

  it("ignores case and punctuation, and an empty query matches nothing", () => {
    expect(titles(search("OPEN, GRAF!", all))).toEqual(["Open Grafana"]);
    expect(score("  ", all[0]!)).toBe(0);
  });

  it("keeps at most limit matches", () => {
    expect(search("open", all, 1)).toHaveLength(1);
  });
});

describe("commandEntries", () => {
  it("groups each Plugin's Commands under its title and runs them through the Plugin", () => {
    const run = vi.fn();
    const commands: Command[] = [{ id: "open:a", title: "Open A", hint: "a.example", icon: "globe", keywords: ["x"], run: () => {} }];
    const [e] = commandEntries([{ plugin: "apps", title: "Apps", commands }], run);
    expect(e).toMatchObject({ key: "apps:open:a", group: "Apps", title: "Open A", hint: "a.example", icon: "globe", keywords: ["x"] });
    e!.run();
    expect(run).toHaveBeenCalledWith("apps", "open:a");
  });

  it("drops a Command without a title or run, and a Plugin's second with the same id", () => {
    const ok = { id: "a", title: "A", run: () => {} };
    const bad = [ok, { ...ok, title: "A again" }, { id: "b", title: "", run: () => {} }, { id: "c", title: "C" } as unknown as Command];
    const entries = commandEntries(
      [
        { plugin: "apps", title: "Apps", commands: bad },
        { plugin: "docker", title: "Docker", commands: [ok] },
      ],
      () => {},
    );
    expect(entries.map((e) => e.key)).toEqual(["apps:a", "docker:a"]);
  });
});

describe("concernEntries", () => {
  it("only goes to a Concern whose main Action doesn't fix it (Mute), never running that", () => {
    const act = vi.fn();
    const go = vi.fn();
    const mute = { label: "Mute", secondary: true, run: () => {} };
    const entries = concernEntries(
      [
        { plugin: "checks", concern: { id: "nas", tone: "down", subject: "nas", title: "nas is down", cause: "no answer · 3 min ago", href: "/checks/2", actions: [mute] } },
        { plugin: "checks", concern: { id: "cam", tone: "down", subject: "cam", title: "cam is down", actions: [mute] } },
      ],
      act,
      go,
    );
    expect(entries.map((e) => [e.title, e.hint])).toEqual([
      ["nas is down", "no answer"],
      ["cam is down", undefined],
    ]);
    entries.forEach((e) => e.run());
    expect(act).not.toHaveBeenCalled();
    expect(go.mock.calls).toEqual([["/checks/2"], ["/"]]);
  });

  it("runs a Concern's main Action, named by it, else goes to its detail or the Overview", () => {
    const act = vi.fn();
    const go = vi.fn();
    const start = { label: "Start", run: () => {} };
    const entries = concernEntries(
      [
        { plugin: "proxmox", concern: { id: "n8n", tone: "down", subject: "n8n", title: "n8n is down", cause: "LXC 206 stopped · 6 min ago", actions: [start] } },
        { plugin: "checks", concern: { id: "nas", tone: "warn", subject: "nas", title: "nas is slow", href: "/checks/12" } },
        { plugin: "network", concern: { id: "dev", tone: "info", subject: "New Device", title: "A new Device" } },
      ],
      act,
      go,
    );
    expect(entries.map((e) => [e.group, e.title, e.hint, e.icon])).toEqual([
      ["Needs you", "Start · n8n is down", "LXC 206 stopped", "circle-x"],
      ["Needs you", "nas is slow", undefined, "triangle-exclamation-point"],
      ["Needs you", "A new Device", undefined, "info"],
    ]);
    entries.forEach((e) => e.run());
    expect(act).toHaveBeenCalledWith(expect.objectContaining({ plugin: "proxmox" }), start);
    expect(go.mock.calls).toEqual([["/checks/12"], ["/"]]);
  });
});

describe("listed", () => {
  const go = vi.fn();
  const all = {
    concerns: [entry("Start · n8n is down", { group: "Needs you" })],
    pages: pageEntries(
      [
        { to: "/", label: "Overview", icon: "layout-dashboard" },
        { to: "/settings/audit-log", label: "Audit Log", icon: "scroll-text" },
      ],
      go,
    ),
    actions: [entry("Switch to dark mode", { group: "Actions" })],
    commands: [entry("Open Grafana")],
  };

  it("lists the loud Concerns, then the Pages, then the shell's Actions while the query is empty", () => {
    expect(listed("", all).map((e) => [e.group, e.title])).toEqual([
      ["Needs you", "Start · n8n is down"],
      ["Go to", "Overview"],
      ["Go to", "Audit Log"],
      ["Actions", "Switch to dark mode"],
    ]);
  });

  it("lists the first six pinned Commands under Pinned, after the Concerns and before the Pages", () => {
    const apps = Array.from({ length: 8 }, (_, i) => entry(`Open App${i}`, { pinned: i !== 1 }));
    const empty = listed("", { ...all, commands: apps });
    expect(empty.map((e) => [e.group, e.title]).slice(0, 8)).toEqual([
      ["Needs you", "Start · n8n is down"],
      ["Pinned", "Open App0"],
      ["Pinned", "Open App2"],
      ["Pinned", "Open App3"],
      ["Pinned", "Open App4"],
      ["Pinned", "Open App5"],
      ["Pinned", "Open App6"],
      ["Go to", "Overview"],
    ]);
    // Typed, a pinned one is found under its Plugin like any other.
    expect(listed("app7", { ...all, commands: apps }).map((e) => [e.group, e.title])).toEqual([["Apps", "Open App7"]]);
  });

  it("leads with the worst five loud Concerns only; typing finds the others", () => {
    const many = Array.from({ length: 7 }, (_, i) => entry(`Start · guest${i} is down`, { group: "Needs you" }));
    const empty = listed("", { ...all, concerns: many });
    expect(empty.filter((e) => e.group === "Needs you").map((e) => e.title)).toEqual(many.slice(0, 5).map((e) => e.title));
    expect(titles(listed("guest6", { ...all, concerns: many }))).toEqual(["Start · guest6 is down"]);
  });

  it("searches everything, the Plugins' Commands too, once typed", () => {
    expect(titles(listed("graf", all))).toEqual(["Open Grafana"]);
    expect(titles(listed("audit", all))).toEqual(["Audit Log"]);
    listed("audit", all)[0]!.run();
    expect(go).toHaveBeenCalledWith("/settings/audit-log");
  });
});
