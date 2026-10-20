import { mkdirSync, mkdtempSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { describe, expect, it } from "vitest";
import { lintStyle } from "./style.ts";

const indexCSS = '@import "@ddash/ui/plugin.css";\n@source "./";\n';

/** A Plugin dir holding files (path under src/ → contents), plus its index.css. */
function plugin(files: Record<string, string>): string {
  const dir = mkdtempSync(join(tmpdir(), "ddash-style-"));
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
import { Badge, cx, StatusDot } from "@ddash/ui";
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

  it("fails hard-coded fonts", () => {
    const dir = plugin({ "row.tsx": `export const c = "font-mono font-serif font-sans font-semibold";\n` });
    expect(lintStyle(dir)).toEqual([
      'src/row.tsx:1: "font-mono" is a hard-coded font; use font-sans, the Theme\'s font',
      'src/row.tsx:1: "font-serif" is a hard-coded font; use font-sans, the Theme\'s font',
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

  it("fails a Plugin stylesheet with its own rules", () => {
    const dir = plugin({ "index.css": `${indexCSS}\n.title { color: #f00; }\n` });
    expect(lintStyle(dir)).toEqual([
      'src/index.css: a Plugin\'s CSS may only @import "@ddash/ui/plugin.css" and @source its sources; style with @ddash/ui and Theme token classes',
    ]);
  });
});
