// The Plugin CI guards over a repo's plugins/*/ (each dir with a plugin.yaml,
// but a headless one's, which has no UI):
//
//     node packages/plugin-check/src/cli.ts contract [root]   # each built bundle (make build first)
//     node packages/plugin-check/src/cli.ts style [root]      # each Plugin's sources
//
// root defaults to the working directory. Exits 1 on any problem. Manifest
// names (matching their directory, clashing with core paths) are the Go
// catalogue's to check (internal/release).
import { existsSync, readdirSync, readFileSync } from "node:fs";
import { join, resolve } from "node:path";
import { checkBundle } from "./bundle.ts";
import { lintStyle } from "./style.ts";

const [command, rootArg] = process.argv.slice(2);
const root = resolve(rootArg ?? ".");

/** Whether plugin.yaml says the Plugin is headless: it has no UI to check. */
const headless = (manifest: string) => /^headless:\s*true\s*$/m.test(readFileSync(manifest, "utf8"));

const plugins = readdirSync(join(root, "plugins"), { withFileTypes: true })
  .filter((e) => e.isDirectory())
  .map((e) => e.name)
  .filter((name) => {
    const manifest = join(root, "plugins", name, "plugin.yaml");
    return existsSync(manifest) && !headless(manifest);
  })
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
