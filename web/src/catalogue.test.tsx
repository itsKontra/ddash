import { act, cleanup, fireEvent, render, screen, within } from "@testing-library/react";
import { createQueryClient } from "@ddash/plugin";
import { MemoryRouter } from "react-router";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { App } from "./app";

// A Component as ListCatalogue returns it; state is empty when it doesn't run.
type Comp = { name: string; state?: string; message?: string; held?: boolean };

const plugin = (name: string, title: string, components: Comp[], installed = false) => ({
  name,
  title,
  description: `${title} things.`,
  icon: "container",
  author: "itsKontra",
  installed,
  components,
});

let catalogue: {
  release: string;
  plugins: (ReturnType<typeof plugin> & { updatePending?: string })[];
  orphans?: string[];
  availableRelease?: string;
};
let calls: { method: string; body: unknown }[];
let refusal: { code: string; message: string } | undefined;

beforeEach(() => {
  calls = [];
  refusal = undefined;
  catalogue = {
    release: "v1.2.3",
    plugins: [
      plugin("checks", "Checks", [{ name: "ntfy-api", state: "ok" }, { name: "checks" }]),
      plugin("docker", "Docker", [{ name: "docker-api", state: "ok" }], true),
    ],
  };
});

afterEach(cleanup);

// The fake core answers ListCatalogue with catalogue, ListInstalledPlugins
// with nothing, and every Action with {} or the queued refusal.
const fetchFake = async (input: RequestInfo | URL, init?: RequestInit) => {
  const method = String(input).split("/").pop()!;
  calls.push({ method, body: JSON.parse(new TextDecoder().decode(init?.body as Uint8Array)) });
  if (method === "ListCatalogue") return Response.json(catalogue);
  if (method === "ListInstalledPlugins") return Response.json({});
  if (refusal) return Response.json(refusal, { status: 400 });
  return Response.json({});
};

const settle = () => act(() => new Promise((r) => setTimeout(r, 20)));

async function boot() {
  render(
    <MemoryRouter initialEntries={["/plugins"]}>
      <App client={createQueryClient()} fetch={vi.fn(fetchFake)} importBundle={async () => ({})} />
    </MemoryRouter>,
  );
  await settle();
}

const entry = (title: string) => screen.getByRole("listitem", { name: title });
const actions = () => calls.filter((c) => !c.method.startsWith("List"));
const catalogueReads = () => calls.filter((c) => c.method === "ListCatalogue").length;

it("lists every Plugin in the Release with its manifest and the Components it pulls in, marking running ones", async () => {
  await boot();

  const checks = entry("Checks");
  expect(within(checks).getByText("Checks things.")).toBeTruthy();
  expect(within(checks).getByRole("link", { name: "@itsKontra" }).getAttribute("href")).toBe("https://github.com/itsKontra");
  const pulls = within(within(checks).getByRole("list", { name: "Components" })).getAllByRole("listitem");
  expect(pulls.map((c) => c.textContent)).toEqual(["ntfy-api", "checks"]);
  expect(within(pulls[0]).getByRole("img", { name: "running" })).toBeTruthy();
  expect(within(pulls[1]).queryByRole("img")).toBeNull();
  expect(within(checks).getByRole("button", { name: "Install" })).toBeTruthy();
  expect(within(entry("Docker")).getByRole("button", { name: "Uninstall" })).toBeTruthy();
  expect(screen.getByText(/Release v1\.2\.3/)).toBeTruthy();
});

it("installs at once, without asking, and reads the Catalogue again", async () => {
  await boot();
  const reads = catalogueReads();

  fireEvent.click(within(entry("Checks")).getByRole("button", { name: "Install" }));
  await settle();

  expect(screen.queryByRole("alertdialog")).toBeNull();
  expect(actions()).toEqual([{ method: "Install", body: { plugin: "checks" } }]);
  expect(catalogueReads()).toBe(reads + 1);
});

it("asks before uninstalling, offering to delete the data too", async () => {
  await boot();

  fireEvent.click(within(entry("Docker")).getByRole("button", { name: "Uninstall" }));
  await settle();
  expect(screen.getByRole("alertdialog").textContent).toMatch(/Uninstall Docker\?/);
  expect(actions()).toEqual([]);
  fireEvent.click(screen.getByRole("checkbox", { name: "Also delete data" }));
  fireEvent.click(within(screen.getByRole("alertdialog")).getByRole("button", { name: "Uninstall" }));
  await settle();

  expect(actions()).toEqual([{ method: "Uninstall", body: { plugin: "docker", deleteData: true } }]);
});

it("doesn't uninstall when the Operator cancels", async () => {
  await boot();

  fireEvent.click(within(entry("Docker")).getByRole("button", { name: "Uninstall" }));
  await settle();
  fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
  await settle();

  expect(actions()).toEqual([]);
});

it("shows an installed Plugin's worst Component health, expandable to each Component", async () => {
  catalogue.plugins[1] = plugin(
    "docker",
    "Docker",
    [
      { name: "ntfy-api", state: "ok" },
      { name: "docker-api", state: "degraded", message: "media unreachable" },
      { name: "docker-ui", state: "starting" },
    ],
    true,
  );
  await boot();

  const health = within(entry("Docker")).getByRole("button", { name: /degraded/ });
  expect(health.getAttribute("aria-expanded")).toBe("false");
  expect(within(entry("Checks")).queryByRole("button", { name: /ok|degraded/ })).toBeNull();

  fireEvent.click(health);

  const rows = within(within(entry("Docker")).getByRole("list", { name: "Component health" })).getAllByRole("listitem");
  expect(rows.map((r) => r.textContent)).toEqual(["ntfy-apiok", "docker-apidegradedmedia unreachable", "docker-uistarting"]);
});

it("shows update pending, before any worse health, for an installed Plugin with a Component whose download failed", async () => {
  catalogue.plugins[1] = plugin(
    "docker",
    "Docker",
    [
      { name: "ntfy-api", state: "down", message: "unreachable" },
      { name: "docker-api", state: "update pending", message: "download failed: docker-api of Release v1.2.3: 502; retrying" },
    ],
    true,
  );
  await boot();

  fireEvent.click(within(entry("Docker")).getByRole("button", { name: /update pending/ }));

  const rows = within(within(entry("Docker")).getByRole("list", { name: "Component health" })).getAllByRole("listitem");
  expect(rows[1].textContent).toBe("docker-apiupdate pendingdownload failed: docker-api of Release v1.2.3: 502; retrying");
});

it("shows update pending, with the reason, for an installed Plugin whose bundles are still missing", async () => {
  catalogue.plugins[1] = {
    ...plugin("docker", "Docker", [{ name: "docker-api", state: "ok" }], true),
    updatePending: "download failed: docker.plugin.js of Release v1.2.3: 502; retrying",
  };
  await boot();

  fireEvent.click(within(entry("Docker")).getByRole("button", { name: /update pending/ }));

  expect(within(entry("Docker")).getByText("download failed: docker.plugin.js of Release v1.2.3: 502; retrying")).toBeTruthy();
});

it("shows a read-only line when a newer Release is available, and none otherwise", async () => {
  await boot();
  expect(screen.queryByText(/available/)).toBeNull();
  cleanup();

  catalogue.availableRelease = "v1.3.0";
  await boot();

  const line = screen.getByText("Release v1.3.0 available");
  expect(line.closest("a, button")).toBeNull();
});

it("offers Restart, after confirming, for a Component held down after repeated crashes", async () => {
  catalogue.plugins[1] = plugin(
    "docker",
    "Docker",
    [
      { name: "ntfy-api", state: "crashed", message: "exited unexpectedly; restarting in 2s" },
      { name: "docker-api", state: "crashed", message: "crashed 5 times in 10m0s; restart it from the Catalogue", held: true },
    ],
    true,
  );
  await boot();

  fireEvent.click(within(entry("Docker")).getByRole("button", { name: /crashed/ }));
  const rows = within(within(entry("Docker")).getByRole("list", { name: "Component health" })).getAllByRole("listitem");
  expect(within(rows[0]).queryByRole("button", { name: "Restart" })).toBeNull();
  fireEvent.click(within(rows[1]).getByRole("button", { name: "Restart" }));
  await settle();
  expect(screen.getByRole("alertdialog").textContent).toMatch(/Restart docker-api\?/);
  fireEvent.click(within(screen.getByRole("alertdialog")).getByRole("button", { name: "Restart" }));
  await settle();

  expect(actions()).toEqual([{ method: "Restart", body: { component: "docker-api" } }]);
});

it("lists orphaned DB files below, each deleted after confirming", async () => {
  catalogue.orphans = ["relay", "unifi-api"];
  await boot();

  const orphans = within(screen.getByRole("list", { name: "Orphaned data" })).getAllByRole("listitem");
  expect(orphans.map((o) => within(o).getByText(/\.db$/).textContent)).toEqual(["relay.db", "unifi-api.db"]);
  fireEvent.click(within(orphans[1]).getByRole("button", { name: "Delete" }));
  await settle();
  expect(screen.getByRole("alertdialog").textContent).toMatch(/Delete unifi-api's data\?/);
  fireEvent.click(within(screen.getByRole("alertdialog")).getByRole("button", { name: "Delete" }));
  await settle();

  expect(actions()).toEqual([{ method: "DeleteOrphan", body: { component: "unifi-api" } }]);
});

it("says nothing is orphaned when no DB file is", async () => {
  await boot();

  expect(screen.queryByRole("list", { name: "Orphaned data" })).toBeNull();
});

it("shows why an Action failed", async () => {
  refusal = { code: "not_found", message: "plugin checks isn't in Release v1.2.3" };
  await boot();

  fireEvent.click(within(entry("Checks")).getByRole("button", { name: "Install" }));
  await settle();

  expect(within(entry("Checks")).getByRole("alert").textContent).toMatch(/Install failed.*plugin checks isn't in Release v1\.2\.3/);
});
