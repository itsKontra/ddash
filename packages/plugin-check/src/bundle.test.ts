import { mkdirSync, mkdtempSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { describe, expect, it } from "vitest";
import { checkBundle } from "./bundle.ts";

/** A vendor dir as the shell's build lays it out, with a stand-in @ddash/plugin. */
function vendor(): string {
  const dir = mkdtempSync(join(tmpdir(), "ddash-vendor-"));
  writeFileSync(join(dir, "ddash-plugin.js"), "export const definePlugin = (d) => d;\n");
  writeFileSync(join(dir, "react.js"), "export default {};\n");
  return dir;
}

function bundle(js: string): string {
  const dir = mkdtempSync(join(tmpdir(), "ddash-bundle-"));
  mkdirSync(join(dir, "dist"));
  writeFileSync(join(dir, "dist/index.js"), js);
  return join(dir, "dist/index.js");
}

describe("checkBundle", () => {
  it("imports the built bundle through the import map and passes a kept contract", async () => {
    const js = `import { definePlugin } from "@ddash/plugin";
import "react";
export default definePlugin({ nav: { order: 1 }, pages: [{ index: true }] });
`;
    expect(await checkBundle({ name: "docker", bundle: bundle(js), vendor: vendor() })).toEqual([]);
  });

  it("fails a bundle breaking the contract", async () => {
    const js = `import { definePlugin } from "@ddash/plugin";
export default definePlugin({ pages: [{ path: "/settings" }] });
`;
    expect(await checkBundle({ name: "docker", bundle: bundle(js), vendor: vendor() })).toEqual([
      'pages[0].path: "/settings" is outside /docker/*',
    ]);
  });

  it("fails a bundle that throws on import", async () => {
    const problems = await checkBundle({ name: "docker", bundle: bundle(`throw new Error("boom");`), vendor: vendor() });
    expect(problems).toHaveLength(1);
    expect(problems[0]).toMatch(/^importing the bundle failed: .*boom/);
  });

  it("fails a Plugin whose bundle isn't built", async () => {
    expect(await checkBundle({ name: "docker", bundle: "/nonexistent/dist/index.js", vendor: vendor() })).toEqual([
      "no built bundle at /nonexistent/dist/index.js: run `make build`",
    ]);
  });
});
