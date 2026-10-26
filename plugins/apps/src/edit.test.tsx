import { cleanup, fireEvent, screen, within } from "@testing-library/react";
import { useLocation } from "react-router";
import { afterEach, expect, it } from "vitest";
import { settle } from "@ddash/plugin/testing";
import plugin from "./index";
import { actions, renderScoped, split } from "./testing";

afterEach(cleanup);

let search = "";
function Where() {
  search = useLocation().search;
  return null;
}

const Page = () => {
  const P = plugin.pages![0].Component!;
  return (
    <>
      <P />
      <Where />
    </>
  );
};

const open = async (id: string, answers = {}) => {
  const calls = renderScoped(<Page />, { ListApps: () => Response.json(split), ...answers }, ["apps"], `/?app=${encodeURIComponent(id)}`);
  await settle();
  return calls;
};

const pane = () => screen.getByRole("article");
const panel = (name: string) => within(pane()).getByRole("region", { name });
const editButtons = () => within(pane()).queryAllByRole("button", { name: /^Edit / });

it("saves the Display settings at once, keeping the Workload override", async () => {
  const calls = await open("app:5a5a5a5a5a5a5a5a");

  fireEvent.click(screen.getByRole("button", { name: "Edit Display" }));
  const display = within(panel("Display"));
  fireEvent.change(display.getByLabelText("Name"), { target: { value: "Storage" } });
  fireEvent.change(display.getByLabelText("Group"), { target: { value: "Infra" } });
  fireEvent.click(display.getByRole("checkbox", { name: "Hidden" }));
  fireEvent.click(display.getByRole("button", { name: /^Icon/ }));
  fireEvent.change(display.getByRole("combobox", { name: "Search icons" }), { target: { value: "synology" } });
  fireEvent.click(await display.findByRole("option", { name: "Use synology" }));
  fireEvent.click(display.getByRole("button", { name: "Save" }));
  await settle();

  expect(actions(calls)).toEqual([
    {
      method: "SetSettings",
      body: { id: "app:5a5a5a5a5a5a5a5a", settings: { name: "Storage", icon: "synology", group: "Infra", hidden: true, workload: { kind: "KIND_EXTERNAL", host: "198.18.1.20" } } },
    },
  ]);
  // Saved: the Panel shows its facts again.
  expect(within(panel("Display")).queryByRole("button", { name: "Save" })).toBeNull();
});

it("edits one Panel at a time", async () => {
  await open("proxy:jellyfin.home.example");
  expect(editButtons().map((b) => b.getAttribute("aria-label"))).toEqual(["Edit Display", "Edit Address and DNS", "Edit Workload and Check"]);

  fireEvent.click(screen.getByRole("button", { name: "Edit Display" }));
  expect(editButtons().map((b) => [b.getAttribute("aria-label"), (b as HTMLButtonElement).disabled])).toEqual([
    ["Edit Address and DNS", true],
    ["Edit Workload and Check", true],
  ]);

  fireEvent.click(within(panel("Display")).getByRole("button", { name: "Cancel" }));
  expect(editButtons().every((b) => !(b as HTMLButtonElement).disabled)).toBe(true);
});

it("shows why a save was refused and keeps the Panel editing", async () => {
  await open("proxy:jellyfin.home.example", {
    SetSettings: () => Response.json({ code: "invalid_argument", message: "an external Workload needs its host" }, { status: 400 }),
  });

  fireEvent.click(screen.getByRole("button", { name: "Edit Display" }));
  fireEvent.click(within(panel("Display")).getByRole("button", { name: "Save" }));
  await settle();

  expect(within(panel("Display")).getByRole("alert").textContent).toMatch(/an external Workload needs its host/);
  expect(within(panel("Display")).getByRole("button", { name: "Save" })).toBeTruthy();
});

it("resets a touched App's settings, and offers no reset on an untouched one", async () => {
  const calls = await open("proxy:jellyfin.home.example");
  fireEvent.click(screen.getByRole("button", { name: "Edit Display" }));
  expect(within(panel("Display")).queryByRole("button", { name: "Reset all settings" })).toBeNull();

  cleanup();
  const touched = await open("proxy:grafana.home.example");
  fireEvent.click(screen.getByRole("button", { name: "Edit Display" }));
  fireEvent.click(within(panel("Display")).getByRole("button", { name: "Reset all settings" }));
  await settle();

  expect(actions(calls)).toEqual([]);
  expect(actions(touched)).toEqual([{ method: "SetSettings", body: { id: "proxy:grafana.home.example", settings: {} } }]);
});

it("overrides the Workload with one the sources list, at once", async () => {
  const calls = await open("proxy:jellyfin.home.example");

  fireEvent.click(screen.getByRole("button", { name: "Edit Workload and Check" }));
  await settle();
  const select = within(panel("Workload and Check")).getByRole("combobox", { name: "Workload" });
  expect(within(select).getAllByRole("option").map((o) => o.textContent)).toEqual([
    "Matched: Docker jellyfin",
    "Docker jellyfin (media)",
    "LXC media-stack (pve/102)",
    "LXC grafana (pve/120)",
    "VM home-assistant (pve/110)",
    "External host…",
  ]);
  fireEvent.change(select, { target: { value: "1|pve|102" } });
  fireEvent.click(within(panel("Workload and Check")).getByRole("button", { name: "Save" }));
  await settle();

  expect(actions(calls)).toEqual([
    { method: "SetSettings", body: { id: "proxy:jellyfin.home.example", settings: { workload: { kind: "KIND_LXC", host: "pve", id: "102" } } } },
  ]);
});

it("sets an external host as the Workload", async () => {
  const calls = await open("proxy:jellyfin.home.example");

  fireEvent.click(screen.getByRole("button", { name: "Edit Workload and Check" }));
  await settle();
  fireEvent.change(within(panel("Workload and Check")).getByRole("combobox", { name: "Workload" }), { target: { value: "external" } });
  fireEvent.change(within(panel("Workload and Check")).getByLabelText("External host"), { target: { value: "198.18.1.50" } });
  fireEvent.click(within(panel("Workload and Check")).getByRole("button", { name: "Save" }));
  await settle();

  expect(actions(calls)).toEqual([
    { method: "SetSettings", body: { id: "proxy:jellyfin.home.example", settings: { workload: { kind: "KIND_EXTERNAL", host: "198.18.1.50" } } } },
  ]);
});

it("promotes an alias to the URL, at once", async () => {
  const calls = await open("proxy:jellyfin.home.example");

  fireEvent.click(screen.getByRole("button", { name: "Edit Address and DNS" }));
  fireEvent.click(within(panel("Address and DNS")).getByRole("button", { name: "Make tv.home.example the URL" }));
  await settle();

  expect(actions(calls)).toEqual([{ method: "PromoteAlias", body: { id: "proxy:jellyfin.home.example", alias: "tv.home.example" } }]);
  expect(within(panel("Address and DNS")).queryByRole("button", { name: /^Make / })).toBeNull();
});

it("offers no Address Edit without aliases to promote", async () => {
  await open("proxy:grafana.home.example");

  expect(screen.queryByRole("button", { name: "Edit Address and DNS" })).toBeNull();
});

it("deletes a Link-only App after a confirm; Delete… on others waits for the write tickets", async () => {
  const calls = await open("app:00aa11bb22cc33dd");

  fireEvent.click(within(pane()).getByRole("button", { name: "Delete…" }));
  const confirm = screen.getByRole("alertdialog");
  expect(confirm.textContent).toContain("Delete the App pfSense?");
  fireEvent.click(within(confirm).getByRole("button", { name: "Delete" }));
  await settle();

  expect(actions(calls)).toEqual([{ method: "DeleteApp", body: { id: "app:00aa11bb22cc33dd" } }]);
  expect(search).toBe("");

  cleanup();
  await open("proxy:grafana.home.example");
  expect((within(pane()).getByRole("button", { name: "Delete…" }) as HTMLButtonElement).disabled).toBe(true);
});
