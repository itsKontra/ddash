import { mkdtempSync, readFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { build, createServer, type ViteDevServer } from "vite";
import { afterAll, beforeAll, describe, expect, it } from "vitest";
import { importMap, importMapPackages, pluginConfig, vendorFromSource } from "./index";

const fixture = fileURLToPath(new URL("../testdata/hello", import.meta.url));

/** Builds the Plugin at dir with pluginConfig into a temp dir, which it returns. */
async function buildPlugin(dir: string): Promise<string> {
  const out = mkdtempSync(join(tmpdir(), "ddash-preset-"));
  await build({ ...pluginConfig(dir), configFile: false, logLevel: "silent", build: { ...pluginConfig(dir).build, outDir: out } });
  return out;
}

const buildCSS = async (dir: string) => readFileSync(join(await buildPlugin(dir), "index.css"), "utf8");

describe("pluginConfig", () => {
  let js: string;
  let css: string;

  beforeAll(async () => {
    const out = await buildPlugin(fixture);
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

  it("ships the Plugin's own keyframes", () => {
    expect(css).toMatch(/@keyframes hello-pulse\s*\{/);
  });

  it("ships no utilities, which are the shell's to generate and order", () => {
    // The fixture's hidden lg:flex text-up p-3 and the animate-[hello-pulse_...]
    // applying its keyframes all come from the shell's stylesheet.
    for (const cls of [".hidden", ".text-up", ".p-3", ".lg\\:flex", "animate-"]) expect(css).not.toContain(cls);
    expect(css).not.toContain("@layer utilities{");
  });

  it("ships no utilities even when a Plugin's stylesheet @sources its sources: plugin.css carries only the tokens", async () => {
    const sourced = await buildCSS(fileURLToPath(new URL("../testdata/sourced", import.meta.url)));
    for (const cls of [".hidden", ".text-up", ".p-3", ".lg\\:flex"]) expect(sourced).not.toContain(cls);
  }, 60_000);

  it("leaves preflight out", () => {
    expect(css).not.toMatch(/box-sizing:\s*border-box/);
    expect(css).not.toContain("@layer base{");
  });
});

describe("vendorFromSource", () => {
  const source = fileURLToPath(new URL("../testdata/vendor-source/ui.ts", import.meta.url));
  let server: ViteDevServer;
  let base: string;

  beforeAll(async () => {
    server = await createServer({
      configFile: false,
      logLevel: "silent",
      root: dirname(source),
      plugins: [vendorFromSource({ "@ddash/ui": source })],
      server: { port: 0, host: "127.0.0.1" },
    });
    await server.listen();
    base = server.resolvedUrls!.local[0]!.replace(/\/$/, "");
  }, 60_000);
  afterAll(() => server?.close());

  it("serves the dev Release's vendor file of a package as its source, transformed on each request", async () => {
    const shim = await fetch(`${base}/vendor/dev/ddash-ui.js`);
    expect(shim.headers.get("content-type")).toMatch(/javascript/);
    const target = /export \* from "([^"]+)"/.exec(await shim.text())?.[1];
    expect(target).toBeDefined();

    const module = await (await fetch(base + target)).text();
    expect(module).toContain("hello from source");
    expect(module).not.toContain(": string"); // TypeScript, transformed
  });

  it("leaves the vendor files of other packages alone", async () => {
    const res = await fetch(`${base}/vendor/dev/react.js`);
    expect(await res.text()).not.toContain("export * from");
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
