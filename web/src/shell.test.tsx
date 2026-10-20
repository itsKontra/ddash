import { act, cleanup, render, screen, within } from "@testing-library/react";
import { createQueryClient, definePlugin, usePlugin, usePluginLink, type PluginDefinition } from "@ddash/plugin";
import { MemoryRouter, useParams } from "react-router";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { App } from "./app";

// An installed Plugin as ListInstalledPlugins returns it.
const installed = (name: string, title: string, icon = "puzzle") => ({
  name,
  title,
  description: "",
  icon,
  author: "itsKontra",
  requires: [`${name}-api`],
  script: `/plugins/${name}/v1.2.3/index.js`,
  style: `/plugins/${name}/v1.2.3/index.css`,
});

type Bundle = PluginDefinition | Error | Promise<PluginDefinition>;

let coreCalls: string[];
let importBundle: ReturnType<typeof vi.fn<(url: string) => Promise<{ default: PluginDefinition }>>>;

beforeEach(() => {
  coreCalls = [];
});

afterEach(() => {
  cleanup();
  document.head.querySelectorAll("link[rel=stylesheet]").forEach((l) => l.remove());
});

// boot runs the shell at path with plugins installed, each bundle's script
// URL importing as the given Bundle.
async function boot(path: string, plugins: [ReturnType<typeof installed>, Bundle][]) {
  const fetch = vi.fn(async (input: RequestInfo | URL) => {
    coreCalls.push(String(input));
    return Response.json({ plugins: plugins.map(([p]) => p) });
  });
  const bundles = new Map(plugins.map(([p, b]) => [p.script, b]));
  importBundle = vi.fn(async (url: string) => {
    const b = await bundles.get(url);
    if (b instanceof Error) throw b;
    return { default: b! };
  });
  render(
    <MemoryRouter initialEntries={[path]}>
      <App client={createQueryClient()} fetch={fetch} importBundle={importBundle} />
    </MemoryRouter>,
  );
  await settle();
}

const settle = () => act(() => new Promise((r) => setTimeout(r, 20)));

const navLinks = () =>
  within(screen.getByRole("navigation", { name: "Main" }))
    .getAllByRole("link")
    .map((l) => l.textContent);

const page = (text: string) => () => <p>{text}</p>;

it("lists Overview first, then Nav Entries by order and title, then Plugins and Settings", async () => {
  await boot("/", [
    [installed("docker", "Docker"), definePlugin({ nav: { order: 50 }, pages: [{ index: true, Component: page("d") }] })],
    [installed("checks", "Checks"), definePlugin({ nav: { order: 20 }, pages: [{ index: true, Component: page("c") }] })],
    [installed("bravo", "Bravo"), definePlugin({ nav: { order: 50 }, pages: [{ index: true, Component: page("b") }] })],
    [installed("apps", "Apps"), definePlugin({ nav: { order: 10, label: "Applications" }, pages: [{ index: true, Component: page("a") }] })],
    [installed("quiet", "Quiet"), definePlugin({ widgets: [] })],
  ]);

  expect(navLinks()).toEqual(["Overview", "Applications", "Checks", "Bravo", "Docker", "Plugins", "Settings"]);
  expect(screen.getByRole("link", { name: "Docker" }).getAttribute("href")).toBe("/docker");
  // The shell reads the installed Plugins, the Overview layout and the core settings (the Theme), once each.
  expect(coreCalls.toSorted()).toEqual([
    "/api/core/ddash.core.CoreService/GetCoreSettings",
    "/api/core/ddash.core.CoreService/GetLayout",
    "/api/core/ddash.core.CoreService/ListInstalledPlugins",
  ]);
});

it("imports every bundle in parallel at boot, each with its stylesheet", async () => {
  const never = new Promise<PluginDefinition>(() => {});
  await boot("/", [
    [installed("docker", "Docker"), never],
    [installed("checks", "Checks"), never],
  ]);

  expect(importBundle.mock.calls.map(([url]) => url).sort()).toEqual([
    "/plugins/checks/v1.2.3/index.js",
    "/plugins/docker/v1.2.3/index.js",
  ]);
  const styles = [...document.head.querySelectorAll<HTMLLinkElement>("link[rel=stylesheet]")].map((l) => l.getAttribute("href"));
  expect(styles.sort()).toEqual(["/plugins/checks/v1.2.3/index.css", "/plugins/docker/v1.2.3/index.css"]);
});

it("routes a Plugin's Pages under /<plugin-name>/, inside its own scope", async () => {
  function Host() {
    return <p>{`${usePlugin().name} host ${useParams().host}`}</p>;
  }
  const docker = definePlugin({
    nav: { order: 50 },
    pages: [
      { index: true, Component: page("all hosts") },
      { path: "hosts/:host", Component: Host },
    ],
  });

  await boot("/docker/hosts/media", [[installed("docker", "Docker"), docker]]);

  expect(screen.getByText("docker host media")).toBeTruthy();
});

it("lets a Plugin link to another installed Plugin's Page, and not to one that isn't installed", async () => {
  function Links() {
    return (
      <>
        <p>{`docker: ${usePluginLink("docker", "hosts/media") ?? "none"}`}</p>
        <p>{`proxmox: ${usePluginLink("proxmox") ?? "none"}`}</p>
      </>
    );
  }
  await boot("/apps", [
    [installed("apps", "Apps"), definePlugin({ nav: { order: 10 }, pages: [{ index: true, Component: Links }] })],
    [installed("docker", "Docker"), definePlugin({ nav: { order: 50 }, pages: [{ index: true, Component: page("d") }] })],
  ]);

  expect(screen.getByText("docker: /docker/hosts/media")).toBeTruthy();
  expect(screen.getByText("proxmox: none")).toBeTruthy();
});

it("shows a bundle that fails to load as a broken Nav Entry, and its Page says so", async () => {
  await boot("/docker", [
    [installed("docker", "Docker"), new Error("Failed to fetch dynamically imported module")],
    [installed("checks", "Checks"), definePlugin({ nav: { order: 20 }, pages: [{ index: true, Component: page("c") }] })],
  ]);

  const broken = screen.getByRole("link", { name: /Docker/ });
  expect(within(broken).getByRole("img", { name: "failed to load" })).toBeTruthy();
  expect(screen.getByRole("alert").textContent).toMatch(/Docker failed to load.*Failed to fetch dynamically imported module/);
  expect(navLinks()).toContain("Checks");
});

it("keeps the shell up when a Page crashes, showing an error frame in its place", async () => {
  const quiet = vi.spyOn(console, "error").mockImplementation(() => {});
  function Crash(): never {
    throw new Error("boom");
  }
  await boot("/docker", [[installed("docker", "Docker"), definePlugin({ nav: { order: 50 }, pages: [{ index: true, Component: Crash }] })]]);

  expect(screen.getByRole("alert").textContent).toMatch(/Docker crashed.*boom/);
  expect(navLinks()).toContain("Docker");
  quiet.mockRestore();
});

it("shows a Nav Entry's badge, and drops a badge that crashes without breaking the sidebar", async () => {
  const quiet = vi.spyOn(console, "error").mockImplementation(() => {});
  await boot("/", [
    [
      installed("checks", "Checks"),
      definePlugin({ nav: { order: 20, useBadge: () => ({ count: 3, tone: "down" }) }, pages: [{ index: true, Component: page("c") }] }),
    ],
    [
      installed("network", "Network"),
      definePlugin({
        nav: {
          order: 30,
          useBadge: () => {
            throw new Error("badge boom");
          },
        },
        pages: [{ index: true, Component: page("n") }],
      }),
    ],
  ]);

  expect(navLinks()).toEqual(["Overview", "Checks3", "Network", "Plugins", "Settings"]);
  quiet.mockRestore();
});

it("shows a badge's text in place of a count", async () => {
  await boot("/", [
    [
      installed("reverse-proxy", "Reverse proxy"),
      definePlugin({ nav: { order: 60, useBadge: () => ({ text: "!", tone: "down" }) }, pages: [{ index: true, Component: page("r") }] }),
    ],
  ]);

  expect(navLinks()).toEqual(["Overview", "Reverse proxy!", "Plugins", "Settings"]);
});

it("answers a path no installed Plugin owns with not found", async () => {
  await boot("/nope/x", [[installed("docker", "Docker"), definePlugin({ nav: { order: 50 } })]]);

  expect(screen.getByRole("heading", { name: "Not found" })).toBeTruthy();
});
