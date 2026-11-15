import { act, cleanup, fireEvent, render, screen, within } from "@testing-library/react";
import { createQueryClient, definePlugin, type Command, type Concern, type PluginDefinition } from "@ddash/plugin";
import { settle, tracked } from "@ddash/plugin/testing";
import { useEffect } from "react";
import { MemoryRouter, useLocation } from "react-router";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { App } from "./app";

// An installed Plugin as ListInstalledPlugins returns it.
const installed = (name: string, title: string) => ({
  name,
  title,
  description: "",
  icon: "puzzle",
  author: "itsKontra",
  requires: [`${name}-api`],
  script: `/plugins/${name}/v1.2.3/index.js`,
  style: "",
});

let plugins: [ReturnType<typeof installed>, PluginDefinition][];

beforeEach(() => {
  plugins = [];
  document.documentElement.classList.remove("dark");
  window.localStorage.clear();
});

afterEach(cleanup);

const fetchFake = async (input: RequestInfo | URL) => {
  switch (String(input).split("/").pop()) {
    case "ListInstalledPlugins":
      return Response.json({ plugins: plugins.map(([p]) => p) });
    case "GetLayout":
      return Response.json({ layout: { entries: [], seen: [] } });
    case "ListAuditLog":
      return Response.json({ entries: [] });
    default:
      return Response.json({});
  }
};

// Where the router is, as the Page under test shows it.
function Where() {
  return <output aria-label="Path">{useLocation().pathname}</output>;
}

async function boot(path: string) {
  const bundles = new Map(plugins.map(([p, b]) => [p.script, b]));
  render(
    <MemoryRouter initialEntries={[path]}>
      <App client={createQueryClient()} fetch={tracked(vi.fn(fetchFake))} reload={vi.fn()} importBundle={async (url) => ({ default: bundles.get(url) })} />
      <Where />
    </MemoryRouter>,
  );
  await settle();
}

const press = (key: string, init: KeyboardEventInit = {}) => fireEvent.keyDown(document.activeElement ?? document.body, { key, ...init });
const palette = () => screen.queryByRole("dialog", { name: "Command palette" });
const box = () => within(palette()!).getByRole("combobox", { name: "Search or run" });
// What an option reads, its decoration (aria-hidden) left out.
const said = (el: Element) => {
  const copy = el.cloneNode(true) as Element;
  copy.querySelectorAll("[aria-hidden]").forEach((n) => n.remove());
  return copy.textContent;
};
const options = () => within(palette()!).queryAllByRole("option").map(said);
// The option the combobox announces as active.
const active = () => document.getElementById(box().getAttribute("aria-activedescendant") ?? "");
const type = async (text: string) => {
  fireEvent.change(box(), { target: { value: text } });
  await settle();
};
const path = () => screen.getByRole("status", { name: "Path" }).textContent;

const docker = (Page = () => <input aria-label="Filter" />): [ReturnType<typeof installed>, PluginDefinition] => [
  installed("docker", "Docker"),
  definePlugin({ nav: { order: 50 }, pages: [{ index: true, Component: Page }] }),
];

it("opens with Ctrl K on a Plugin's Page, opens an App by the keyboard alone, and gives the focus back", async () => {
  const opened = vi.fn();
  const commands: Command[] = [
    { id: "open:grafana", title: "Open Grafana", hint: "grafana.home.example", icon: { app: "Grafana", url: "https://grafana.home.example" }, run: () => opened("grafana") },
    { id: "open:jellyfin", title: "Open Jellyfin", run: () => opened("jellyfin") },
  ];
  plugins = [docker(), [installed("apps", "Apps"), definePlugin({ useCommands: () => commands })]];
  await boot("/docker");
  const filter = screen.getByRole("textbox", { name: "Filter" });
  filter.focus();

  // Ctrl K works even from a text field.
  press("k", { ctrlKey: true });
  await settle();
  expect(palette()?.getAttribute("aria-modal")).toBe("true");
  expect(document.activeElement).toBe(box());
  expect(active()?.getAttribute("aria-selected")).toBe("true");

  await type("graf");
  expect(options()).toEqual(["Open Grafanagrafana.home.example"]);
  expect(active()?.textContent).toMatch(/^Open Grafana/);
  press("Enter");
  await settle();

  expect(opened).toHaveBeenCalledExactlyOnceWith("grafana");
  expect(palette()).toBeNull();
  expect(document.activeElement).toBe(filter);
});

it("reaches a Page with / and the arrows, and announces the active option as it moves", async () => {
  plugins = [docker(() => <p>Docker page</p>)];
  await boot("/docker");

  press("/");
  await settle();
  // Empty: the Pages, in the sidebar's order, then the shell's Actions.
  expect(options()).toEqual(["Overview", "Docker", "Plugins", "Settings", "Audit Log", "Switch to dark mode"]);
  expect(within(palette()!).getByRole("group", { name: "Go to" })).toBeTruthy();
  expect(active()?.textContent).toBe("Overview");
  press("ArrowDown");
  press("ArrowDown");
  expect(active()?.textContent).toBe("Plugins");
  press("ArrowUp");
  press("ArrowUp");
  press("ArrowUp");
  // It wraps.
  expect(active()?.textContent).toBe("Switch to dark mode");

  await type("audit");
  expect(screen.getByRole("status", { name: "" }).textContent).toBe("1 result");
  press("Enter");
  await settle();
  expect(path()).toBe("/settings/audit-log");
});

it("doesn't open with / typed in a text field, and Escape closes it", async () => {
  plugins = [docker()];
  await boot("/docker");
  const filter = screen.getByRole("textbox", { name: "Filter" });
  filter.focus();

  press("/");
  expect(palette()).toBeNull();

  const trigger = screen.getAllByRole("button", { name: /Search or run/ })[0]!;
  trigger.focus();
  fireEvent.click(trigger);
  await settle();
  expect(palette()).not.toBeNull();
  press("Escape");
  expect(palette()).toBeNull();
  expect(document.activeElement).toBe(trigger);
});

it("leads with the loud Concerns on any Page, each running its main Action through the Plugin", async () => {
  const start = vi.fn();
  const concerns: Concern[] = [
    { id: "n8n", tone: "down", subject: "n8n", title: "n8n is down", cause: "LXC 206 stopped · 6 min ago", actions: [{ label: "Start", run: start }] },
    { id: "nas", tone: "down", subject: "nas", title: "nas is down", quiet: "muted" },
  ];
  plugins = [docker(), [installed("proxmox", "Proxmox"), definePlugin({ useConcerns: () => concerns })]];
  await boot("/docker");

  press("k", { metaKey: true });
  await settle();
  const needs = within(palette()!).getByRole("group", { name: "Needs you" });
  // A quiet one isn't offered.
  expect(within(needs).getAllByRole("option").map(said)).toEqual(["Start · n8n is downLXC 206 stopped"]);
  press("Enter");
  await settle();
  expect(start).toHaveBeenCalledOnce();
});

it("reads the Concerns Needs you lists, running each Plugin's useConcerns once", async () => {
  let mounted = 0;
  function useConcerns(): Concern[] {
    useEffect(() => {
      mounted++;
    }, []);
    return [{ id: "n8n", tone: "down", subject: "n8n", title: "n8n is down" }];
  }
  plugins = [[installed("proxmox", "Proxmox"), definePlugin({ useConcerns })]];
  await boot("/");

  press("/");
  await settle();
  expect(within(screen.getByRole("list", { name: "Needs you" })).getAllByRole("listitem")).toHaveLength(1);
  expect(within(palette()!).getByRole("group", { name: "Needs you" }).textContent).toMatch(/n8n is down/);
  expect(mounted).toBe(1);
});

it("switches the scheme", async () => {
  await boot("/");

  press("/");
  await type("dark");
  press("Enter");
  expect(document.documentElement.classList.contains("dark")).toBe(true);
});

it("leaves out the Commands of a Plugin whose useCommands throws, and offers the others", async () => {
  vi.spyOn(console, "error").mockImplementation(() => {});
  plugins = [
    [
      installed("broken", "Broken"),
      definePlugin({
        useCommands: () => {
          throw new Error("boom");
        },
      }),
    ],
    [installed("apps", "Apps"), definePlugin({ useCommands: () => [{ id: "open:a", title: "Open Alpha", run: () => {} }] })],
  ];
  await boot("/");

  press("/");
  await type("open");
  expect(options()).toEqual(["Open AlphaApps"]);
  vi.restoreAllMocks();
});

it("doesn't open over another dialog", async () => {
  plugins = [docker()];
  await boot("/docker");
  fireEvent.click(screen.getByRole("button", { name: "Open navigation" }));
  await act(async () => {});

  press("k", { ctrlKey: true });
  expect(palette()).toBeNull();
});
