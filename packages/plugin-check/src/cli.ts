// The Plugin CI guards over a repo's plugins/*/ (each dir with a plugin.yaml):
//
//     node packages/plugin-check/src/cli.ts contract [root]   # each built bundle (make build first)
//     node packages/plugin-check/src/cli.ts style [root]      # each Plugin's sources
//
// root defaults to the working directory. Exits 1 on any problem. Manifest
// names (matching their directory, clashing with core paths) are the Go
// catalogue's to check (internal/release).
import { existsSync, readdirSync } from "node:fs";
import { join, resolve } from "node:path";
import { checkBundle } from "./bundle.ts";
import { lintStyle } from "./style.ts";

const [command, rootArg] = process.argv.slice(2);
const root = resolve(rootArg ?? ".");

const plugins = readdirSync(join(root, "plugins"), { withFileTypes: true })
  .filter((e) => e.isDirectory() && existsSync(join(root, "plugins", e.name, "plugin.yaml")))
  .map((e) => e.name)
  .sort();

let failed = false;
for (const name of plugins) {
  const dir = join(root, "plugins", name);
  let problems: string[];
  switch (command) {
    case "contract":
      problems = await checkBundle({ name, bundle: join(dir, "dist/index.js"), vendor: join(root, "web/dist/shell/vendor") });
      break;
    case "style":
      problems = lintStyle(dir);
      break;
    default:
      console.error("usage: cli.ts contract|style [root]");
      process.exit(2);
  }
  for (const p of problems) console.error(`plugins/${name}: ${p}`);
  failed ||= problems.length > 0;
}
if (failed) process.exit(1);
console.log(`${command}: ${plugins.length} Plugin(s) ok`);
