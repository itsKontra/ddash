import { act, cleanup, fireEvent, render, screen, within } from "@testing-library/react";
import { createQueryClient, PluginHost, PluginScope } from "@ddash/plugin";
import { MemoryRouter, useLocation } from "react-router";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import plugin from "./index";
import { check } from "./testing";

afterEach(cleanup);

// What checks answers ListChecks with (Connect JSON).
const listed = {
  checks: [
    check(1, "nas", "STATE_UP", { latencyMs: 12.4, lastBeat: "2026-10-20T08:00:00Z" }),
    check(2, "router", "STATE_DOWN", { reason: "connection refused", muted: true }),
    check(3, "adguard", "STATE_PENDING", {
      spec: { name: "adguard", type: "CHECK_TYPE_DNS", target: "198.18.1.2", dns: { name: "jellyfin.home.example", recordType: "RECORD_TYPE_A" } },
      failures: 1,
      reason: "timeout",
    }),
    check(4, "nvr", "STATE_UP", { disabled: true }),
    check(5, "Jellyfin", "STATE_UP", {
      appId: "proxy:jellyfin.home.example",
      spec: { name: "Jellyfin", type: "CHECK_TYPE_HTTP", target: "https://jellyfin.home.example" },
      certExpires: "2026-10-30T00:00:00Z",
      certExpiring: true,
      workload: { kind: "KIND_LXC", host: "pve1", id: "110" },
    }),
    check(6, "Sonarr", "STATE_DOWN", {
      appId: "proxy:sonarr.home.example",
      spec: { name: "Sonarr", type: "CHECK_TYPE_HTTP", target: "https://sonarr.home.example" },
      workload: { kind: "KIND_DOCKER", host: "media", id: "sonarr" },
      maintenance: true,
    }),
    check(7, "Radarr", "STATE_UP", {
      appId: "proxy:radarr.home.example",
      spec: { name: "Radarr", type: "CHECK_TYPE_HTTP", target: "https://radarr.home.example" },
      workload: { kind: "KIND_DOCKER", host: "media", id: "sonarr" },
      maintenance: true,
      maintenanceEnds: "2026-10-20T09:30:00Z",
    }),
    check(8, "camera", "STATE_UP", { flapping: true, latencyMs: 30, lastBeat: "2026-10-20T08:00:00Z" }),
  ],
};

// What checks answers ListMaintenance with; none unless a test sets it.
let maintenance: unknown[];

// The fake host: ListChecks answers listed, an Action answers {} or the
// queued refusal. Every Action call is recorded with its JSON body.
let actions: { method: string; body: unknown }[];
let refusal: { code: string; message: string } | undefined;

beforeEach(() => {
  actions = [];
  refusal = undefined;
  maintenance = [];
});

const fetch = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
  const method = String(input).split("/").pop()!;
  if (method === "ListChecks") return Response.json(listed);
  if (method === "ListMaintenance") return Response.json({ maintenance });
  actions.push({ method, body: JSON.parse(new TextDecoder().decode(init?.body as Uint8Array)) });
  if (refusal) return Response.json(refusal, { status: 400 });
  return Response.json(method === "DeleteCheck" ? {} : { check: listed.checks[0] });
});

const settle = () => act(() => new Promise((r) => setTimeout(r, 50)));

// The Page's URL search, as the explorer last wrote it.
let search = "";

async function open(at = "/") {
  const Page = plugin.pages![0].Component!;
  function Where() {
    search = useLocation().search;
    return null;
  }
  render(
    <MemoryRouter initialEntries={[at]}>
      <PluginHost client={createQueryClient()}>
        <PluginScope plugin={{ name: "checks", requires: ["checks"] }} fetch={fetch}>
          <Page />
          <Where />
        </PluginScope>
      </PluginHost>
    </MemoryRouter>,
  );
  await settle();
}

/** Picks item from the ⋯ Menu of the Check named name. */
function pick(name: string, item: string) {
  fireEvent.click(within(row(name)).getByRole("button", { name: `Actions for ${name}` }));
  fireEvent.click(screen.getByRole("menuitem", { name: item }));
}

/** The items of the ⋯ Menu of the Check named name; the Menu is closed again after. */
function itemsOf(name: string) {
  const opener = within(row(name)).getByRole("button", { name: `Actions for ${name}` });
  fireEvent.click(opener);
  const items = screen.getAllByRole("menuitem").map((i) => i.textContent);
  fireEvent.click(opener);
  return items;
}

const row = (name: string) => screen.getByRole("row", { name: new RegExp(name) });

it("is the Checks Nav Entry at order 20 with a badge (its icon, activity, is in plugin.yaml)", () => {
  expect(plugin.nav?.order).toBe(20);
  expect(plugin.nav?.useBadge).toBeTypeOf("function");
});

it("lists each Check with its state, latency or reason, and markers", async () => {
  await open();

  const nas = row("nas");
  expect(within(nas).getByRole("img", { name: "up" })).toBeTruthy();
  expect(within(nas).getByText("12 ms")).toBeTruthy();
  expect(within(nas).getByText("nas.home.example:22")).toBeTruthy();

  const router = row("router");
  expect(within(router).getByRole("img", { name: "down" })).toBeTruthy();
  expect(within(router).getByText("connection refused")).toBeTruthy();
  expect(within(router).getByText("muted")).toBeTruthy();

  const adguard = row("adguard");
  expect(within(adguard).getByRole("img", { name: "pending" })).toBeTruthy();
  expect(within(adguard).getByText("DNS")).toBeTruthy();
  expect(within(adguard).getByText(/jellyfin\.home\.example A @ 198\.18\.1\.2/)).toBeTruthy();

  expect(within(row("nvr")).getByRole("img", { name: "disabled" })).toBeTruthy();
});

/** The Checks the table shows, by name, in order. */
const shown = () => within(screen.getByRole("table")).queryAllByRole("rowheader").map((h) => h.querySelector("button")!.textContent);

it("searches the Checks by name or target, kept in the URL", async () => {
  await open();

  fireEvent.change(screen.getByRole("searchbox", { name: "Search Checks" }), { target: { value: "jellyfin" } });

  expect(shown()).toEqual(["adguard", "Jellyfin"]);
  expect(search).toBe("?q=jellyfin");
});

it("filters the Checks by type, kept in the URL", async () => {
  await open();

  fireEvent.change(screen.getByRole("combobox", { name: "Type" }), { target: { value: "HTTP" } });

  expect(shown()).toEqual(["Jellyfin", "Sonarr", "Radarr"]);
  expect(search).toBe("?type=HTTP");
});

it("splits the Checks into tabs with counts, the tab kept in the URL", async () => {
  await open();

  const tabs = within(screen.getByRole("tablist", { name: "Show" })).getAllByRole("tab");
  expect(tabs.map((t) => t.textContent)).toEqual(["All8", "Down1", "Attention3", "Maintenance2", "Muted1"]);
  fireEvent.click(screen.getByRole("tab", { name: /Attention/ }));

  expect(shown()).toEqual(["adguard", "Jellyfin", "camera"]);
  expect(search).toBe("?tab=attention");
});

it("opens on the search, type and tab a link carries", async () => {
  await open("/?tab=maint&type=HTTP&q=rad");

  expect(screen.getByRole("tab", { name: /Maintenance/ }).getAttribute("aria-selected")).toBe("true");
  expect(shown()).toEqual(["Radarr"]);
});

it("says so when no Check matches", async () => {
  await open("/?q=nothing-like-it");

  expect(shown()).toEqual([]);
  expect(screen.getByText("No Check matches.")).toBeTruthy();
});

it("marks a Flapping Check, warn while it is up", async () => {
  await open();

  const camera = row("camera");
  expect(within(camera).getByText("flapping")).toBeTruthy();
  expect(within(camera).getByRole("img", { name: "up, flapping" })).toBeTruthy();
  expect(within(camera).getByText("30 ms")).toBeTruthy();
});

it("lists App Checks, marks an expiring certificate, and leaves their editing to their App", async () => {
  await open();

  const jellyfin = row("Jellyfin");
  expect(within(jellyfin).getByText("HTTP")).toBeTruthy();
  expect(within(jellyfin).getByText("https://jellyfin.home.example")).toBeTruthy();
  expect(within(jellyfin).getByText("certificate expiring")).toBeTruthy();
  expect(itemsOf("Jellyfin")).toEqual(["Maintenance", "Mute", "Disable"]);
});

it("adds a Check from the form", async () => {
  await open();

  fireEvent.click(screen.getByRole("button", { name: "Add Check" }));
  const form = screen.getByRole("dialog", { name: "Add Check" });
  fireEvent.change(within(form).getByLabelText("Name"), { target: { value: "switch" } });
  fireEvent.click(within(form).getByRole("radio", { name: "ICMP" }));
  fireEvent.change(within(form).getByLabelText("Host"), { target: { value: "198.18.1.3" } });
  fireEvent.click(within(form).getByRole("button", { name: "Save" }));
  await settle();

  expect(actions).toEqual([{ method: "CreateCheck", body: { spec: { name: "switch", type: "CHECK_TYPE_ICMP", target: "198.18.1.3" } } }]);
  expect(screen.queryByRole("dialog")).toBeNull();
});

it("asks a DNS Check for its server, name and record type", async () => {
  await open();

  fireEvent.click(screen.getByRole("button", { name: "Add Check" }));
  const form = screen.getByRole("dialog", { name: "Add Check" });
  fireEvent.click(within(form).getByRole("radio", { name: "DNS" }));
  fireEvent.change(within(form).getByLabelText("DNS server"), { target: { value: "198.18.1.2" } });
  fireEvent.change(within(form).getByLabelText("Name to resolve"), { target: { value: "nas.home.example" } });
  expect(within(form).getByRole("radio", { name: "A" }).getAttribute("aria-checked")).toBe("true");
  fireEvent.click(within(form).getByRole("radio", { name: "AAAA" }));
  fireEvent.click(within(form).getByRole("button", { name: "Save" }));
  await settle();

  expect(actions[0].body).toEqual({
    spec: { type: "CHECK_TYPE_DNS", target: "198.18.1.2", dns: { name: "nas.home.example", recordType: "RECORD_TYPE_AAAA" } },
  });
});

it("keeps the form open with checks' refusal", async () => {
  await open();
  refusal = { code: "invalid_argument", message: "target must be host:port, e.g. 198.18.1.10:22" };

  fireEvent.click(screen.getByRole("button", { name: "Add Check" }));
  const form = screen.getByRole("dialog", { name: "Add Check" });
  fireEvent.change(within(form).getByLabelText("Host and port"), { target: { value: "198.18.1.3" } });
  fireEvent.click(within(form).getByRole("button", { name: "Save" }));
  await settle();

  expect(within(form).getByRole("alert").textContent).toContain("target must be host:port");
});

it("edits a Check from its row, filled in", async () => {
  await open();

  pick("adguard", "Edit");
  const form = screen.getByRole("dialog", { name: "Edit adguard" });
  expect((within(form).getByLabelText("Name to resolve") as HTMLInputElement).value).toBe("jellyfin.home.example");
  fireEvent.change(within(form).getByLabelText("DNS server"), { target: { value: "198.18.1.3" } });
  fireEvent.click(within(form).getByRole("button", { name: "Save" }));
  await settle();

  expect(actions).toEqual([
    {
      method: "UpdateCheck",
      body: { id: "3", spec: { name: "adguard", type: "CHECK_TYPE_DNS", target: "198.18.1.3", dns: { name: "jellyfin.home.example", recordType: "RECORD_TYPE_A" } } },
    },
  ]);
});

it("keeps a Check's Actions in its row's ⋯ Menu, never inline", async () => {
  await open();

  expect(itemsOf("nas")).toEqual(["Edit", "Maintenance", "Mute", "Disable", "Delete"]);
  const buttons = within(row("nas")).getAllByRole("button");
  expect(buttons.map((b) => b.getAttribute("aria-label") ?? b.textContent)).toEqual(["nas", "Actions for nas"]);
});

it("mutes, unmutes, disables and enables from the row's Menu", async () => {
  await open();

  for (const [check, item] of [["nas", "Mute"], ["router", "Unmute"], ["nas", "Disable"], ["nvr", "Enable"]]) {
    pick(check, item);
    await settle();
  }

  expect(actions).toEqual([
    { method: "SetMuted", body: { id: "1", muted: true } },
    { method: "SetMuted", body: { id: "2" } },
    { method: "SetDisabled", body: { id: "1", disabled: true } },
    { method: "SetDisabled", body: { id: "4" } },
  ]);
});

it("deletes a Check only once confirmed", async () => {
  await open();

  pick("router", "Delete");
  await settle();
  const ask = screen.getByRole("alertdialog");
  expect(ask.textContent).toContain("Delete the Check router? Its history goes with it.");
  expect(actions).toEqual([]);
  fireEvent.click(within(ask).getByRole("button", { name: "Delete" }));
  await settle();

  expect(actions).toEqual([{ method: "DeleteCheck", body: { id: "2" } }]);
});

it("says so when there are no Checks, with a way to add one", async () => {
  fetch.mockImplementationOnce(async () => Response.json({}));
  await open();

  expect(screen.getByText(/No Checks yet/)).toBeTruthy();
  expect(screen.queryByRole("table")).toBeNull();
  expect(screen.getAllByRole("button", { name: "Add Check" })).toHaveLength(2);
});

const tile = (label: string) => screen.getByRole("group", { name: label });

it("heads the Page with the up, down and Maintenance counts and the median latency", async () => {
  await open();

  expect(screen.getByRole("heading", { level: 1, name: "Checks" })).toBeTruthy();
  expect(tile("Up").textContent).toBe("Up3of 7 enabled");
  expect(tile("Down").textContent).toContain("1");
  expect(tile("Maintenance").textContent).toContain("2");
  // nas at 12.4 ms and camera at 30 ms; Jellyfin hasn't beaten yet.
  expect(tile("Median latency").textContent).toContain("21 ms");
});

it("draws skeletons, not text, until checks first answers", async () => {
  fetch.mockImplementationOnce(() => new Promise<Response>(() => {}));
  await open();

  expect(within(tile("Up")).getByRole("status", { name: "Loading" })).toBeTruthy();
  expect(within(screen.getByRole("table")).getByRole("status", { name: "Loading" })).toBeTruthy();
  expect(screen.queryByText(/Loading/)).toBeNull();
});

it("says what failed when checks can't list the Checks", async () => {
  fetch.mockImplementationOnce(async () => Response.json({ code: "unavailable", message: "checks is down" }, { status: 503 }));
  await open();

  expect(screen.getByRole("alert").textContent).toContain("checks is down");
  expect(screen.queryByRole("table")).toBeNull();
});

it("marks the Checks in Maintenance with when it ends", async () => {
  await open();

  const sonarr = row("Sonarr");
  expect(within(sonarr).getByRole("img", { name: "Maintenance" })).toBeTruthy();
  expect(within(sonarr).getByText("Maintenance until ended")).toBeTruthy();
  expect(within(row("Radarr")).getByText(/^Maintenance until \S/).textContent).not.toBe("Maintenance until ended");
  expect(within(row("nas")).queryByText(/Maintenance until/)).toBeNull();
});

it("lists the Maintenance in force and ends one early", async () => {
  maintenance = [
    { scope: { everything: true }, started: "2026-10-20T08:00:00Z", ends: "2026-10-20T09:00:00Z" },
    { scope: { workload: { kind: "KIND_DOCKER", host: "media", id: "sonarr" } }, started: "2026-10-20T08:00:00Z" },
    { scope: { checkId: "1" }, started: "2026-10-20T08:00:00Z", untilUp: true },
  ];
  await open();

  const region = screen.getByRole("region", { name: "Maintenance" });
  const items = within(region).getAllByRole("listitem");
  expect(items.map((i) => within(i).getByRole("img").getAttribute("aria-label"))).toEqual(["maint", "maint", "maint"]);
  expect(items.map((i) => i.textContent)).toEqual([
    expect.stringMatching(/^EverythingEnduntil \S/),
    "Docker sonarr on mediaEnduntil ended",
    "nasEnduntil its Checks are up",
  ]);

  fireEvent.click(within(items[1]).getByRole("button", { name: "End" }));
  await settle();

  expect(actions).toEqual([{ method: "EndMaintenance", body: { scope: { workload: { kind: "KIND_DOCKER", host: "media", id: "sonarr" } } } }]);
});

it("has no Maintenance list while none is in force", async () => {
  await open();

  expect(screen.queryByRole("region", { name: "Maintenance" })).toBeNull();
});

it("starts Maintenance on everything for 1 h by default", async () => {
  await open();

  fireEvent.click(screen.getByRole("button", { name: "Start Maintenance" }));
  const sheet = screen.getByRole("dialog", { name: "Maintenance" });
  expect(within(sheet).getByRole("radio", { name: "Everything" }).getAttribute("aria-checked")).toBe("true");
  expect(within(sheet).getByRole("radio", { name: "1 h" }).getAttribute("aria-checked")).toBe("true");
  fireEvent.click(within(sheet).getByRole("button", { name: "Start" }));
  await settle();

  expect(actions).toEqual([{ method: "StartMaintenance", body: { scope: { everything: true }, duration: "3600s" } }]);
  expect(screen.queryByRole("dialog")).toBeNull();
});

it("starts Maintenance on a Workload its App Checks run on, until ended", async () => {
  await open();

  fireEvent.click(screen.getByRole("button", { name: "Start Maintenance" }));
  const sheet = screen.getByRole("dialog", { name: "Maintenance" });
  fireEvent.click(within(sheet).getByRole("radio", { name: "A Workload" }));
  const pick = within(sheet).getByLabelText("Workload") as HTMLSelectElement;
  expect(Array.from(pick.options).map((o) => o.text)).toEqual(["Docker sonarr on media", "LXC 110"]);
  fireEvent.change(pick, { target: { value: pick.options[1].value } });
  fireEvent.click(within(sheet).getByRole("radio", { name: "Until ended" }));
  fireEvent.click(within(sheet).getByRole("button", { name: "Start" }));
  await settle();

  expect(actions).toEqual([{ method: "StartMaintenance", body: { scope: { workload: { kind: "KIND_LXC", host: "pve1", id: "110" } } } }]);
});

it("starts Maintenance for one Check from its row", async () => {
  await open();

  pick("nas", "Maintenance");
  const sheet = screen.getByRole("dialog", { name: "Maintenance for nas" });
  expect(within(sheet).queryByRole("radio", { name: "Everything" })).toBeNull();
  fireEvent.click(within(sheet).getByRole("radio", { name: "30 min" }));
  fireEvent.click(within(sheet).getByRole("button", { name: "Start" }));
  await settle();

  expect(actions).toEqual([{ method: "StartMaintenance", body: { scope: { checkId: "1" }, duration: "1800s" } }]);
});
