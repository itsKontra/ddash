// The WCAG AA bar every Theme meets, computed from theme.css. The derived
// mixes below follow the rules tokens.css and surfaces.css apply in CSS.

/** A Theme's tokens as theme.css sets them, per scheme (radius and font live in light). */
export type Theme = { name: string; light: Map<string, string>; dark: Map<string, string> };

// :root is the ddash Theme; another is :root[data-theme="<name>"]; .dark marks the dark scheme.
// A block's first selector names it; the rest of its list (the previews) is skipped.
const selector = /(?:^|[;}])\s*:root((?:\.dark|\[data-theme="[\w-]+"\])*)\s*(?:,[^{]*)?{([^}]*)/g;

/** Every Theme theme.css sets, by name. */
export function themes(css: string): Theme[] {
  const byName = new Map<string, Theme>();
  for (const m of css.replace(/\/\*[\s\S]*?\*\//g, "").matchAll(selector)) {
    const name = /\[data-theme="([\w-]+)"\]/.exec(m[1])?.[1] ?? "ddash";
    const theme = byName.get(name) ?? { name, light: new Map(), dark: new Map() };
    byName.set(name, theme);
    const tokens = m[1].includes(".dark") ? theme.dark : theme.light;
    for (const [, k, v] of m[2].matchAll(/--([\w-]+):\s*([^;]+);/g)) tokens.set(k, v.trim());
  }
  return [...byName.values()];
}

const tones = ["up", "down", "warn", "maint", "info"];
const charts = ["chart-1", "chart-2", "chart-3", "chart-4", "chart-5", "chart-6"];

/** Where the Theme falls short of the AA bar, as "<scheme>: <fg> on <bg> is <ratio>:1, below <bar>:1"; none when it meets it. */
export function aaFailures(theme: Theme): string[] {
  const failures: string[] = [];
  for (const scheme of ["light", "dark"] as const) {
    const tokens = theme[scheme];
    const colour = (token: string): RGB | undefined => {
      const value = tokens.get(token);
      const rgb = value === undefined ? undefined : hex(value);
      if (rgb === undefined) failures.push(`${scheme}: ${token} is ${value === undefined ? "not set" : `not a hex colour (${value})`}`);
      return rgb;
    };
    const surfaces = new Map<string, RGB | undefined>();
    const surface = (name: string, rgb: RGB | undefined) => surfaces.set(name, rgb);
    for (const t of ["card", "background", "nav", "hero-from", "hero-to", "primary"]) surface(t, colour(t));
    const nav = surfaces.get("nav");
    const mixInto = (token: string, pct: number) => {
      const c = colour(token);
      return c && nav && mix(c, pct, nav);
    };
    surface("nav-hover", mixInto("nav-strong", 0.06));
    surface("nav-active", mixInto("nav-accent", 0.15));

    const bar = (fg: string, bgs: string[], min: number, fgColour = colour(fg)) => {
      for (const bg of bgs) {
        const b = surfaces.get(bg);
        if (fgColour === undefined || b === undefined) continue;
        const ratio = contrast(fgColour, b);
        if (ratio < min) failures.push(`${scheme}: ${fg} on ${bg} is ${ratio.toFixed(2)}:1, below ${min}:1`);
      }
    };

    // Text: on card, background and its own 12% tint (a pill or chip on a card).
    bar("foreground", ["card", "background"], 4.5);
    bar("muted-foreground", ["card", "background"], 4.5);
    bar("primary-foreground", ["primary"], 4.5);
    const card = surfaces.get("card");
    for (const tone of tones) {
      const fill = colour(tone);
      surface(`${tone}-tint`, fill && card && mix(fill, 0.12, card));
      bar(`${tone}-text`, ["card", "background", `${tone}-tint`], 4.5);
    }
    // Primary as text: a link, and a selected Segmented, Tab or chip on its tint.
    const primary = surfaces.get("primary");
    surface("primary-tint", primary && card && mix(primary, 0.12, card));
    bar("primary", ["card", "primary-tint"], 4.5);
    // Marks: tone fills, chart colours and the focus outline on card.
    for (const mark of [...tones, ...charts, "primary"]) bar(mark, ["card"], 3);
    // Nav and hero text, and the ring on the hero.
    const navAndHero = ["nav", "nav-hover", "nav-active", "hero-from", "hero-to"];
    bar("nav-foreground", navAndHero, 4.5);
    bar("nav-strong", navAndHero, 4.5);
    bar("nav-accent", ["hero-from", "hero-to"], 3);
    // A Nav Entry's badge: its tone's nav text on its tint, over the nav and its states.
    const strong = colour("nav-strong");
    for (const tone of tones) {
      const fill = colour(tone);
      if (!fill || !strong) continue;
      const tinted = ["nav", "nav-hover", "nav-active"].map((row) => {
        const name = `${tone}-tint over ${row}`;
        const under = surfaces.get(row);
        surface(name, under && mix(fill, 0.12, under));
        return name;
      });
      bar(`nav-${tone}`, tinted, 4.5, mix(fill, 0.6, strong));
    }
  }
  return [...new Set(failures)];
}

type RGB = [number, number, number];

function hex(value: string): RGB | undefined {
  const m = /^#([0-9a-f]{6})$/i.exec(value);
  if (!m) return undefined;
  const n = Number.parseInt(m[1], 16);
  return [(n >> 16) & 255, (n >> 8) & 255, n & 255];
}

/** color-mix(in srgb, a pct, b): what a pct-strength a over b paints. */
const mix = (a: RGB, pct: number, b: RGB): RGB => [0, 1, 2].map((i) => a[i] * pct + b[i] * (1 - pct)) as RGB;

function luminance(c: RGB): number {
  const [r, g, b] = c.map((v) => {
    const s = v / 255;
    return s <= 0.04045 ? s / 12.92 : ((s + 0.055) / 1.055) ** 2.4;
  });
  return 0.2126 * r + 0.7152 * g + 0.0722 * b;
}

function contrast(a: RGB, b: RGB): number {
  const [hi, lo] = [luminance(a), luminance(b)].sort((x, y) => y - x);
  return (hi + 0.05) / (lo + 0.05);
}
