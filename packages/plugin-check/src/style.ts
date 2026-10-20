// The style lint: a Plugin is built only from @ddash/ui and Theme tokens.
// It reads every source under the Plugin's src/ (the files its Tailwind
// @source scans) and reports hard-coded colours, radii and fonts, arbitrary
// Tailwind values, and design-system imports other than @ddash/ui.
import { readdirSync, readFileSync } from "node:fs";
import { join, relative } from "node:path";
import { parseSync, Visitor } from "vite";

const scripts = /\.[cm]?[jt]sx?$/;

/** The problems in the Plugin at dir, as "src/<file>:<line>: <problem>"; none when it keeps the design system. */
export function lintStyle(dir: string): string[] {
  const problems: string[] = [];
  for (const file of sources(join(dir, "src"))) {
    const rel = relative(dir, file);
    const text = readFileSync(file, "utf8");
    if (scripts.test(file)) lintScript(rel, text, problems);
    else if (file.endsWith(".css") && !pluginCSS.test(text.replace(/\/\*[\s\S]*?\*\//g, ""))) {
      problems.push(`${rel}: a Plugin's CSS may only @import "@ddash/ui/plugin.css" and @source its sources; style with @ddash/ui and Theme token classes`);
    }
  }
  return problems;
}

// A Plugin's stylesheet, comments aside: only these two at-rules.
const pluginCSS = /^(\s*(@import\s+["']@ddash\/ui\/plugin\.css["']|@source\s+(not\s+)?["'][^"']*["'])\s*;)*\s*$/;

/** Every file under dir, sorted, recursively. */
function sources(dir: string): string[] {
  return readdirSync(dir, { recursive: true, withFileTypes: true })
    .filter((e) => e.isFile())
    .map((e) => join(e.parentPath, e.name))
    .sort();
}

function lintScript(file: string, text: string, problems: string[]) {
  const { program, errors } = parseSync(file, text);
  if (errors.length > 0) {
    problems.push(`${file}: ${errors[0].message}`);
    return;
  }
  const line = (offset: number) => text.slice(0, offset).split("\n").length;
  const at = (offset: number, problem: string) => problems.push(`${file}:${line(offset)}: ${problem}`);
  const check = (value: string, offset: number) => {
    for (const p of checkString(value)) at(offset, p);
  };
  const source = (node: { source?: { value: unknown } | null | object; start: number }) => {
    const s = node.source && "value" in node.source ? node.source.value : undefined;
    if (typeof s === "string" && designSystem(s)) at(node.start, `imports ${s}; build the UI from @ddash/ui`);
  };
  new Visitor({
    ImportDeclaration: source,
    ExportNamedDeclaration: source,
    ExportAllDeclaration: source,
    ImportExpression: source,
    JSXAttribute(node) {
      if (node.name.type !== "JSXIdentifier" || node.name.name !== "style") return;
      const value = node.value?.type === "JSXExpressionContainer" ? node.value.expression : undefined;
      if (value?.type !== "ObjectExpression") return;
      for (const p of value.properties) {
        if (p.type !== "Property") continue;
        const key = p.key.type === "Identifier" ? p.key.name : p.key.type === "Literal" ? String(p.key.value) : "";
        if (styleProperty.test(key)) at(p.start, `style sets ${key}; use @ddash/ui and Theme token classes`);
      }
    },
    Literal(node) {
      if (typeof node.value === "string") check(node.value, node.start);
    },
    TemplateElement(node) {
      check(node.value.cooked ?? node.value.raw, node.start);
    },
  }).visit(program);
}

// Style props a Plugin leaves to @ddash/ui and the Theme tokens.
const styleProperty = /colou?r|background|fill|stroke|radius|font|shadow|^border$|^outline$/i;

// UI kits, icon sets and CSS-in-JS: the design system is @ddash/ui alone.
const designSystems = [
  /^@ddash\/ui\/./, // its internals and stylesheets; the shell and plugin.css load those
  /^@(radix-ui|mui|material-ui|chakra-ui|headlessui|mantine|ark-ui|base-ui-components|base-ui|react-aria|nextui-org|heroui|ant-design|fluentui|blueprintjs|emotion|stitches|vanilla-extract|heroicons|phosphor-icons|tabler|fortawesome|shadcn|tailwindcss)\//,
  /^(radix-ui|react-aria|react-aria-components|antd|react-bootstrap|bootstrap|primereact|semantic-ui-react|daisyui|flowbite|flowbite-react|shadcn|styled-components|tailwindcss|lucide|lucide-react|react-icons)(\/|$)/,
];
const stylesheet = /\.(css|scss|sass|less|styl)(\?.*)?$/;

function designSystem(specifier: string): boolean {
  if (stylesheet.test(specifier)) return specifier !== "./index.css";
  return designSystems.some((re) => re.test(specifier));
}

const colour = "use a Theme token class (bg-card, text-down, ...)";
const palette =
  "red|orange|amber|yellow|lime|green|emerald|teal|cyan|sky|blue|indigo|violet|purple|fuchsia|pink|rose|slate|gray|zinc|neutral|stone|mauve|olive|mist|taupe";
const colourUtility = new RegExp(
  "^(bg|text|border(-[xytrblse])?|divide|outline|ring|ring-offset|inset-ring|shadow|inset-shadow|drop-shadow|text-shadow|accent|caret|fill|stroke|decoration|placeholder|from|via|to)-" +
    `(black|white|(${palette})-\\d{2,3})(/[\\w.]+)?$`,
);
const hex = /^#([0-9a-f]{3,4}|[0-9a-f]{6}|[0-9a-f]{8})$/i;
const colourFunction = /\b(rgba?|hsla?|hwb|lab|lch|oklab|oklch|color)\(/i;

// rounded, rounded-t, rounded-lg, rounded-tl-md, ...: the value (if any) in group 1.
const radiusUtility = /^rounded(?:-(?:[trblse]|tl|tr|br|bl|ss|se|es|ee))?(?:-(.+))?$/;
// The Theme's radius, and the two that aren't a radius choice: a circle and none.
const radii = new Set(["ui", "full", "none"]);
const radius = "use rounded-ui, the Theme's radius";

const fontUtility = /^font-(mono|serif)$/;
const font = "use font-sans, the Theme's font";

// w-[13px], bg-[#fff]/50, bg-(--brand), bg-up/[.5], [color:red].
const arbitraryUtility = /^-?[a-z][\w-]*-(\[.+\]|\(.+\))(\/.+)?$|\/(\[.+\]|\(.+\))$|^\[[a-z-]+:.+\]$/;
const arbitrary = "use Tailwind's scale and the Theme tokens";

// What a Tailwind class's utility starts with: a letter (a negative value
// has a "-" first) or an arbitrary property's "[name:".
const classLike = /^-?[a-z]|^\[[a-z-]+:/;

/** The problems in one string literal: each class (or colour) in it is checked. */
function checkString(value: string): string[] {
  if (colourFunction.test(value)) return [`${JSON.stringify(value.trim())} is a hard-coded colour; ${colour}`];
  const problems: string[] = [];
  const report = (token: string, what: string, fix: string) => problems.push(`${JSON.stringify(token)} is ${what}; ${fix}`);
  for (const token of value.split(/\s+/)) {
    if (token === "") continue;
    const start = splitVariants(token);
    const utility = token.slice(start).replace(/^!|!$/g, "");
    if (utility === "") continue;
    if (colourUtility.test(utility) || hex.test(token)) report(token, "a hard-coded colour", colour);
    else if (!classLike.test(utility)) continue; // text, e.g. a "[::]:443" listen
    else if (arbitraryUtility.test(utility) || /[[(]/.test(token.slice(0, start))) report(token, "an arbitrary value", arbitrary);
    else if (hardCodedRadius(utility)) report(token, "a hard-coded radius", radius);
    else if (fontUtility.test(utility)) report(token, "a hard-coded font", font);
  }
  return problems;
}

function hardCodedRadius(utility: string): boolean {
  const m = radiusUtility.exec(utility);
  return m !== null && !radii.has(m[1] ?? ""); // bare rounded is Tailwind's default radius
}

/** Where a class's utility starts, after its variants (hover:, dark:, [&>p]:, ...). */
function splitVariants(token: string): number {
  let depth = 0;
  let start = 0;
  for (let i = 0; i < token.length; i++) {
    const c = token[i];
    if (c === "[" || c === "(") depth++;
    else if (c === "]" || c === ")") depth--;
    else if (c === ":" && depth === 0) start = i + 1;
  }
  return start;
}
