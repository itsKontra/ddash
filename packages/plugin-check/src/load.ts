// The loader process checkBundle forks: `load.ts <name> <bundle> <imports>`,
// imports being the import map ({ specifier: file URL }) as JSON. It imports
// the bundle with those specifiers mapped and sends back checkDefinition's
// problems.
import { registerHooks } from "node:module";
import { pathToFileURL } from "node:url";
import { checkDefinition } from "./contract.ts";

const [name, bundle, json] = process.argv.slice(2);
const imports = new Map(Object.entries(JSON.parse(json) as Record<string, string>));

registerHooks({
  resolve(specifier, context, next) {
    const url = imports.get(specifier);
    return url ? { url, shortCircuit: true } : next(specifier, context);
  },
});

let problems: string[];
try {
  const mod = (await import(pathToFileURL(bundle).href)) as { default?: unknown };
  problems = checkDefinition(name, mod.default);
} catch (err) {
  problems = [`importing the bundle failed: ${err instanceof Error ? err.message : String(err)}`];
}
// A bundle may leave timers or sockets behind; the answer is all we need.
process.send!(problems, () => process.exit(0));
