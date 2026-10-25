// @vitest-environment node
/// <reference types="node" />
import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";
import { aaFailures, themes } from "./theme-check";

const css = readFileSync(new URL("./theme.css", import.meta.url), "utf8");
const derivedCSS = ["tokens.css", "surfaces.css"].map((f) => readFileSync(new URL(`./${f}`, import.meta.url), "utf8")).join("\n");

// The default Theme, as the spec's table gives it ("Theme and palette").
const ddash = {
  light: {
    background: "#f6f8fb",
    foreground: "#0f1d33",
    card: "#ffffff",
    muted: "#eef2f6",
    "muted-foreground": "#5c6b7f",
    border: "#e7ecf2",
    primary: "#1e5eea",
    "primary-foreground": "#ffffff",
    up: "#12a27c",
    "up-text": "#09785d",
    down: "#e2414f",
    "down-text": "#c32a3e",
    warn: "#c47f12",
    "warn-text": "#8f5f25",
    maint: "#8e6ee6",
    "maint-text": "#7657bf",
    info: "#0d9cbf",
    "info-text": "#0e7490",
    "chart-1": "#2f6fec",
    "chart-2": "#d6458e",
    "chart-3": "#7a5af5",
    "chart-4": "#d9761c",
    "chart-5": "#0f9db3",
    "chart-6": "#64748b",
    nav: "#0f1a2e",
    "nav-foreground": "#a9b8c8",
    "nav-strong": "#ffffff",
    "nav-accent": "#5b9cf5",
    "hero-from": "#162a52",
    "hero-to": "#0d1a33",
    elevation: "0 12px 34px rgba(22, 42, 63, 0.05)",
    "elevation-raised": "0 18px 40px rgba(14, 40, 53, 0.14)",
    radius: "0.75rem",
    font: '"Inter", ui-sans-serif, system-ui, sans-serif',
  },
  dark: {
    background: "#0a0f1a",
    foreground: "#e3ebf3",
    card: "#111a2a",
    muted: "#172235",
    "muted-foreground": "#8597ab",
    border: "#1e2b40",
    primary: "#60a5fa",
    "primary-foreground": "#04142e",
    up: "#2fc79a",
    "up-text": "#2fc79a",
    down: "#f06b7c",
    "down-text": "#f06b7c",
    warn: "#e3a55a",
    "warn-text": "#e3a55a",
    maint: "#a48be0",
    "maint-text": "#a48be0",
    info: "#22d3ee",
    "info-text": "#22d3ee",
    "chart-1": "#60a5fa",
    "chart-2": "#f472b6",
    "chart-3": "#a78bfa",
    "chart-4": "#fb923c",
    "chart-5": "#22d3ee",
    "chart-6": "#94a3b8",
    nav: "#070d18",
    "nav-foreground": "#95a7ba",
    "nav-strong": "#f1f6fa",
    "nav-accent": "#60a5fa",
    "hero-from": "#132648",
    "hero-to": "#0a1529",
    elevation: "0 12px 34px rgba(0, 0, 0, 0.3)",
    "elevation-raised": "0 18px 40px rgba(0, 0, 0, 0.45)",
  },
};

describe("the ddash Theme", () => {
  const theme = themes(css).find((t) => t.name === "ddash");

  it("sets exactly the spec's tokens and values, light and dark", () => {
    expect(theme).toBeDefined();
    expect(Object.fromEntries(theme!.light)).toEqual(ddash.light);
    expect(Object.fromEntries(theme!.dark)).toEqual(ddash.dark);
  });

  it("leaves tints, nav states, the radius scale, monospace and focus to @ddash/ui's derived rules", () => {
    for (const [token, rule] of [
      ["--color-up-tint", "color-mix(in srgb, var(--up) 12%, transparent)"],
      ["--color-primary-tint", "color-mix(in srgb, var(--primary) 12%, transparent)"],
      ["--color-nav-hover", "color-mix(in srgb, var(--nav-strong) 6%, var(--nav))"],
      ["--color-nav-border", "color-mix(in srgb, var(--nav-strong) 8%, var(--nav))"],
      ["--color-nav-active", "color-mix(in srgb, var(--nav-accent) 15%, var(--nav))"],
      ["--radius-ui-sm", "calc(var(--radius) * 2 / 3)"],
      ["--radius-ui-lg", "calc(var(--radius) * 4 / 3)"],
      ["--font-mono", "ui-monospace,"],
    ]) {
      expect(derivedCSS, token).toContain(`${token}: ${rule}`);
    }
    expect(css).toMatch(/:focus-visible\s*{\s*outline:\s*2px solid var\(--primary\);\s*outline-offset:\s*2px;/);
  });
});

describe("the AA bar", () => {
  it("is met by every Theme in theme.css, in both schemes", () => {
    const all = themes(css);
    expect(all.length).toBeGreaterThan(0);
    for (const t of all) expect(aaFailures(t), t.name).toEqual([]);
  });

  it("fails a Theme whose value is illegible", () => {
    const theme = themes(css).find((t) => t.name === "ddash")!;
    const failing = (scheme: "light" | "dark", token: string, value: string) => {
      const broken = { ...theme, [scheme]: new Map(theme[scheme]).set(token, value) };
      return aaFailures(broken);
    };
    // The light warn fill as text: 3.3:1 on white.
    expect(failing("light", "warn-text", "#c47f12")).toContainEqual(expect.stringMatching(/^light: warn-text on card is 3\.\d+:1, below 4\.5:1$/));
    // A pale chart colour: a mark under 3:1.
    expect(failing("light", "chart-4", "#f5c08a")).toContainEqual(expect.stringMatching(/^light: chart-4 on card is 1\.\d+:1, below 3:1$/));
    // Nav text too dim for the derived active state.
    expect(failing("dark", "nav-foreground", "#3f4c5c")).toContainEqual(expect.stringMatching(/^dark: nav-foreground on nav-active is \d\.\d+:1, below 4\.5:1$/));
    // A ring that vanishes on the hero.
    expect(failing("dark", "nav-accent", "#1d3a66")).toContainEqual(expect.stringMatching(/^dark: nav-accent on hero-from is 1\.\d+:1, below 3:1$/));
    // Text on its own tint.
    expect(failing("light", "info-text", "#1393b4")).toContainEqual(expect.stringMatching(/^light: info-text on info-tint is \d\.\d+:1, below 4\.5:1$/));
  });
});

it("gives Plugins no nav or hero utilities", () => {
  const plugin = ["plugin.css", "tokens.css"].map((f) => readFileSync(new URL(`./${f}`, import.meta.url), "utf8")).join("\n").replace(/\/\*[\s\S]*?\*\//g, "");
  expect(plugin).toContain('@import "./tokens.css"');
  expect(plugin).not.toMatch(/surfaces\.css|--color-(nav|hero)/);
  expect(css).toContain('@import "./surfaces.css"');
});
