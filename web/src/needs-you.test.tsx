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
const overall = () => within(screen.getByText("Status").closest("section")!).getByRole("img", { name: /^Overall: / }).getAttribute("aria-label");

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
});

it("says nothing needs the Operator once every Plugin reported none", async () => {
  plugins = [[installed("checks", "Checks"), definePlugin({ useConcerns: () => [] })]];

  await boot();

  expect(panel().textContent).toContain("Nothing needs you.");
  expect(rows()).toEqual([]);
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
