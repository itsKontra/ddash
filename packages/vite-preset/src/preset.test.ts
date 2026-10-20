import { mkdtempSync, readFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import { build } from "vite";
import { beforeAll, describe, expect, it } from "vitest";
import { importMap, importMapPackages, pluginConfig } from "./index";

const fixture = fileURLToPath(new URL("../testdata/hello", import.meta.url));

describe("pluginConfig", () => {
  let js: string;
  let css: string;

  beforeAll(async () => {
    const out = mkdtempSync(join(tmpdir(), "ddash-preset-"));
    await build({ ...pluginConfig(fixture), configFile: false, logLevel: "silent", build: { ...pluginConfig(fixture).build, outDir: out } });
    js = readFileSync(join(out, "index.js"), "utf8");
    css = readFileSync(join(out, "index.css"), "utf8");
  }, 60_000);

  it("builds one ESM bundle that imports the import-map packages instead of bundling them", () => {
    for (const pkg of ["react/jsx-runtime", "@ddash/plugin", "@ddash/ui", "react"]) {
      expect(js).toMatch(new RegExp(`from\\s*["']${pkg}["']`));
    }
    expect(js).not.toContain("useState=function"); // React itself isn't inside
    expect(js).toMatch(/export\s*\{[^}]*as default/);
  });

  it("builds the Plugin's own Tailwind CSS from its own sources, on the Theme tokens", () => {
    expect(css).toContain(".text-up");
    expect(css).toContain("var(--up)");
    expect(css).toContain(".p-3");
    // Classes only @ddash/ui or other sources use aren't the Plugin's to ship.
    expect(css).not.toContain(".bg-maint");
    expect(css).not.toContain(".animate-ping");
  });

  it("leaves preflight out", () => {
    expect(css).not.toMatch(/box-sizing:\s*border-box/);
    expect(css).not.toContain("@layer base{");
  });
});

describe("importMap", () => {
  it("maps every import-map package into /vendor/<release>/", () => {
    expect(importMap("v1.2.3")).toEqual({
      imports: {
        react: "/vendor/v1.2.3/react.js",
        "react/jsx-runtime": "/vendor/v1.2.3/react-jsx-runtime.js",
        "react-dom": "/vendor/v1.2.3/react-dom.js",
        "react-dom/client": "/vendor/v1.2.3/react-dom-client.js",
        "react-router": "/vendor/v1.2.3/react-router.js",
        "@ddash/ui": "/vendor/v1.2.3/ddash-ui.js",
        "@ddash/plugin": "/vendor/v1.2.3/ddash-plugin.js",
      },
    });
    expect(Object.keys(importMap("x").imports)).toEqual(importMapPackages);
  });
});
