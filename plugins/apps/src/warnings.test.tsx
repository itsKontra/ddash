import { cleanup, fireEvent, screen, within } from "@testing-library/react";
import { afterEach, expect, it } from "vitest";
import { settle } from "@ddash/plugin/testing";
import plugin from "./index";
import { actions, renderScoped, split } from "./testing";

afterEach(cleanup);

const Page = () => {
  const P = plugin.pages![0].Component!;
  return <P />;
};

const open = async (path: string, answers = {}) => {
  const calls = renderScoped(<Page />, { ListApps: () => Response.json(split), ...answers }, ["apps"], path);
  await settle();
  return calls;
};

const pane = () => screen.getByRole("article");
const box = (name: string) => within(pane()).getByRole("region", { name });
const buttons = (name: string) => within(box(name)).getAllByRole("button").map((b) => [b.textContent, (b as HTMLButtonElement).disabled]);

it("shows the Warnings that count as boxes at the top of the pane, each with its fixes", async () => {
  await open("/?app=proxy:jellyfin.home.example");

  const regions = within(pane())
    .getAllByRole("region")
    .map((r) => r.getAttribute("aria-label") ?? r.querySelector("h2")?.textContent);
  expect(regions.slice(0, 3)).toEqual(["Proxy Host offline", "Hostname on two Apps", "Display"]);
  expect(box("Proxy Host offline").textContent).toContain("nginx: [emerg] host not found in upstream");
  expect(buttons("Proxy Host offline")).toEqual([
    ["Save again", false],
    ["Delete App…", false],
    ["Ignore", false],
  ]);
  expect(box("Hostname on two Apps").textContent).toContain("media.home.example");
  expect(buttons("Hostname on two Apps")).toEqual([
    ["Merge NAS into this App", false],
    ["Ignore", false],
  ]);
});

it("Ignores a Warning on this App", async () => {
  const calls = await open("/?app=proxy:jellyfin.home.example");

  fireEvent.click(within(box("Proxy Host offline")).getByRole("button", { name: "Ignore" }));
  await settle();

  expect(actions(calls)).toEqual([{ method: "IgnoreWarning", body: { id: "proxy:jellyfin.home.example", kind: "WARNING_KIND_PROXY_HOST_OFFLINE" } }]);
});

it("shows Ignored Warnings only behind Show ignored Warnings, and un-ignores them", async () => {
  const calls = await open("/?app=proxy:printer.home.example");
  expect(within(pane()).queryByRole("region", { name: "No TLS" })).toBeNull();

  fireEvent.click(screen.getByRole("checkbox", { name: "Show ignored Warnings (1)" }));
  await settle();
  expect(within(box("No TLS")).getByText("Ignored")).toBeTruthy();
  fireEvent.click(within(box("No TLS")).getByRole("button", { name: "Un-ignore" }));
  await settle();

  expect(actions(calls)).toEqual([{ method: "UnignoreWarning", body: { id: "proxy:printer.home.example", kind: "WARNING_KIND_NO_TLS" } }]);
});

it("merges the other App sharing the hostname into this one, after a confirm", async () => {
  const calls = await open("/?app=proxy:jellyfin.home.example");

  fireEvent.click(within(box("Hostname on two Apps")).getByRole("button", { name: "Merge NAS into this App" }));
  const confirm = screen.getByRole("alertdialog");
  expect(confirm.textContent).toContain("Merge NAS into Jellyfin?");
  fireEvent.click(within(confirm).getByRole("button", { name: "Merge" }));
  await settle();

  expect(actions(calls)).toEqual([{ method: "Merge", body: { id: "proxy:jellyfin.home.example", from: "app:5a5a5a5a5a5a5a5a" } }]);
});

it("shows why a merge was refused", async () => {
  await open("/?app=proxy:jellyfin.home.example", {
    Merge: () => Response.json({ code: "failed_precondition", message: "both Apps have a Proxy Host" }, { status: 400 }),
  });

  fireEvent.click(within(box("Hostname on two Apps")).getByRole("button", { name: "Merge NAS into this App" }));
  fireEvent.click(within(screen.getByRole("alertdialog")).getByRole("button", { name: "Merge" }));
  await settle();

  expect(within(box("Hostname on two Apps")).getByRole("alert").textContent).toMatch(/both Apps have a Proxy Host/);
});

it("reattaches orphaned App settings to an App without any, from their Warning box", async () => {
  const calls = await open("/?app=proxy:paperless.home.example");

  expect(buttons("Orphaned App settings")).toEqual([
    ["Reattach to an App…", false],
    ["Delete", false],
    ["Ignore", false],
  ]);
  fireEvent.click(within(box("Orphaned App settings")).getByRole("button", { name: "Reattach to an App…" }));
  const dialog = screen.getByRole("dialog", { name: "Reattach Documents" });
  const target = within(dialog).getByRole("combobox", { name: "Reattach to" });
  // Only discovered Apps without settings of their own.
  expect(within(target).getAllByRole("option").map((o) => o.getAttribute("value"))).toEqual(["", "proxy:jellyfin.home.example", "proxy:printer.home.example"]);
  expect((within(dialog).getByRole("button", { name: "Reattach" }) as HTMLButtonElement).disabled).toBe(true);
  fireEvent.change(target, { target: { value: "proxy:printer.home.example" } });
  fireEvent.click(within(dialog).getByRole("button", { name: "Reattach" }));
  await settle();

  expect(actions(calls)).toEqual([{ method: "Reattach", body: { id: "proxy:paperless.home.example", to: "proxy:printer.home.example" } }]);
  expect(screen.queryByRole("dialog", { name: "Reattach Documents" })).toBeNull();
});

it("deletes orphaned App settings from their Warning box after a confirm", async () => {
  const calls = await open("/?app=proxy:paperless.home.example");

  fireEvent.click(within(box("Orphaned App settings")).getByRole("button", { name: "Delete" }));
  const confirm = screen.getByRole("alertdialog");
  expect(confirm.textContent).toContain("Delete the settings kept for Documents?");
  fireEvent.click(within(confirm).getByRole("button", { name: "Delete" }));
  await settle();

  expect(actions(calls)).toEqual([{ method: "DeleteOrphan", body: { id: "proxy:paperless.home.example" } }]);
  expect(screen.queryByRole("article")).toBeNull();
});

it("opens the Workload Panel's Edit from Set a Workload override, and uses a Proxy Host name as the URL at once", async () => {
  const old = split.apps.find((a) => a.name === "Old")!;
  const renamed = {
    ...old,
    hidden: false,
    warnings: [
      ...old.warnings!,
      { kind: "WARNING_KIND_URL_NOT_ON_PROXY_HOST", fixes: [{ kind: "FIX_KIND_USE_AS_URL", hostname: "older.home.example" }] },
    ],
  };
  const calls = await open("/?app=proxy:old.home.example", { ListApps: () => Response.json({ ...split, apps: split.apps.map((a) => (a.id === old.id ? renamed : a)) }) });

  fireEvent.click(within(box("No Workload")).getByRole("button", { name: "Set a Workload override" }));
  await settle();
  expect(within(within(pane()).getByRole("region", { name: "Workload and Check" })).getByRole("combobox", { name: "Workload" })).toBeTruthy();

  fireEvent.click(within(box("URL not on the Proxy Host")).getByRole("button", { name: "Use older.home.example as the URL" }));
  await settle();
  expect(actions(calls)).toEqual([{ method: "PromoteAlias", body: { id: "proxy:old.home.example", alias: "older.home.example" } }]);
});
