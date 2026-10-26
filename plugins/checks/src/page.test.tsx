import { cleanup, fireEvent, screen, within } from "@testing-library/react";
import { metricValue, pending, renderScoped, settle, unavailable, type Answer } from "@ddash/plugin/testing";
import { useLocation } from "react-router";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import plugin from "./index";
import { check, checks } from "./testing";

afterEach(cleanup);
afterEach(() => vi.useRealTimers());

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

// The fake host: ListChecks answers listed (or listAnswer, when a test
// sets it), an Action answers {} or the queued refusal. Every Action call is
// recorded with its JSON body.
let actions: { method: string; body: unknown }[];
let refusal: { code: string; message: string } | undefined;
let listAnswer: Answer | undefined;

beforeEach(() => {
  actions = [];
  refusal = undefined;
  listAnswer = undefined;
  maintenance = [];
});

const answers = {
  ListChecks: (body: unknown) => (listAnswer ? listAnswer(body) : Response.json(listed)),
  ListMaintenance: () => Response.json({ maintenance }),
};

function answerAction(method: string, body: unknown) {
  actions.push({ method, body });
  if (refusal) return Response.json(refusal, { status: 400 });
  return Response.json(method === "DeleteCheck" ? {} : { check: listed.checks[0] });
}

// The Page's URL search, as the explorer last wrote it.
let search = "";

async function open(at = "/") {
  const Page = plugin.pages![0].Component!;
  function Where() {
    search = useLocation().search;
    return null;
  }
  renderScoped(
    checks,
    <>
      <Page />
      <Where />
    </>,
    { path: at, answers, unanswered: answerAction },
  );
  await settle();
}

/** The row of the Check named name (not its strip's row). */
const row = (name: string) => screen.getByRole("rowheader", { name: new RegExp(name) }).closest("tr")!;

/** Opens the strip of the Check named name by its row's arrow. */
function openStrip(name: string) {
  fireEvent.click(within(row(name)).getByRole("button", { name: `Actions for ${name}` }));
  return screen.getByRole("group", { name: `Actions for ${name}` });
}

/** The controls in a strip, by name, a ToggleButton's pressed state after it. */
const controls = (strip: HTMLElement) =>
  within(strip)
    .getAllByRole("button")
    .map((b) => b.textContent + (b.hasAttribute("aria-pressed") ? ` ${b.getAttribute("aria-pressed")}` : ""));

/** Clicks a strip's control with the focus on it first, as a browser does. */
function press(strip: HTMLElement, name: string) {
  const b = within(strip).getByRole("button", { name });
  b.focus();
  fireEvent.click(b);
  return b;
}

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
const checkNames = () => within(screen.getByRole("table")).queryAllByRole("rowheader").map((h) => h.querySelector("button")!.textContent);

it("searches the Checks by name or target, kept in the URL", async () => {
  await open();

  fireEvent.change(screen.getByRole("searchbox", { name: "Search Checks" }), { target: { value: "jellyfin" } });

  expect(checkNames()).toEqual(["Jellyfin", "adguard"]);
  expect(search).toBe("?q=jellyfin");
});

it("matches every word of the search, in the name or the target", async () => {
  await open("/?q=HTTPS%20jellyfin");

  // adguard resolves jellyfin.home.example, but nothing of it says https.
  expect(checkNames()).toEqual(["Jellyfin"]);
});

it("sorts by State, Down first, while the URL names no sort", async () => {
  await open();

  // Down, then warn (Flapping, an expiring certificate), Maintenance, up, then pending and disabled.
  expect(checkNames()).toEqual(["router", "Jellyfin", "camera", "Sonarr", "Radarr", "nas", "adguard", "nvr"]);
  expect(screen.getByRole("columnheader", { name: /State/ }).getAttribute("aria-sort")).toBe("ascending");
  expect(search).toBe("");
});

it("sorts by Target, kept in the URL, and opens on the sort a link carries", async () => {
  await open();

  fireEvent.click(within(screen.getByRole("columnheader", { name: /Target/ })).getByRole("button"));

  expect(checkNames()).toEqual(["camera", "Jellyfin", "Radarr", "Sonarr", "adguard", "nas", "nvr", "router"]);
  expect(search).toBe("?sort=target");

  cleanup();
  await open("/?sort=-state");
  expect(checkNames()).toEqual(["adguard", "nvr", "nas", "Sonarr", "Radarr", "Jellyfin", "camera", "router"]);
});

it("filters the Checks by type, kept in the URL", async () => {
  await open();

  fireEvent.change(screen.getByRole("combobox", { name: "Type" }), { target: { value: "HTTP" } });

  expect(checkNames()).toEqual(["Jellyfin", "Sonarr", "Radarr"]);
  expect(search).toBe("?type=HTTP");
});

it("splits the Checks into tabs with counts, the tab kept in the URL", async () => {
  await open();

  const tabs = within(screen.getByRole("tablist", { name: "Show" })).getAllByRole("tab");
  expect(tabs.map((t) => t.textContent)).toEqual(["All8", "Down1", "Attention3", "Maintenance2", "Muted1"]);
  fireEvent.click(screen.getByRole("tab", { name: /Attention/ }));

  expect(checkNames()).toEqual(["Jellyfin", "camera", "adguard"]);
  expect(search).toBe("?tab=attention");
});

it("opens on the search, type and tab a link carries", async () => {
  await open("/?tab=maint&type=HTTP&q=rad");

  expect(screen.getByRole("tab", { name: /Maintenance/ }).getAttribute("aria-selected")).toBe("true");
  expect(checkNames()).toEqual(["Radarr"]);
});

it("says so when no Check matches", async () => {
  await open("/?q=nothing-like-it");

  expect(checkNames()).toEqual([]);
  expect(screen.getByText('No Checks match "nothing-like-it"')).toBeTruthy();
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
  const strip = openStrip("Jellyfin");
  expect(controls(strip)).toEqual(["Maintenance", "Muted false", "Disabled false"]);
  expect(within(strip).getByRole("status").textContent).toMatch(/· Edited with its App$/);
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

  press(openStrip("adguard"), "Edit");
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

it("keeps a Check's Actions in its row's strip, with no ⋯ Menu", async () => {
  vi.useFakeTimers({ toFake: ["Date"] });
  vi.setSystemTime("2026-10-20T08:02:30Z");
  await open();

  const buttons = within(row("nas")).getAllByRole("button");
  expect(buttons.map((b) => b.getAttribute("aria-label") ?? b.textContent)).toEqual(["nas", "Actions for nas"]);
  expect(buttons[1].getAttribute("aria-haspopup")).toBeNull();

  const strip = openStrip("nas");
  expect(within(strip).getByRole("status").textContent).toBe("up · 12 ms · checked 2 min ago");
  expect(controls(strip)).toEqual(["Edit", "Maintenance", "Muted false", "Disabled false", "Delete"]);
  expect(controls(openStrip("router"))).toEqual(["Edit", "Maintenance", "Muted true", "Disabled false", "Delete"]);
  expect(screen.queryByRole("group", { name: "Actions for nas" })).toBeNull();
});

it("sums up a down Check by its reason, and one not probed yet", async () => {
  await open();

  expect(within(openStrip("router")).getByRole("status").textContent).toBe("down · connection refused · not checked yet");
  expect(within(openStrip("nvr")).getByRole("status").textContent).toBe("disabled · not checked yet");
});

it("opens a Check's detail on a click of its row, beside its arrow", async () => {
  await open();

  openStrip("nas");
  expect(screen.queryByRole("dialog")).toBeNull();
  fireEvent.click(within(row("nas")).getByText("nas.home.example:22"));

  expect(screen.getByRole("dialog", { name: "nas" })).toBeTruthy();
});

it("mutes, unmutes, disables and enables from the row's strip", async () => {
  await open();

  for (const [check, item] of [["nas", "Muted"], ["router", "Muted"], ["nas", "Disabled"], ["nvr", "Disabled"]]) {
    press(openStrip(check), item);
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

  press(openStrip("router"), "Delete");
  await settle();
  const ask = screen.getByRole("alertdialog");
  expect(ask.textContent).toContain("Delete the Check router? Its history goes with it.");
  expect(actions).toEqual([]);
  fireEvent.click(within(ask).getByRole("button", { name: "Delete" }));
  await settle();

  expect(actions).toEqual([{ method: "DeleteCheck", body: { id: "2" } }]);
});

it("says so when there are no Checks, with a way to add one", async () => {
  listAnswer = () => Response.json({});
  await open();

  expect(screen.getByText(/No Checks yet/)).toBeTruthy();
  expect(screen.queryByRole("table")).toBeNull();
  expect(screen.getAllByRole("button", { name: "Add Check" })).toHaveLength(2);
});

it("heads the Page with the up, down and Maintenance counts and the median latency", async () => {
  await open();

  expect(screen.getByRole("heading", { level: 1, name: "Checks" })).toBeTruthy();
  expect(metricValue("Up")).toBe("3of 7 enabled");
  expect(metricValue("Down")).toBe("1after 3 failures in a row");
  expect(metricValue("Maintenance")).toBe("2left out of uptime");
  // nas at 12.4 ms and camera at 30 ms; Jellyfin hasn't beaten yet.
  expect(metricValue("Median latency")).toBe("21 msacross up Checks");
});

it("draws skeletons, not text, until checks first answers", async () => {
  listAnswer = pending;
  await open();

  expect(metricValue("Up")).toBeUndefined();
  expect(within(screen.getByRole("table")).getByRole("status", { name: "Loading" })).toBeTruthy();
  expect(screen.queryByText(/Loading/)).toBeNull();
});

it("says what failed when checks can't list the Checks", async () => {
  listAnswer = () => unavailable("checks is down");
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

  press(openStrip("nas"), "Maintenance");
  const sheet = screen.getByRole("dialog", { name: "Maintenance for nas" });
  expect(within(sheet).queryByRole("radio", { name: "Everything" })).toBeNull();
  fireEvent.click(within(sheet).getByRole("radio", { name: "30 min" }));
  fireEvent.click(within(sheet).getByRole("button", { name: "Start" }));
  await settle();

  expect(actions).toEqual([{ method: "StartMaintenance", body: { scope: { checkId: "1" }, duration: "1800s" } }]);
});

it("leaves the strip open under a Sheet it opened, and gives the focus back to its Button", async () => {
  await open();

  for (const [name, title] of [["Edit", "Edit nas"], ["Maintenance", "Maintenance for nas"]]) {
    const strip = screen.queryByRole("group", { name: "Actions for nas" }) ?? openStrip("nas");
    const opener = press(strip, name);
    const sheet = screen.getByRole("dialog", { name: title });
    expect(screen.getByRole("group", { name: "Actions for nas" })).toBe(strip);
    fireEvent.click(within(sheet).getByRole("button", { name: "Cancel" }));
    await settle();

    expect(screen.queryByRole("dialog")).toBeNull();
    expect(screen.getByRole("group", { name: "Actions for nas" })).toBe(strip);
    expect(document.activeElement).toBe(opener);
  }
});
