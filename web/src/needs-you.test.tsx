import { act, cleanup, fireEvent, render, screen, within } from "@testing-library/react";
import { useState } from "react";
import { createQueryClient, definePlugin, useConfirm, usePlugin, type Concern, type PluginDefinition, type Status, type Widget } from "@ddash/plugin";
import { settle, tracked } from "@ddash/plugin/testing";
import { Sheet } from "@ddash/ui";
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
  style: "",
});

const widget = (id: string, title: string, status: Status): Widget => ({
  id,
  title,
  sizes: ["S"],
  defaultSize: "S",
  defaultOrder: 0,
  Body: () => <p>{`${title} body`}</p>,
  useStatus: () => status,
});

const down = (id: string, extra: Partial<Concern> = {}): Concern => ({ id, tone: "down", subject: id, title: `${id} is down`, cause: "timeout · 6 min ago", ...extra });

let plugins: [ReturnType<typeof installed>, PluginDefinition][];
let stored: Layout;

beforeEach(() => {
  plugins = [];
  stored = { entries: [], seen: [] };
});

afterEach(cleanup);

const fetchFake = async (input: RequestInfo | URL) => {
  switch (String(input).split("/").pop()) {
    case "ListInstalledPlugins":
      return Response.json({ plugins: plugins.map(([p]) => p) });
    case "GetLayout":
      return Response.json({ layout: stored });
    case "SaveLayout":
    case "GetCoreSettings":
    case "GetOperator":
    case "ListAuditLog":
      return Response.json({});
  }
  return Response.json({ code: "unimplemented" }, { status: 501 });
};

async function boot() {
  const bundles = new Map(plugins.map(([p, b]) => [p.script, b]));
  render(
    <MemoryRouter initialEntries={["/"]}>
      <App client={createQueryClient()} fetch={tracked(vi.fn(fetchFake))} reload={vi.fn()} importBundle={async (url) => ({ default: bundles.get(url) })} />
    </MemoryRouter>,
  );
  await settle();
}

const panel = () => screen.getByRole("region", { name: /^Needs you/ });
const rows = () =>
  within(panel())
    .queryAllByRole("listitem")
    .map((li) => li.getAttribute("aria-label"));
const overall = () => screen.getByRole("img", { name: /^Overall: / }).getAttribute("aria-label");
// The hero: the region named by its sentence.
const sentence = () => screen.getByRole("img", { name: /^Overall: / }).closest("section")!.querySelector("h2")!.textContent;

it("lists every installed Plugin's Concerns in one place, worst first, Widget placed or not", async () => {
  plugins = [
    [installed("network", "Network"), definePlugin({ useConcerns: () => [{ id: "dev", tone: "info", subject: "New Device", title: "New Device on the network" }] })],
    [installed("checks", "Checks"), definePlugin({ widgets: [widget("summary", "Checks", { tone: "down", summary: "1 down" })], useConcerns: () => [down("nas")] })],
    [installed("proxmox", "Proxmox"), definePlugin({ useConcerns: () => [{ id: "disk", tone: "warn", subject: "media", title: "media disk is 92 % full" }] })],
  ];
  // No Widget is placed: reconciling places the new ones, but the Overview lists what isn't on it too.
  stored = { entries: [], seen: ["checks.summary"] };

  await boot();

  expect(rows()).toEqual(["nas is down", "media disk is 92 % full", "New Device on the network"]);
  expect(within(panel()).getByRole("heading").textContent).toBe("Needs you3");
  expect(panel().textContent).toContain("timeout · 6 min ago");
  // The hero names what is down by its subject, once, though the Checks Widget is down too.
  expect(sentence()).toBe("nas is down. Everything else is up.");
  expect(overall()).toBe("Overall: down");
});

it("clamps a long cause to two lines, with the whole of it in its title, and breaks a long token", async () => {
  const image = "registry.example.org/some/very-long-image-name@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef";
  plugins = [[installed("docker", "Docker"), definePlugin({ useConcerns: () => [down("media", { cause: `${image} is not ${image}` })] })]];

  await boot();

  const cause = within(panel()).getByText(/is not/);
  expect(cause.getAttribute("title")).toBe(`${image} is not ${image}`);
  expect(cause.className).toContain("line-clamp-2");
  expect(cause.className).toContain("break-words");
});

it("says nothing needs the Operator once every Plugin reported none", async () => {
  plugins = [[installed("checks", "Checks"), definePlugin({ useConcerns: () => [] })]];

  await boot();

  expect(panel().textContent).toBe("Needs youNothing needs you.");
  expect(rows()).toEqual([]);
});

it("fills the main button only for an Action that fixes the cause", async () => {
  const run = () => {};
  plugins = [
    [
      installed("proxmox", "Proxmox"),
      definePlugin({
        useConcerns: () => [
          down("n8n", { actions: [{ label: "Start", run }] }),
          down("nas", { actions: [{ label: "Mute", secondary: true, run }, { label: "Maintenance", secondary: true, run }] }),
        ],
      }),
    ],
  ];

  await boot();

  const button = (row: string, name: string) => within(within(panel()).getByRole("listitem", { name: row })).getByRole("button", { name });
  expect(button("n8n is down", "Start").className).toContain("bg-primary");
  for (const name of ["Mute", "Maintenance"]) expect(button("nas is down", name).className).not.toContain("bg-primary");
  expect(button("nas is down", "Mute").className).toContain("border-border");
});

it("folds the quiet ones under one line, and they leave the hero's tone alone", async () => {
  plugins = [
    [
      installed("checks", "Checks"),
      definePlugin({
        widgets: [widget("summary", "Checks", { tone: "up", summary: "3 of 3 watched up" })],
        useConcerns: () => [down("backup", { quiet: "muted", href: "/checks/1" }), down("nvr", { quiet: "in Maintenance" }), down("dns", { quiet: "muted" })],
      }),
    ],
  ];

  await boot();

  expect(panel().textContent).toContain("Nothing needs you.");
  const fold = within(panel()).getByText(/^Off on purpose/);
  expect(fold.textContent).toBe("Off on purpose · 2 muted, 1 in Maintenance");
  expect(fold.closest("summary")!.textContent).toContain("— backup, dns, nvr");
  expect(fold.closest("details")!.open).toBe(false);
  // Folded, they are still there, each with its detail.
  expect(within(screen.getByRole("list", { name: "Off on purpose", hidden: true })).getByRole("link", { name: "Details", hidden: true }).getAttribute("href")).toBe("/checks/1");
  expect(overall()).toBe("Overall: up");
  expect(sentence()).toBe("Everything you watch is up.");
});

it("runs an Action's newest run, through the Plugin's own confirm", async () => {
  const restarted = vi.fn();
  function useConcerns(): Concern[] {
    const ask = useConfirm();
    return [
      down("paperless", {
        actions: [{ label: "Restart", run: async () => (await ask({ text: "Restart paperless?", action: "Restart" })) && restarted() }],
      }),
    ];
  }
  plugins = [[installed("docker", "Docker"), definePlugin({ useConcerns })]];

  await boot();
  fireEvent.click(within(panel()).getByRole("button", { name: "Restart" }));
  await settle();
  const dialog = screen.getByRole("alertdialog");
  expect(dialog.textContent).toContain("Restart paperless?");
  fireEvent.click(within(dialog).getByRole("button", { name: "Restart" }));
  await settle();

  expect(restarted).toHaveBeenCalledOnce();
});

it("draws an Action's Sheet in the Plugin's scope, and keeps it open when the Concern goes away", async () => {
  let fix = () => {};
  function useConcerns(): Concern[] {
    const [gone, setGone] = useState(false);
    fix = () => setGone(true);
    return gone ? [] : [down("n8n", { actions: [{ label: "Start", sheet: (close) => <PlanSheet onClose={close} /> }] })];
  }
  function PlanSheet({ onClose }: { onClose: () => void }) {
    return (
      <Sheet title="Start LXC 206" onClose={onClose}>
        <p>{`planned by ${usePlugin().name}`}</p>
      </Sheet>
    );
  }
  plugins = [[installed("proxmox", "Proxmox"), definePlugin({ useConcerns })]];

  await boot();
  fireEvent.click(within(panel()).getByRole("button", { name: "Start" }));
  await settle();

  expect(screen.getByRole("dialog", { name: "Start LXC 206" }).textContent).toContain("planned by proxmox");

  act(() => fix());
  await settle();
  expect(rows()).toEqual([]);
  expect(screen.getByRole("dialog", { name: "Start LXC 206" }).textContent).toContain("planned by proxmox");
});

it("shows the worst five, the rest behind a line that expands them in place and folds them again", async () => {
  function PlanSheet({ onClose }: { onClose: () => void }) {
    return (
      <Sheet title="Start LXC 208" onClose={onClose}>
        <p>{`planned by ${usePlugin().name}`}</p>
      </Sheet>
    );
  }
  const concerns = [1, 2, 3, 4, 5, 6, 7].map((i) => down(`ct${i}`));
  concerns.push(down("ct8", { actions: [{ label: "Start", sheet: (close) => <PlanSheet onClose={close} /> }] }));
  plugins = [
    [installed("checks", "Checks"), definePlugin({ useConcerns: () => [{ id: "vpn", tone: "warn", subject: "vpn", title: "vpn is slow" }, down("backup", { quiet: "muted" })] })],
    [installed("proxmox", "Proxmox"), definePlugin({ useConcerns: () => concerns })],
  ];

  await boot();

  const first = ["ct1 is down", "ct2 is down", "ct3 is down", "ct4 is down", "ct5 is down"];
  const loudRows = () =>
    within(screen.getByRole("list", { name: "Needs you" }))
      .getAllByRole("listitem")
      .map((li) => li.getAttribute("aria-label"));
  expect(loudRows()).toEqual(first);
  // The count is of every loud Concern, folded or not.
  expect(within(panel()).getByRole("heading").textContent).toBe("Needs you9");
  const more = within(panel()).getByRole("button", { name: "and 4 more" });
  expect(more.tagName).toBe("BUTTON");
  expect(more.getAttribute("aria-expanded")).toBe("false");
  expect(more.getAttribute("aria-controls")).toBe(screen.getByRole("list", { name: "Needs you" }).id);
  // The quiet ones keep their own folded line.
  expect(within(panel()).getByText(/^Off on purpose/).textContent).toBe("Off on purpose · 1 muted");

  fireEvent.click(more);
  expect(loudRows()).toEqual([...first, "ct6 is down", "ct7 is down", "ct8 is down", "vpn is slow"]);
  expect(more.getAttribute("aria-expanded")).toBe("true");
  expect(more.textContent).toBe("Show fewer");

  // A row past the cap acts once shown, and its Sheet outlives folding it again.
  fireEvent.click(within(within(panel()).getByRole("listitem", { name: "ct8 is down" })).getByRole("button", { name: "Start" }));
  await settle();
  expect(screen.getByRole("dialog", { name: "Start LXC 208" }).textContent).toContain("planned by proxmox");

  fireEvent.click(within(panel()).getByRole("button", { name: "Show fewer" }));
  expect(loudRows()).toEqual(first);
  expect(within(panel()).getByRole("button", { name: "and 4 more" }).getAttribute("aria-expanded")).toBe("false");
  expect(screen.getByRole("dialog", { name: "Start LXC 208" })).toBeTruthy();
});

it("says so when a Plugin's useConcerns throws, and lists the others", async () => {
  plugins = [
    [
      installed("broken", "Broken"),
      definePlugin({
        useConcerns: () => {
          throw new Error("no data");
        },
      }),
    ],
    [installed("checks", "Checks"), definePlugin({ useConcerns: () => [down("nas")] })],
  ];
  vi.spyOn(console, "error").mockImplementation(() => {});

  await boot();

  expect(rows()).toEqual(["nas is down", "Broken can't say what needs you"]);
  expect(panel().textContent).toContain("no data");
});
