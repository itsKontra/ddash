import { mkdirSync, mkdtempSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { describe, expect, it } from "vitest";
import { lintStyle } from "./style.ts";

const indexCSS = '@import "@ddash/ui/plugin.css";\n';

/** The Plugin hello's dir, holding files (path under src/ → contents), plus its index.css. */
function plugin(files: Record<string, string>): string {
  const dir = join(mkdtempSync(join(tmpdir(), "ddash-style-")), "hello");
  for (const [path, body] of Object.entries({ "index.css": indexCSS, ...files })) {
    const p = join(dir, "src", path);
    mkdirSync(dirname(p), { recursive: true });
    writeFileSync(p, body);
  }
  return dir;
}

describe("lintStyle", () => {
  it("passes a Plugin built from @ddash/ui and Theme token classes", () => {
    const dir = plugin({
      "index.tsx": `import { definePlugin } from "@ddash/plugin";
import { DockerApiService } from "@ddash/proto/docker-api/docker_api_pb";
import { cx, Pill, StatusDot } from "@ddash/ui";
import { useState } from "react";
import { Hosts } from "./hosts";
import "./index.css";

export function Row({ down }: { down: boolean }) {
  return (
    <section className="rounded-ui border border-border bg-card font-sans">
      <h2 className={cx("font-semibold text-sm", down && "text-down")}>{"jellyfin/jellyfin:10.10"}</h2>
      <p className={\`truncate px-2 py-2 \${down ? "text-muted-foreground" : "text-up"}\`}>Error: [object]</p>
      <span className="hover:bg-muted/50 last:border-b-0 rounded-full dark:text-foreground" style={{ width: "40%" }} />
    </section>
  );
}
`,
    });
    expect(lintStyle(dir)).toEqual([]);
  });

  it("fails hard-coded colours", () => {
    const dir = plugin({
      "row.tsx": `export const Row = () => (
  <div className="bg-red-500 hover:text-white border-slate-200/50">
    <svg fill="#ff0000" stroke="rgb(0 0 0)" />
  </div>
);
`,
    });
    expect(lintStyle(dir)).toEqual([
      'src/row.tsx:2: "bg-red-500" is a hard-coded colour; use a Theme token class (bg-card, text-down, ...)',
      'src/row.tsx:2: "hover:text-white" is a hard-coded colour; use a Theme token class (bg-card, text-down, ...)',
      'src/row.tsx:2: "border-slate-200/50" is a hard-coded colour; use a Theme token class (bg-card, text-down, ...)',
      'src/row.tsx:3: "#ff0000" is a hard-coded colour; use a Theme token class (bg-card, text-down, ...)',
      'src/row.tsx:3: "rgb(0 0 0)" is a hard-coded colour; use a Theme token class (bg-card, text-down, ...)',
    ]);
  });

  it("fails hard-coded radii", () => {
    const dir = plugin({ "row.tsx": `export const c = ["rounded", "rounded-lg", "md:rounded-t-md", "rounded-ui rounded-t-ui rounded-full rounded-none"];\n` });
    const fix = "use rounded-ui, the Theme's radius";
    expect(lintStyle(dir)).toEqual([
      `src/row.tsx:1: "rounded" is a hard-coded radius; ${fix}`,
      `src/row.tsx:1: "rounded-lg" is a hard-coded radius; ${fix}`,
      `src/row.tsx:1: "md:rounded-t-md" is a hard-coded radius; ${fix}`,
    ]);
  });

  it("fails hard-coded fonts, and passes the Theme's font and @ddash/ui's monospace", () => {
    const dir = plugin({ "row.tsx": `export const c = "font-mono font-serif font-sans font-semibold";\n` });
    expect(lintStyle(dir)).toEqual([
      'src/row.tsx:1: "font-serif" is a hard-coded font; use font-sans, the Theme\'s font, or font-mono',
    ]);
  });

  it("passes the Theme's radius scale", () => {
    const dir = plugin({ "row.tsx": `export const c = "rounded-ui-sm rounded-ui-lg rounded-t-ui-lg";\n` });
    expect(lintStyle(dir)).toEqual([]);
  });

  it("fails the shell-only nav and hero surfaces", () => {
    const dir = plugin({
      "row.tsx": `export const c = "bg-nav text-nav-strong hover:bg-nav-hover/50 border-nav-border bg-linear-to-b from-hero-from to-hero-to text-navy";\n`,
    });
    const fix = "the nav is the shell's and the hero is HeroCard's";
    expect(lintStyle(dir)).toEqual([
      `src/row.tsx:1: "bg-nav" is a shell-only surface; ${fix}`,
      `src/row.tsx:1: "text-nav-strong" is a shell-only surface; ${fix}`,
      `src/row.tsx:1: "hover:bg-nav-hover/50" is a shell-only surface; ${fix}`,
      `src/row.tsx:1: "border-nav-border" is a shell-only surface; ${fix}`,
      `src/row.tsx:1: "from-hero-from" is a shell-only surface; ${fix}`,
      `src/row.tsx:1: "to-hero-to" is a shell-only surface; ${fix}`,
    ]);
  });

  it("fails arbitrary Tailwind values", () => {
    const dir = plugin({
      "row.tsx": "const pad = 3;\nexport const c = `w-[13px] bg-(--brand) [color:red] [&>p]:p-2 mt-${pad}`;\n",
    });
    const fix = "use Tailwind's scale and the Theme tokens";
    expect(lintStyle(dir)).toEqual([
      `src/row.tsx:2: "w-[13px]" is an arbitrary value; ${fix}`,
      `src/row.tsx:2: "bg-(--brand)" is an arbitrary value; ${fix}`,
      `src/row.tsx:2: "[color:red]" is an arbitrary value; ${fix}`,
      `src/row.tsx:2: "[&>p]:p-2" is an arbitrary value; ${fix}`,
    ]);
  });

  it("passes brackets in text that is no class, such as an IPv6 listen", () => {
    const dir = plugin({
      "row.tsx": `export const listens = ["[::]:443", "[::1]:8080 ssl", "[2001:db8::1]:80"];\nexport const note = "Error: [object Object]";\n`,
    });
    expect(lintStyle(dir)).toEqual([]);
  });

  it("fails a design-system import other than @ddash/ui", () => {
    const dir = plugin({
      "row.tsx": `import * as Dialog from "@radix-ui/react-dialog";
import { Box } from "lucide-react";
import styled from "styled-components";
import "@ddash/ui/theme.css";
import "./row.css";
export { Button } from "@mui/material";
const lazy = () => import("antd");
`,
    });
    const fix = "build the UI from @ddash/ui";
    expect(lintStyle(dir)).toEqual([
      `src/row.tsx:1: imports @radix-ui/react-dialog; ${fix}`,
      `src/row.tsx:2: imports lucide-react; ${fix}`,
      `src/row.tsx:3: imports styled-components; ${fix}`,
      `src/row.tsx:4: imports @ddash/ui/theme.css; ${fix}`,
      `src/row.tsx:5: imports ./row.css; ${fix}`,
      `src/row.tsx:6: imports @mui/material; ${fix}`,
      `src/row.tsx:7: imports antd; ${fix}`,
    ]);
  });

  it("fails hard-coded colours, radii and fonts in a style prop", () => {
    const dir = plugin({
      "row.tsx": `export const Row = () => <div style={{ color: "var(--up)", borderRadius: 4, fontFamily: "Inter", width: "40%" }} />;\n`,
    });
    expect(lintStyle(dir)).toEqual([
      "src/row.tsx:1: style sets color; use @ddash/ui and Theme token classes",
      "src/row.tsx:1: style sets borderRadius; use @ddash/ui and Theme token classes",
      "src/row.tsx:1: style sets fontFamily; use @ddash/ui and Theme token classes",
    ]);
  });

  it("passes animations behind motion-safe:, its own keyframes among them", () => {
    const dir = plugin({
      "row.tsx": `export const c = ["motion-safe:animate-ping", "md:motion-safe:hover:animate-pulse", "motion-safe:animate-[hello-pulse_2s_ease-in-out_infinite]", "motion-reduce:animate-none"];\n`,
    });
    expect(lintStyle(dir)).toEqual([]);
  });

  it("fails an animation not behind motion-safe:", () => {
    const dir = plugin({ "row.tsx": `export const c = "animate-ping hover:animate-spin animate-[hello-pulse_2s]";\n` });
    const fix = "put it behind motion-safe:";
    expect(lintStyle(dir)).toEqual([
      `src/row.tsx:1: "animate-ping" animates without motion-safe:; ${fix}`,
      `src/row.tsx:1: "hover:animate-spin" animates without motion-safe:; ${fix}`,
      `src/row.tsx:1: "animate-[hello-pulse_2s]" animates without motion-safe:; ${fix}`,
    ]);
  });

  it("fails an arbitrary animation other than the Plugin's own keyframes", () => {
    const dir = plugin({ "row.tsx": `export const c = "motion-safe:animate-[spin_1s] motion-safe:animate-[docker-pulse_1s] motion-safe:animate-(--a)";\n` });
    const fix = "use Tailwind's scale and the Theme tokens";
    expect(lintStyle(dir)).toEqual([
      `src/row.tsx:1: "motion-safe:animate-[spin_1s]" is an arbitrary value; ${fix}`,
      `src/row.tsx:1: "motion-safe:animate-[docker-pulse_1s]" is an arbitrary value; ${fix}`,
      `src/row.tsx:1: "motion-safe:animate-(--a)" is an arbitrary value; ${fix}`,
    ]);
  });

  it("passes a Plugin stylesheet holding only the plugin.css import and its own keyframes", () => {
    const dir = plugin({
      "index.css": `/* The pulse on a fresh row. */
@import "@ddash/ui/plugin.css";

@keyframes hello-pulse {
  from { transform: scale(1); opacity: 1; }
  50%, 75% { transform: scale(1.1) translateY(-2px); opacity: var(--pulse-dim); --glow: var(--up); }
  to { transform: none; opacity: 0.6 }
}
@keyframes hello-fade { 0% { opacity: 0 } 100% { opacity: 1 } }
`,
    });
    expect(lintStyle(dir)).toEqual([]);
  });

  it("fails a Plugin stylesheet with selectors or other at-rules", () => {
    const fix = 'a Plugin\'s CSS may only @import "@ddash/ui/plugin.css" and hold @keyframes hello-<name>; style with @ddash/ui and Theme token classes';
    for (const extra of [".title { color: #f00; }", '@source "./";', '@import "./more.css";', "@media (min-width: 1px) { .flex { display: none } }"]) {
      const dir = plugin({ "index.css": `${indexCSS}${extra}\n` });
      expect(lintStyle(dir), extra).toEqual([`src/index.css: ${fix}`]);
    }
  });

  it("fails keyframes not named after the Plugin", () => {
    const dir = plugin({ "index.css": `${indexCSS}@keyframes spin { to { transform: rotate(1turn) } }\n@keyframes hello { to { opacity: 0 } }\n` });
    expect(lintStyle(dir)).toEqual([
      "src/index.css:2: @keyframes spin isn't named hello-<name>",
      "src/index.css:3: @keyframes hello isn't named hello-<name>",
    ]);
  });

  it("fails keyframes animating anything but transform, opacity and token variables", () => {
    const dir = plugin({
      "index.css": `${indexCSS}@keyframes hello-flash {
  from { color: red; transform: none }
  to { background-color: var(--up); opacity: 1 }
}
`,
    });
    const fix = "animate only transform, opacity and token variables";
    expect(lintStyle(dir)).toEqual([
      `src/index.css:3: @keyframes hello-flash animates color; ${fix}`,
      `src/index.css:4: @keyframes hello-flash animates background-color; ${fix}`,
    ]);
  });
});
