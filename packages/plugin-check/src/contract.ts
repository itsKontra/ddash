// The Plugin contract, checked on what a built bundle's definePlugin(...)
// returns. Plain data in, problems out, so it runs in the loader process
// (load.ts) and in tests alike.

const isObject = (v: unknown): v is Record<string, unknown> => typeof v === "object" && v !== null && !Array.isArray(v);

/**
 * The problems with definition, the default export of Plugin name's bundle;
 * none when it keeps the contract.
 */
export function checkDefinition(name: string, definition: unknown): string[] {
  if (!isObject(definition)) return ["the bundle's default export must be definePlugin({ nav, pages, widgets })"];
  const problems: string[] = [];
  const { nav, pages, widgets } = definition;
  if (Array.isArray(nav)) problems.push("nav: at most one Nav Entry, not a list");
  if (pages !== undefined) checkRoutes(name, pages, "pages", problems);
  if (isObject(nav) && !(Array.isArray(pages) && pages.some((p) => isObject(p) && p.index === true))) {
    problems.push("nav: the Nav Entry leads to the index Page, but pages has no index route");
  }
  if (widgets !== undefined) checkWidgets(widgets, problems);
  return problems;
}

const sizes = ["S", "M", "L"];

function checkWidgets(widgets: unknown, problems: string[]) {
  if (!Array.isArray(widgets)) {
    problems.push("widgets: must be a list of Widgets");
    return;
  }
  const ids = new Set<unknown>();
  widgets.forEach((w, i) => {
    const here = `widgets[${i}]`;
    if (!isObject(w)) {
      problems.push(`${here}: must be a Widget object`);
      return;
    }
    if (typeof w.id !== "string" || w.id === "") problems.push(`${here}.id: must be a non-empty string`);
    else if (ids.has(w.id)) problems.push(`${here}.id: ${JSON.stringify(w.id)} is used twice`);
    ids.add(w.id);
    if (!Array.isArray(w.sizes) || w.sizes.length === 0) problems.push(`${here}.sizes: must list at least one of S, M and L`);
    else for (const s of w.sizes) if (!sizes.includes(s)) problems.push(`${here}.sizes: ${JSON.stringify(s)} isn't S, M or L`);
    if (!(Array.isArray(w.sizes) && sizes.includes(w.defaultSize as string) && w.sizes.includes(w.defaultSize))) {
      problems.push(`${here}.defaultSize: ${JSON.stringify(w.defaultSize)} isn't one of its sizes`);
    }
    if (typeof w.useStatus !== "function") problems.push(`${here}.useStatus: must be a hook returning the Widget's Status`);
  });
}

/**
 * Pages are react-router routes mounted at /<name>/*: a relative path stays
 * under it, an absolute one must start there.
 */
function checkRoutes(name: string, routes: unknown, at: string, problems: string[]) {
  if (!Array.isArray(routes)) {
    problems.push(`${at}: must be a list of routes`);
    return;
  }
  routes.forEach((route, i) => {
    const here = `${at}[${i}]`;
    if (!isObject(route)) {
      problems.push(`${here}: must be a route object`);
      return;
    }
    const { path, children } = route;
    if (typeof path === "string" && !under(name, path)) problems.push(`${here}.path: ${JSON.stringify(path)} is outside /${name}/*`);
    if (children !== undefined) checkRoutes(name, children, `${here}.children`, problems);
  });
}

function under(name: string, path: string): boolean {
  if (path.split("/").includes("..")) return false;
  return !path.startsWith("/") || path === `/${name}` || path.startsWith(`/${name}/`);
}
