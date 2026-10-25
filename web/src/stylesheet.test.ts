// @vitest-environment node
/// <reference types="node" />
import tailwindcss from "@tailwindcss/vite";
import { mkdirSync, rmSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import { build, type Rolldown } from "vite";
import { beforeAll, expect, it } from "vitest";

const web = fileURLToPath(new URL("..", import.meta.url));
// A Plugin's sources that only this test has, so its classes are used
// nowhere else. No plugin.yaml, so no Release or lint ever sees it.
const probe = join(web, "../plugins/zz-shell-stylesheet-probe");

// Spelled out at run time: this file is among the shell's own sources.
const size = `size-${97}`;
const responsive = ["lg", "inline-table"].join(":");
const keyframes = ["probe", "pulse"].join("-");
const animation = `motion-safe:animate-[${keyframes}_2s_infinite]`;

let css: string;

beforeAll(async () => {
  mkdirSync(join(probe, "src"), { recursive: true });
  writeFileSync(
    join(probe, "src/index.tsx"),
    `export const Probe = () => <div className="hidden ${responsive} ${size} ${animation}" />;\n`,
  );
  try {
    const out = (await build({
      configFile: false,
      root: web,
      logLevel: "silent",
      plugins: [tailwindcss()],
      build: { write: false, rolldownOptions: { input: join(web, "src/index.css") } },
    })) as Rolldown.RolldownOutput;
    css = out.output.flatMap((o) => (o.type === "asset" && o.fileName.endsWith(".css") ? [String(o.source)] : [])).join("");
  } finally {
    rmSync(probe, { recursive: true, force: true });
  }
}, 60_000);

it("generates the classes only a Plugin uses, an animation of its own keyframes among them", () => {
  expect(css).toContain(`.${size}{`);
  expect(css).toContain(`.motion-safe\\:animate-\\[${keyframes}_2s_infinite\\]{`);
});

it("orders a Plugin's responsive classes after its base ones, so a class hidden below lg shows from lg up", () => {
  const hidden = css.indexOf(".hidden{");
  const lg = css.indexOf(".lg\\:inline-table{");
  expect(hidden).toBeGreaterThanOrEqual(0);
  expect(lg).toBeGreaterThan(hidden);
});
