// The shell's build (into dist/shell, embedded by package web) and its dev
// server for `make dev`, which proxies the core's paths to it.
import tailwindcss from "@tailwindcss/vite";
import { importMap, importMapPackages, isImportMapped, jsx, onLog } from "@ddash/vite-preset";
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
      // PROTOTYPE: serve @ddash/ui from source in dev (see uiFromSource).
      if (id === "@ddash/ui" && this.environment.mode === "dev") return uiSource;
      // PROTOTYPE (prototype/ui-screens): @ddash/plugin too, so its ConfirmDialog is the restyled one.
      if (id === "@ddash/plugin" && this.environment.mode === "dev") return pluginSource;
      if (!isImportMapped(id)) return null;
      // The dev server rewrites bare imports, so point it at the vendor URL
      // the import map gives; a build leaves the bare import to the map.
      return { id: this.environment.mode === "dev" ? importMap(release).imports[id] : id, external: true };
    },
    transformIndexHtml: {
      order: "pre",
      handler: () => [
        { tag: "script", attrs: { type: "importmap" }, children: JSON.stringify(importMap(release)), injectTo: "head-prepend" },
      ],
    },
  };
}

// PROTOTYPE: @ddash/ui from source, so its edits show without rebuilding
// the vendor bundle; Plugins' /vendor/dev/ddash-ui.js redirects to the
// same module, so shell and Plugins still share one instance.
const uiSource = new URL("../packages/ui/src/index.tsx", import.meta.url).pathname;
const pluginSource = new URL("../packages/plugin/src/index.ts", import.meta.url).pathname;
function uiFromSource(): Plugin {
  return {
    name: "prototype-ui-from-source",
    configureServer(server) {
      server.middlewares.use((req, res, next) => {
        const to = req.url === "/vendor/dev/ddash-ui.js" ? uiSource : req.url === "/vendor/dev/ddash-plugin.js" ? pluginSource : undefined;
        if (!to) return next();
        res.statusCode = 302;
        res.setHeader("Location", `/@fs${to}`);
        res.end();
      });
    },
  };
}

export default defineConfig({
  plugins: [uiFromSource(), importMapped(), tailwindcss()],
  oxc: jsx,
  // No pre-bundling: it would bundle its own React into deps such as
  // lucide-react. The shell's other deps are ESM and served as they are.
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
    proxy: { "/api": core, "/plugins": core, "/vendor": core },
  },
});
