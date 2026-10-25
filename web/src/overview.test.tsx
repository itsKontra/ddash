import { act, cleanup, fireEvent, render, screen, within } from "@testing-library/react";
import { createQueryClient, definePlugin, type PluginDefinition, type Status, type Widget, type WidgetSize } from "@ddash/plugin";
import { MemoryRouter } from "react-router";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { App } from "./app";
import type { Layout } from "./layout";

// An installed Plugin as ListInstalledPlugins returns it.
const installed = (name: string, title: string) => ({
  name,
  title,
  description: "",
  icon: "puzzle",
  author: "itsKontra",
  requires: [`${name}-api`],
  script: `/plugins/${name}/v1.2.3/index.js`,
  style: `/plugins/${name}/v1.2.3/index.css`,
});

// A Widget whose body names it and whose status is fixed; its first size
// is its default.
const widget = (id: string, title: string, status: Status | undefined, ...sizes: WidgetSize[]): Widget => ({
  id,
  title,
  sizes,
  defaultSize: sizes[0],
  Body: ({ size }) => <p>{`${title} body at ${size}`}</p>,
  useStatus: () => status,
});

const up = (summary: string): Status => ({ tone: "up", summary });

type Bundle = PluginDefinition | Error;

// The fake core: its installed Plugins and stored layout, and the layouts
// saved, oldest first.
let plugins: [ReturnType<typeof installed>, Bundle][];
let stored: Layout;
let saves: Layout[];
// Answers a call before the fake core does, e.g. to fail it.
let intercept: ((method: string) => Response | Promise<Response> | undefined) | undefined;

beforeEach(() => {
  plugins = [];
  stored = { entries: [], seen: [] };
  saves = [];
  intercept = undefined;
});

afterEach(() => {
  cleanup();
  vi.useRealTimers();
});

const fetchFake = async (input: RequestInfo | URL, init?: RequestInit) => {
  const method = String(input).split("/").pop()!;
  const intercepted = intercept?.(method);
  if (intercepted) return intercepted;
  switch (method) {
    case "ListInstalledPlugins":
      return Response.json({ plugins: plugins.map(([p]) => p) });
    case "GetLayout":
      return Response.json({ layout: stored });
    case "SaveLayout": {
      const body = JSON.parse(new TextDecoder().decode(init?.body as Uint8Array));
      stored = { entries: body.layout.entries ?? [], seen: body.layout.seen ?? [] };
      saves.push(stored);
      return Response.json({});
    }
    case "GetCoreSettings":
    case "GetOperator":
      return Response.json({});
  }
  return Response.json({ code: "unimplemented", message: method }, { status: 501 });
};

async function boot(path = "/", reload = vi.fn()) {
  const bundles = new Map(plugins.map(([p, b]) => [p.script, b]));
  render(
    <MemoryRouter initialEntries={[path]}>
      <App
        client={createQueryClient()}
        fetch={vi.fn(fetchFake)}
        reload={reload}
        importBundle={async (url) => {
          const b = bundles.get(url);
          if (b instanceof Error) throw b;
          return { default: b };
        }}
      />
    </MemoryRouter>,
  );
  await settle();
}

const settle = () => act(() => new Promise((r) => setTimeout(r, 20)));

const cards = () => within(screen.getByRole("list", { name: "Widgets" })).queryAllByRole("listitem");
const card = (title: string) => within(screen.getByRole("list", { name: "Widgets" })).getByRole("listitem", { name: title });
// The HeroCard: the region named by its headline, under the "Status" kicker.
const hero = () => screen.getByText("Status").closest("section")!;
const headline = () => within(hero()).getByRole("heading", { level: 2 }).textContent;
const overall = () => within(hero()).getByRole("img", { name: /^Overall: / }).getAttribute("aria-label");
const problems = () =>
  within(hero())
    .queryAllByRole("listitem")
    .map((li) => li.textContent);

const docker = () =>
  definePlugin({
    nav: { order: 50 },
    pages: [{ index: true, Component: () => <p>docker page</p> }],
    widgets: [widget("containers", "Containers", up("12 of 12 running"), "S", "M")],
  });

it("appends an installed Plugin's Widgets at their default size, and saves the layout", async () => {
  plugins = [
    [installed("docker", "Docker"), docker()],
    [installed("network", "Network"), definePlugin({ widgets: [widget("throughput", "Throughput", up("1 Gbit/s"), "M"), widget("vpn", "VPN", up("on"), "S")] })],
  ];

  await boot();

  expect(cards().map((c) => c.getAttribute("aria-label"))).toEqual(["Containers", "Throughput", "VPN"]);
  expect(card("Containers").textContent).toContain("Containers body at S");
  expect(saves).toEqual([
    {
      entries: [
        { widget: "docker.containers", size: "S" },
        { widget: "network.throughput", size: "M" },
        { widget: "network.vpn", size: "S" },
      ],
      seen: ["docker.containers", "network.throughput", "network.vpn"],
    },
  ]);
});

it("draws each Widget in the host's frame: its title links to the Plugin's Page, next to its status", async () => {
  plugins = [[installed("docker", "Docker"), docker()]];
  stored = { entries: [{ widget: "docker.containers", size: "M" }], seen: ["docker.containers"] };

  await boot();

  const c = card("Containers");
  expect(within(c).getByRole("link", { name: "Containers" }).getAttribute("href")).toBe("/docker");
  expect(within(c).getByRole("img", { name: "up: 12 of 12 running" })).toBeTruthy();
  expect(c.textContent).toContain("Containers body at M");
  expect(saves).toEqual([]);
});

it("forgets an uninstalled Plugin's Widgets, and keeps a removed Widget removed", async () => {
  plugins = [[installed("docker", "Docker"), docker()]];
  stored = {
    entries: [{ widget: "checks.summary", size: "S" }],
    seen: ["checks.summary", "docker.containers"],
  };

  await boot();

  expect(cards()).toEqual([]);
  expect(saves).toEqual([{ entries: [], seen: ["docker.containers"] }]);
});

it("reconciles on every page, so an uninstall from the Catalogue forgets the Plugin's Widgets at once", async () => {
  stored = { entries: [{ widget: "docker.containers", size: "S" }], seen: ["docker.containers"] };

  await boot("/settings");

  expect(saves).toEqual([{ entries: [], seen: [] }]);
});

it("the hero lists the placed Widgets that aren't up, ignoring one that hasn't reported", async () => {
  plugins = [
    [
      installed("checks", "Checks"),
      definePlugin({
        widgets: [
          widget("summary", "Checks", { tone: "down", summary: "paperless down 14 min" }, "S"),
          widget("uptime", "Uptime", undefined, "M"),
        ],
      }),
    ],
    [installed("docker", "Docker"), docker()],
    [installed("network", "Network"), definePlugin({ widgets: [widget("vpn", "VPN", { tone: "warn", summary: "on the fallback" }, "S")] })],
  ];

  await boot();

  expect(problems()).toEqual(["Checkspaperless down 14 min", "VPNon the fallback"]);
  expect(within(hero()).getAllByRole("img", { name: /^(down|warn)$/ }).map((d) => d.getAttribute("aria-label"))).toEqual(["down", "warn"]);
  expect(overall()).toBe("Overall: down");
  // The headline counts the worst tone, as its tile does: the warn one isn't down.
  expect(headline()).toBe("1 Widget down");
  expect(screen.getByRole("region", { name: "1 Widget down" })).toBe(hero());
});

it("the hero's headline counts only the Widgets needing attention, as their tile does, when none is down", async () => {
  plugins = [
    [
      installed("lab", "Lab"),
      definePlugin({
        widgets: [
          widget("a", "Alpha", { tone: "warn", summary: "flapping" }, "S"),
          widget("b", "Bravo", { tone: "maint", summary: "patching" }, "S"),
          widget("c", "Charlie", { tone: "warn", summary: "slow" }, "S"),
        ],
      }),
    ],
  ];

  await boot();

  expect(headline()).toBe("2 Widgets need attention");
  expect(screen.getByRole("group", { name: "Needs attention" }).textContent).toBe("Needs attention2");
});

it("the hero's headline counts the Widgets in Maintenance when nothing worse is wrong", async () => {
  plugins = [[installed("lab", "Lab"), definePlugin({ widgets: [widget("b", "Bravo", { tone: "maint", summary: "patching" }, "S")] })]];

  await boot();

  expect(headline()).toBe("1 Widget in Maintenance");
});

it("the hero says everything is up when every reporting Widget is", async () => {
  plugins = [[installed("docker", "Docker"), docker()]];

  await boot();

  expect(problems()).toEqual([]);
  expect(overall()).toBe("Overall: up");
  expect(headline()).toBe("Everything is up");
});

const mixed = () =>
  definePlugin({
    widgets: [
      widget("a", "Alpha", up("a"), "S"),
      widget("b", "Bravo", up("b"), "S"),
      widget("c", "Charlie", { tone: "down", summary: "gone" }, "S"),
      widget("d", "Delta", { tone: "warn", summary: "flapping" }, "S"),
      widget("e", "Echo", { tone: "maint", summary: "patching" }, "S"),
      widget("f", "Foxtrot", undefined, "S"),
    ],
  });

it("the hero's foot counts the placed, reporting and up Widgets, and its ring shows the share up", async () => {
  plugins = [[installed("lab", "Lab"), mixed()]];

  await boot();

  const foot = within(hero()).getByRole("img", { name: /^Overall: / }).parentElement!;
  expect([...foot.children].slice(1).map((s) => s.textContent)).toEqual(["6 Widgets on the Overview", "5 reporting", "2 up"]);
  const ring = within(hero()).getByRole("meter", { name: "Widgets up" });
  expect(ring.getAttribute("aria-valuenow")).toBe("33");
  expect(ring.getAttribute("aria-valuetext")).toBe("33%");
});

it("the hero clamps its problems to the first three and says how many more there are", async () => {
  plugins = [
    [
      installed("lab", "Lab"),
      definePlugin({ widgets: ["One", "Two", "Three", "Four", "Five"].map((t) => widget(t.toLowerCase(), t, { tone: "warn", summary: "slow" }, "S")) }),
    ],
  ];

  await boot();

  expect(problems()).toEqual(["Oneslow", "Twoslow", "Threeslow"]);
  expect(within(hero()).getByText("and 2 more")).toBeTruthy();
});

it("the hero has no ring while nothing is on the Overview", async () => {
  await boot();

  expect(headline()).toBe("No Widgets on the Overview");
  expect(within(hero()).queryByRole("meter")).toBeNull();
});

it("four metric tiles count the reporting Widgets that are up, need attention, are down and are in Maintenance", async () => {
  plugins = [[installed("lab", "Lab"), mixed()]];

  await boot();

  const tiles = ["Up", "Needs attention", "Down", "Maintenance"].map((label) => screen.getByRole("group", { name: label }));
  expect(tiles.map((t) => t.textContent)).toEqual(["Up2", "Needs attention1", "Down1", "Maintenance1"]);
  // Each tile's icon chip carries its tone's tint.
  const chipTone = (tile: HTMLElement) => tile.querySelector("[class*='-tint']")?.className.match(/bg-(\w+)-tint/)?.[1];
  expect(tiles.map(chipTone)).toEqual(["up", "warn", "down", "maint"]);
});

it("the metric tiles load until the layout has come", async () => {
  intercept = (method) => (method === "GetLayout" ? new Promise<Response>(() => {}) : undefined);

  await boot();

  expect(within(screen.getByRole("group", { name: "Up" })).getByRole("status", { name: "Loading" })).toBeTruthy();
});

it("lays the Widget grid out in one column on phones, two from small screens and four from wide ones", async () => {
  plugins = [[installed("lab", "Lab"), three()]];
  stored = {
    entries: [
      { widget: "lab.a", size: "L" },
      { widget: "lab.b", size: "S" },
      { widget: "lab.c", size: "M" },
    ],
    seen: ["lab.a", "lab.b", "lab.c"],
  };

  await boot();

  const cols = (el: Element, prefix: string) => el.className.split(" ").filter((c) => c.startsWith(prefix));
  const grid = screen.getByRole("list", { name: "Widgets" });
  expect(cols(grid, "grid-cols-").concat(cols(grid, "sm:grid-cols-"), cols(grid, "xl:grid-cols-"))).toEqual([
    "grid-cols-1",
    "sm:grid-cols-2",
    "xl:grid-cols-4",
  ]);
  // A wide Widget spans both columns from small screens on, never more than the phone's one.
  expect(cols(card("Alpha"), "col-span-").concat(cols(card("Alpha"), "sm:col-span-"))).toEqual(["col-span-1", "sm:col-span-2"]);
  expect(cols(card("Charlie"), "sm:col-span-")).toEqual(["sm:col-span-2"]);
  expect(cols(card("Bravo"), "sm:col-span-")).toEqual([]);
});

it("shows a Widget whose body or status crashes as an error frame, reported to the hero at warn", async () => {
  const quiet = vi.spyOn(console, "error").mockImplementation(() => {});
  const crashingBody: Widget = {
    ...widget("guests", "Guests", up("4 running"), "S"),
    Body: () => {
      throw new Error("body boom");
    },
  };
  const crashingStatus: Widget = {
    ...widget("nginx", "nginx", undefined, "S"),
    useStatus: () => {
      throw new Error("status boom");
    },
  };
  plugins = [
    [installed("proxmox", "Proxmox"), definePlugin({ widgets: [crashingBody] })],
    [installed("reverse-proxy", "Reverse proxy"), definePlugin({ widgets: [crashingStatus] })],
  ];

  await boot();

  expect(within(card("Guests")).getByRole("alert").textContent).toMatch(/Guests failed to render.*body boom/);
  expect(within(card("nginx")).getByRole("alert").textContent).toMatch(/nginx failed to render.*status boom/);
  expect(problems()).toEqual(["GuestsGuests failed to render", "nginxnginx failed to render"]);
  expect(overall()).toBe("Overall: warn");
  quiet.mockRestore();
});

it("shows a placed Widget of a Plugin whose bundle failed as an error frame", async () => {
  plugins = [[installed("docker", "Docker"), new Error("Failed to fetch dynamically imported module")]];
  stored = { entries: [{ widget: "docker.containers", size: "M" }], seen: ["docker.containers"] };

  await boot();

  expect(within(cards()[0]).getByRole("alert").textContent).toMatch(/Docker failed to load/);
  expect(problems()).toEqual(["DockerDocker failed to load"]);
  expect(saves).toEqual([]);
});

const edit = () => fireEvent.click(screen.getByRole("button", { name: "Edit" }));
const done = async () => {
  fireEvent.click(screen.getByRole("button", { name: "Done" }));
  await settle();
};

const three = () =>
  definePlugin({
    widgets: [widget("a", "Alpha", up("a"), "S", "M", "L"), widget("b", "Bravo", up("b"), "S"), widget("c", "Charlie", up("c"), "M", "L")],
  });

const editBar = () => screen.getByRole("toolbar", { name: "Edit the Overview" });

it("Edit swaps the head's actions for the edit bar, whose line says whether anything changed", async () => {
  plugins = [[installed("lab", "Lab"), three()]];
  stored = { entries: [{ widget: "lab.a", size: "S" }], seen: ["lab.a", "lab.b", "lab.c"] };
  await boot();
  expect(screen.queryByRole("toolbar")).toBeNull();

  edit();
  expect(screen.queryByRole("button", { name: "Edit" })).toBeNull();
  expect(within(editBar()).getByText("Editing the Overview")).toBeTruthy();
  expect(within(editBar()).getByText(/No changes yet/)).toBeTruthy();
  expect(within(editBar()).getAllByRole("button").map((b) => b.textContent?.trim())).toEqual(["Add Widget", "Cancel", "Done"]);

  fireEvent.click(within(card("Alpha")).getByRole("radio", { name: "M" }));
  expect(within(editBar()).getByText(/Unsaved changes/)).toBeTruthy();
});

it("on phones the edit bar wraps: its title on one line, its buttons on a full-width line below", async () => {
  plugins = [[installed("lab", "Lab"), three()]];
  await boot();

  edit();
  const bar = editBar();
  expect(bar.className.split(" ")).toContain("flex-wrap");
  const buttons = within(bar).getByRole("button", { name: "Cancel" }).parentElement!;
  expect(buttons.className.split(" ")).toEqual(expect.arrayContaining(["w-full", "sm:w-auto"]));
});

it("Cancel drops the draft and leaves Edit mode without saving", async () => {
  plugins = [[installed("lab", "Lab"), three()]];
  stored = {
    entries: [
      { widget: "lab.a", size: "S" },
      { widget: "lab.b", size: "S" },
    ],
    seen: ["lab.a", "lab.b", "lab.c"],
  };
  await boot();

  edit();
  fireEvent.click(within(card("Alpha")).getByRole("radio", { name: "L" }));
  fireEvent.click(within(card("Bravo")).getByRole("button", { name: "Remove Bravo" }));
  fireEvent.click(within(editBar()).getByRole("button", { name: "Cancel" }));
  await settle();

  expect(saves).toEqual([]);
  expect(screen.queryByRole("toolbar")).toBeNull();
  expect(cards().map((c) => c.getAttribute("aria-label"))).toEqual(["Alpha", "Bravo"]);
  expect(card("Alpha").textContent).toContain("Alpha body at S");
  expect(screen.getByRole("button", { name: "Edit" })).toBeTruthy();
});

it("in Edit mode a card keeps its live body but makes it inert, under a size tab", async () => {
  plugins = [[installed("lab", "Lab"), three()]];
  stored = { entries: [{ widget: "lab.a", size: "M" }], seen: ["lab.a", "lab.b", "lab.c"] };
  await boot();
  expect(card("Alpha").querySelector("[inert]")).toBeNull();

  edit();
  const body = within(card("Alpha")).getByText("Alpha body at M");
  expect(body.closest("[inert]")).toBeTruthy();
  expect(within(card("Alpha")).getByText("M", { selector: "[data-size-tab]" })).toBeTruthy();
  // The title isn't a link while editing, so a click can't leave the draft.
  expect(within(card("Alpha")).queryByRole("link")).toBeNull();
});

it("in Edit mode the Operator resizes a Widget among its sizes, and Done saves it", async () => {
  plugins = [[installed("lab", "Lab"), three()]];
  stored = {
    entries: [
      { widget: "lab.a", size: "S" },
      { widget: "lab.b", size: "S" },
    ],
    seen: ["lab.a", "lab.b", "lab.c"],
  };
  await boot();
  expect(screen.queryByRole("radiogroup")).toBeNull();

  edit();
  const sizes = within(card("Alpha")).getByRole("radiogroup", { name: "Size" });
  expect(within(sizes).getAllByRole("radio").map((r) => r.textContent)).toEqual(["S", "M", "L"]);
  expect(within(card("Bravo")).queryByRole("radiogroup")).toBeNull(); // one size: nothing to pick
  fireEvent.click(within(sizes).getByRole("radio", { name: "L" }));
  expect(card("Alpha").textContent).toContain("Alpha body at L");
  expect(saves).toEqual([]);
  await done();

  expect(saves).toEqual([
    {
      entries: [
        { widget: "lab.a", size: "L" },
        { widget: "lab.b", size: "S" },
      ],
      seen: ["lab.a", "lab.b", "lab.c"],
    },
  ]);
  expect(screen.queryByRole("radiogroup")).toBeNull();
});

it("in Edit mode the Operator removes a Widget, and it stays seen", async () => {
  plugins = [[installed("lab", "Lab"), three()]];
  stored = {
    entries: [
      { widget: "lab.a", size: "S" },
      { widget: "lab.b", size: "S" },
    ],
    seen: ["lab.a", "lab.b", "lab.c"],
  };
  await boot();

  edit();
  fireEvent.click(within(card("Alpha")).getByRole("button", { name: "Remove Alpha" }));
  await done();

  expect(cards().map((c) => c.getAttribute("aria-label"))).toEqual(["Bravo"]);
  expect(saves).toEqual([{ entries: [{ widget: "lab.b", size: "S" }], seen: ["lab.a", "lab.b", "lab.c"] }]);
});

const addWidget = () => {
  fireEvent.click(within(editBar()).getByRole("button", { name: "Add Widget" }));
  return screen.getByRole("dialog", { name: "Add Widget" });
};
const groups = (sheet: HTMLElement) =>
  within(sheet)
    .queryAllByRole("group")
    .map((g) => g.getAttribute("aria-label"));
// A Plugin's Widget rows in the Sheet, each as "<title> <sizes> <Add or On the Overview>".
const offered = (sheet: HTMLElement, plugin: string) =>
  within(within(sheet).getByRole("group", { name: plugin }))
    .getAllByRole("listitem")
    .map((li) => [...li.querySelectorAll("[data-row-part]")].map((el) => el.textContent?.trim()).join(" "));

it("Add Widget lists every offered Widget by Plugin, with its sizes and Add, or On the Overview once placed", async () => {
  plugins = [
    [installed("lab", "Lab"), three()],
    [installed("docker", "Docker"), docker()],
  ];
  stored = { entries: [{ widget: "lab.b", size: "S" }], seen: ["lab.a", "lab.b", "lab.c", "docker.containers"] };
  await boot();

  edit();
  const sheet = addWidget();
  expect(groups(sheet)).toEqual(["Lab", "Docker"]);
  expect(offered(sheet, "Lab")).toEqual(["Alpha SML Add", "Bravo S On the Overview", "Charlie ML Add"]);
  expect(offered(sheet, "Docker")).toEqual(["Containers SM Add"]);
  expect(within(sheet).getByText("1 Widget on the Overview")).toBeTruthy();

  fireEvent.click(within(sheet).getByRole("button", { name: "Add Charlie" }));
  // The Sheet stays open for the next pick, and the row now says it's placed.
  expect(offered(sheet, "Lab")).toEqual(["Alpha SML Add", "Bravo S On the Overview", "Charlie ML On the Overview"]);
  expect(within(sheet).getByText("2 Widgets on the Overview")).toBeTruthy();
  expect(cards().map((c) => c.getAttribute("aria-label"))).toEqual(["Bravo", "Charlie"]);

  // The footer's Close, not the header's icon button.
  fireEvent.click(within(sheet).getAllByRole("button", { name: "Close" }).at(-1)!);
  expect(screen.queryByRole("dialog")).toBeNull();
  await done();

  expect(saves).toEqual([
    {
      entries: [
        { widget: "lab.b", size: "S" },
        { widget: "lab.c", size: "M" },
      ],
      seen: ["lab.a", "lab.b", "lab.c", "docker.containers"],
    },
  ]);
});

it("Add Widget's search narrows the list by Widget or Plugin, and says when nothing matches", async () => {
  plugins = [
    [installed("lab", "Lab"), three()],
    [installed("docker", "Docker"), docker()],
  ];
  await boot();

  edit();
  const sheet = addWidget();
  const search = within(sheet).getByRole("searchbox", { name: "Search Widgets" });
  fireEvent.change(search, { target: { value: "char" } });
  expect(groups(sheet)).toEqual(["Lab"]);
  expect(offered(sheet, "Lab")).toEqual(["Charlie ML On the Overview"]);

  fireEvent.change(search, { target: { value: "docker" } });
  expect(groups(sheet)).toEqual(["Docker"]);

  fireEvent.change(search, { target: { value: "zzz" } });
  expect(groups(sheet)).toEqual([]);
  expect(within(sheet).getByText("No Widget matches “zzz”")).toBeTruthy();
});

it("Add Widget says so when no installed Plugin offers a Widget", async () => {
  plugins = [[installed("plain", "Plain"), definePlugin({ pages: [{ index: true, Component: () => <p>plain</p> }] })]];
  await boot();

  edit();
  const sheet = addWidget();
  expect(within(sheet).queryByRole("searchbox")).toBeNull();
  expect(within(sheet).getByText("No Plugin offers a Widget")).toBeTruthy();
  expect(within(sheet).getByText("0 Widgets on the Overview")).toBeTruthy();
});

it("in Edit mode the Operator drags a Widget to another's place", async () => {
  plugins = [[installed("lab", "Lab"), three()]];
  stored = {
    entries: [
      { widget: "lab.a", size: "S" },
      { widget: "lab.b", size: "S" },
      { widget: "lab.c", size: "M" },
    ],
    seen: ["lab.a", "lab.b", "lab.c"],
  };
  await boot();
  expect(card("Alpha").getAttribute("draggable")).not.toBe("true");

  edit();
  fireEvent.dragStart(card("Charlie"));
  fireEvent.dragOver(card("Alpha"));
  fireEvent.drop(card("Alpha"));
  fireEvent.dragEnd(card("Charlie"));
  await done();

  expect(cards().map((c) => c.getAttribute("aria-label"))).toEqual(["Charlie", "Alpha", "Bravo"]);
  expect(saves.at(-1)?.entries.map((e) => e.widget)).toEqual(["lab.c", "lab.a", "lab.b"]);
});

const badGateway = () => new Response("<html>Bad Gateway</html>", { status: 502, headers: { "Content-Type": "text/html" } });

it("shows the unreachable banner from a 5xx until the next success, with the hero at warn meanwhile", async () => {
  vi.useFakeTimers({ toFake: ["Date"] });
  vi.setSystemTime(new Date(2026, 9, 20, 8, 5));
  plugins = [[installed("docker", "Docker"), docker()]];
  let failSaves = 1;
  intercept = (method) => {
    if (method !== "SaveLayout" || failSaves === 0) return undefined;
    failSaves--;
    vi.setSystemTime(new Date(2026, 9, 20, 8, 9));
    return badGateway();
  };

  await boot(); // appending Containers saves, and that save fails

  expect(screen.getByRole("status").textContent).toBe("ddash unreachable, showing data from 08:05");
  expect(overall()).toBe("Overall: warn");

  edit();
  fireEvent.click(within(card("Containers")).getByRole("radio", { name: "M" }));
  await done();

  expect(screen.queryByRole("status")).toBeNull();
  expect(overall()).toBe("Overall: up");
});

it("shows the unreachable banner when the network fails", async () => {
  plugins = [[installed("docker", "Docker"), docker()]];
  intercept = (method) => (method === "SaveLayout" ? Promise.reject(new TypeError("Failed to fetch")) : undefined);

  await boot();

  expect(screen.getByRole("status").textContent).toMatch(/^ddash unreachable, showing data from \d\d:\d\d$/);
});

it("counts a 5xx from the core, even a Connect error, as ddash failing", async () => {
  plugins = [[installed("docker", "Docker"), docker()]];
  intercept = (method) => (method === "SaveLayout" ? Response.json({ code: "unavailable", message: "audit log unavailable" }, { status: 503 }) : undefined);

  await boot();

  expect(screen.getByRole("status").textContent).toMatch(/^ddash unreachable/);
});

it("reloads the page on a 401, so the Operator signs in again", async () => {
  const reload = vi.fn();
  intercept = (method) => (method === "GetLayout" ? Response.json({ code: "unauthenticated", message: "sign in" }, { status: 401 }) : undefined);

  await boot("/", reload);

  expect(reload).toHaveBeenCalledOnce();
});
