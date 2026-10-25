import { mkdtempSync, readFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { build, createServer, type ViteDevServer } from "vite";
import { afterAll, beforeAll, describe, expect, it } from "vitest";
import { importMap, importMapPackages, pluginConfig, vendorFromSource } from "./index";

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
