// The contract check on a built bundle: import it the way the browser does,
// with the import map pointing at the shell's vendor ESM, and check what its
// definePlugin(...) returns.
import { importMapPackages, vendorFile } from "@ddash/vite-preset";
import { fork } from "node:child_process";
import { existsSync } from "node:fs";
import { join } from "node:path";
import { pathToFileURL } from "node:url";

export interface BundleOptions {
  /** The Plugin's name, as its directory and manifest give it. */
  name: string;
  /** The built bundle: plugins/<name>/dist/index.js. */
  bundle: string;
  /** The shell's vendor ESM, one file per import-map package: web/dist/shell/vendor. */
  vendor: string;
}

const timeout = 30_000;

/**
 * The problems with a built bundle; none when it keeps the contract. Each
 * bundle is imported in its own Node process (load.ts), so one Plugin's
 * top-level code can't affect another's check.
 */
export async function checkBundle({ name, bundle, vendor }: BundleOptions): Promise<string[]> {
  if (!existsSync(bundle)) return [`no built bundle at ${bundle}: run \`make build\``];
  const imports = Object.fromEntries(importMapPackages.map((p) => [p, pathToFileURL(join(vendor, vendorFile(p))).href]));
  const child = fork(join(import.meta.dirname, "load.ts"), [name, bundle, JSON.stringify(imports)], { stdio: "inherit", timeout });
  return new Promise((resolve, reject) => {
    let problems: string[] | undefined;
    child.on("message", (m) => (problems = m as string[]));
    child.on("error", reject);
    child.on("exit", (code, signal) => {
      resolve(problems ?? [`importing the bundle failed: the loader exited (${signal ?? `code ${code}`}) without a result`]);
    });
  });
}
