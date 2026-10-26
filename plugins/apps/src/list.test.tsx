import { cleanup, fireEvent, screen, within } from "@testing-library/react";
import { useLocation } from "react-router";
import { afterEach, expect, it } from "vitest";
import { settle, unavailable } from "@ddash/plugin/testing";
import plugin from "./index";
import { actions, renderScoped, split } from "./testing";

afterEach(cleanup);

// The Page's URL search, as the list last wrote it.
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

const show = async (path = "/", answers = {}) => {
  const calls = renderScoped(<Page />, { ListApps: () => Response.json(split), ...answers }, ["apps"], path);
  await settle();
  return calls;
};

const nav = () => screen.getByRole("navigation", { name: "Apps" });

/** Each group of the list as its title followed by its rows' names. */
const groups = () =>
  within(nav())
    .queryAllByRole("region")
    .map((g) => [g.getAttribute("aria-label"), ...within(g).queryAllByRole("button", { name: (_, el) => el.hasAttribute("data-app") }).map((b) => b.getAttribute("aria-label"))]);

it("is the apps Nav Entry at order 10 (its icon, layout-grid, is in plugin.yaml)", () => {
  expect(plugin.nav?.order).toBe(10);
});

it("titles the Page Apps under its kicker", async () => {
  await show();

  const title = screen.getByRole("heading", { level: 1, name: "Apps" });
  expect(title.previousElementSibling?.textContent).toBe("Services");
});

it("pins Needs attention over the groups, ungrouped Apps after them, and folds Hidden with its count", async () => {
  await show();

  expect(groups()).toEqual([
    ["Needs attention", "Documents", "Jellyfin", "NAS"],
    ["Monitoring", "Grafana"],
    ["Network", "pfSense"],
    ["Office", "Documents"],
    ["Ungrouped", "Jellyfin", "NAS", "Printer"],
    ["Hidden (2)"],
  ]);
});

it("notes each row in Needs attention with its first Warning that counts", async () => {
  await show();

  const attention = within(nav()).getByRole("region", { name: "Needs attention" });
  const jellyfin = within(attention).getByRole("button", { name: "Jellyfin" });
  expect(jellyfin.textContent).toContain("Proxy Host offline");
  expect(within(attention).getByRole("button", { name: "Documents" }).textContent).toContain("Orphaned App settings");
});

it("counts neither Ignored Warnings nor any on a Hidden App toward Needs attention", async () => {
  await show();

  // Printer's No TLS is Ignored; Old's No Workload is on a Hidden App.
  const attention = within(nav()).getByRole("region", { name: "Needs attention" });
  expect(within(attention).queryByRole("button", { name: "Printer" })).toBeNull();
  expect(within(attention).queryByRole("button", { name: "Old" })).toBeNull();
});

it("shows Apps with only Ignored Warnings in Needs attention behind Show ignored Warnings, counted, kept in the URL", async () => {
  await show();

  const toggle = screen.getByRole("checkbox", { name: "Show ignored Warnings (1)" });
  fireEvent.click(toggle);
  await settle();

  expect(groups()[0]).toEqual(["Needs attention", "Documents", "Jellyfin", "NAS", "Printer"]);
  expect(search).toBe("?ignored=1");
});

it("offers no Show ignored Warnings while none is Ignored", async () => {
  const none = { ...split, apps: split.apps.map((a) => ({ ...a, warnings: a.warnings?.filter((w) => !("ignored" in w && w.ignored)) })) };
  await show("/", { ListApps: () => Response.json(none) });

  expect(screen.queryByRole("checkbox", { name: /Show ignored Warnings/ })).toBeNull();
});

it("unfolds Hidden on request, and while the selected App is Hidden", async () => {
  await show();

  fireEvent.click(within(nav()).getByRole("button", { name: "Show Hidden Apps" }));
  expect(groups().at(-1)).toEqual(["Hidden (2)", "Old", "Proxmox"]);
  fireEvent.click(within(nav()).getByRole("button", { name: "Fold Hidden Apps" }));
  expect(groups().at(-1)).toEqual(["Hidden (2)"]);

  cleanup();
  await show("/?app=proxy:old.home.example");
  expect(groups().at(-1)).toEqual(["Hidden (2)", "Old", "Proxmox"]);
});

it("searches every group, Hidden included, by name, hostname, alias or group, kept in the URL", async () => {
  await show();
  const find = (q: string) => {
    fireEvent.change(screen.getByRole("searchbox", { name: "Search Apps" }), { target: { value: q } });
    return groups();
  };

  expect(find("graf")).toEqual([["Monitoring", "Grafana"]]);
  expect(find("tv.home")).toEqual([
    ["Needs attention", "Jellyfin"],
    ["Ungrouped", "Jellyfin"],
  ]);
  expect(find("infra")).toEqual([["Hidden (1)", "Proxmox"]]);
  expect(search).toBe("?q=infra");
  expect(find("nothing-like-it")).toEqual([]);
  expect(within(nav()).getByText('No Apps match "nothing-like-it"')).toBeTruthy();
});

it("selects an App into the pane, the selection in the URL", async () => {
  await show();
  expect(screen.getByText("Pick an App on the left, or add one.")).toBeTruthy();

  const row = within(within(nav()).getByRole("region", { name: "Monitoring" })).getByRole("button", { name: "Grafana" });
  fireEvent.click(row);
  await settle();

  expect(search).toBe("?app=proxy%3Agrafana.home.example");
  expect(screen.getByRole("heading", { level: 2, name: "Grafana" })).toBeTruthy();
  expect(row.getAttribute("aria-current")).toBe("true");
});

it("opens the App a link names", async () => {
  await show("/?app=app:5a5a5a5a5a5a5a5a");

  expect(screen.getByRole("heading", { level: 2, name: "NAS" })).toBeTruthy();
});

it("says so when the App a link names is gone", async () => {
  await show("/?app=proxy:gone.home.example");

  expect(screen.getByText("No App has the id proxy:gone.home.example. Its sources may be gone.")).toBeTruthy();
});

it("refreshes, saying when the sources were read", async () => {
  const calls = await show();

  // The older of the two reads: pfSense's, 6 min ago.
  expect(screen.getByText("read 6 min ago")).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: "Refresh" }));
  await settle();

  expect(actions(calls)).toEqual([{ method: "Refresh", body: {} }]);
});

it("shows a skeleton while apps answers, and apps being unavailable as a failure", async () => {
  renderScoped(<Page />, { ListApps: () => unavailable("apps is down") });
  expect(screen.getByRole("status", { name: "Loading" })).toBeTruthy();
  await settle();

  expect(screen.getByRole("alert").textContent).toMatch(/apps unavailable: apps is down/);
  expect(screen.queryByRole("status", { name: "Loading" })).toBeNull();
});
