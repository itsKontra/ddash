// The vendor build: one ESM file per import-map package in
// dist/shell/vendor/ (react.js, react-jsx-runtime.js, ddash-plugin.js, ...),
// built together so they share one React. Runs after the shell's build.
import { importMapPackages, jsx, onLog, vendorFile } from "@ddash/vite-preset";
import { createRequire } from "node:module";
import { defineConfig, type Plugin } from "vite";

const require = createRequire(import.meta.url);
const prefix = "ddash-vendor:";

// React ships CommonJS, whose names an ESM `export *` can't re-export.
const commonJS = new Set(["react", "react/jsx-runtime", "react-dom", "react-dom/client"]);
const identifier = /^[A-Za-z_$][\w$]*$/;

/** One virtual entry per package, re-exporting all of it. */
function entries(): Plugin {
  return {
    name: "ddash-vendor-entries",
    resolveId: (id) => (id.startsWith(prefix) ? `\0${id}` : null),
    load(id) {
      if (!id.startsWith(`\0${prefix}`)) return null;
      const pkg = id.slice(prefix.length + 1);
      if (!commonJS.has(pkg)) return `export * from ${JSON.stringify(pkg)};`;
      const names = Object.keys(require(pkg)).filter((k) => k !== "default" && identifier.test(k));
      return [
        `import m from ${JSON.stringify(pkg)};`,
        "export default m;",
        `export const { ${names.join(", ")} } = m;`,
      ].join("\n");
    },
  };
}

export default defineConfig({
  plugins: [entries()],
  oxc: jsx,
  publicDir: false,
  define: { "process.env.NODE_ENV": JSON.stringify("production") },
  build: {
    outDir: "dist/shell/vendor",
    emptyOutDir: true,
    rolldownOptions: {
      input: Object.fromEntries(importMapPackages.map((p) => [vendorFile(p).replace(/\.js$/, ""), prefix + p])),
      preserveEntrySignatures: "strict",
      onLog,
      output: {
        format: "es",
        entryFileNames: "[name].js",
        chunkFileNames: "chunks/[name]-[hash].js",
      },
    },
  },
});
