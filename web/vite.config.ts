// The shell's build (into dist/shell, embedded by package web) and its dev
// server for `make dev`, which proxies the core's paths to it.
import tailwindcss from "@tailwindcss/vite";
import { importMap, importMapPackages, isImportMapped, jsx, onLog, vendorFromSource } from "@ddash/vite-preset";
import { createRequire } from "node:module";
import { defineConfig, type Plugin } from "vite";

// Where `make dev` runs the core.
const core = process.env.DDASH_CORE ?? "http://127.0.0.1:8080";

/** Must match core.ReleasePlaceholder: the core puts the running Release there. */
const releasePlaceholder = "__DDASH_RELEASE__";

/**
 * Puts the static import map into index.html and leaves its packages to it:
 * the shell imports React, react-router, @ddash/ui and @ddash/plugin from
 * /vendor/<release>/, exactly as Plugins do, so all share one instance.
 * The dev server serves `make dev`'s Release, dev.
 */
function importMapped(): Plugin {
  let release = releasePlaceholder;
  return {
    name: "ddash-import-map",
    // Before Vite's own resolver, which would find them in node_modules.
    enforce: "pre",
    configResolved(config) {
      if (config.command === "serve") release = "dev";
    },
    resolveId(id) {
      if (!isImportMapped(id)) return null;
      // The dev server rewrites bare imports, so point it at the vendor URL
      // the import map gives; a build leaves the bare import to the map.
      return { id: this.environment.mode === "dev" ? importMap(release).imports[id] : id, external: true };
    },
    transformIndexHtml: {
      order: "pre",
      handler: () => [
        { tag: "script", attrs: { type: "importmap" }, children: JSON.stringify(importMap(release)), injectTo: "head-prepend" },
        // @ddash/ui's Icon puts the Release in its icon URLs, so they cache immutably.
        { tag: "meta", attrs: { name: "ddash-release", content: release }, injectTo: "head-prepend" },
      ],
    },
  };
}

const require = createRequire(import.meta.url);

export default defineConfig({
  // In `make dev`, @ddash/ui and @ddash/plugin come from source, so their
  // edits show in the shell and every Plugin on reload.
  plugins: [
    vendorFromSource({ "@ddash/ui": require.resolve("@ddash/ui"), "@ddash/plugin": require.resolve("@ddash/plugin") }),
    importMapped(),
    tailwindcss(),
  ],
  oxc: jsx,
  // No pre-bundling: it would bundle its own React into deps that import
  // it. The shell's other deps are ESM and served as they are.
  optimizeDeps: { noDiscovery: true, include: [], exclude: importMapPackages },
  build: {
    outDir: "dist/shell",
    emptyOutDir: true,
    rolldownOptions: { external: isImportMapped, onLog },
  },
  server: {
    // Pre-transforming would try to load /vendor/dev/*.js from disk; the
    // browser gets them through the proxy.
    preTransformRequests: false,
    // PROTOTYPE: a second `make dev` beside another one.
    port: Number(process.env.DDASH_SHELL_PORT ?? 5173),
    proxy: { "/api": core, "/plugins": core, "/vendor": core },
  },
});
