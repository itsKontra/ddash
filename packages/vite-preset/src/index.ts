// The shared Vite config. Plugins build with pluginConfig; the shell and
// the vendor build share importMapPackages, so every UI runs on one React,
// one react-router, one @ddash/ui and one @ddash/plugin.
import tailwindcss from "@tailwindcss/vite";
import { join } from "node:path";
import type { UserConfig } from "vite";

/**
 * The packages the shell's static import map serves as ESM singletons from
 * /vendor/<release>/. Plugins and the shell never bundle them.
 */
export const importMapPackages = [
  "react",
  "react/jsx-runtime",
  "react-dom",
  "react-dom/client",
  "react-router",
  "@ddash/ui",
  "@ddash/plugin",
];

/** The vendor file serving pkg: react/jsx-runtime is react-jsx-runtime.js. */
export function vendorFile(pkg: string): string {
  return `${pkg.replace(/^@/, "").replaceAll("/", "-")}.js`;
}

/** The import map, with its URLs on release. */
export function importMap(release: string): { imports: Record<string, string> } {
  return { imports: Object.fromEntries(importMapPackages.map((p) => [p, `/vendor/${release}/${vendorFile(p)}`])) };
}

/** Whether an import is one the import map serves. */
export const isImportMapped = (id: string) => importMapPackages.includes(id);

/**
 * Drops the "use client" warnings TanStack Query and react-router's
 * sources raise: directives mean nothing in a browser bundle.
 */
export const onLog: NonNullable<NonNullable<NonNullable<UserConfig["build"]>["rolldownOptions"]>["onLog"]> = (level, log, handler) => {
  if (log.code !== "MODULE_LEVEL_DIRECTIVE") handler(level, log);
};

/** Shared by every build: the JSX runtime is always the production one. */
export const jsx: UserConfig["oxc"] = { jsx: { runtime: "automatic", development: false } };

/**
 * A Plugin's build, for the Plugin in dir (its vite.config.ts is
 * `export default pluginConfig(import.meta.dirname)`):
 *
 * - src/index.tsx, whose default export is definePlugin(...), becomes one
 *   ESM file, dist/index.js, importing the import-map packages by name;
 * - the CSS it imports (src/index.css: `@import "@ddash/ui/plugin.css";`
 *   and the Plugin's own @keyframes) becomes dist/index.css. It holds no
 *   utilities: the shell's build generates every Plugin's classes.
 *
 * `vite build --watch` rebuilds it in place for `make dev`.
 */
export function pluginConfig(dir: string): UserConfig {
  return {
    root: dir,
    plugins: [tailwindcss()],
    oxc: jsx,
    define: { "process.env.NODE_ENV": JSON.stringify("production") },
    build: {
      outDir: join(dir, "dist"),
      emptyOutDir: true,
      lib: {
        entry: join(dir, "src/index.tsx"),
        formats: ["es"],
        fileName: () => "index.js",
        cssFileName: "index",
      },
      rolldownOptions: { external: isImportMapped, onLog },
    },
  };
}
