import { act, cleanup, fireEvent, render, screen, within } from "@testing-library/react";
import { createQueryClient, PluginHost, PluginScope } from "@ddash/plugin";
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
    }),
  ],
};

// The fake host: ListChecks answers listed, an Action answers {} or the
// queued refusal. Every Action call is recorded with its JSON body.
let actions: { method: string; body: unknown }[];
let refusal: { code: string; message: string } | undefined;

beforeEach(() => {
  actions = [];
  refusal = undefined;
});

const fetch = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
  const method = String(input).split("/").pop()!;
  if (method === "ListChecks") return Response.json(listed);
  actions.push({ method, body: JSON.parse(new TextDecoder().decode(init?.body as Uint8Array)) });
  if (refusal) return Response.json(refusal, { status: 400 });
  return Response.json(method === "DeleteCheck" ? {} : { check: listed.checks[0] });
});

const settle = () => act(() => new Promise((r) => setTimeout(r, 50)));

async function open() {
  const Page = plugin.pages![0].Component!;
  render(
    <PluginHost client={createQueryClient()}>
      <PluginScope plugin={{ name: "checks", requires: ["checks"] }} fetch={fetch}>
        <Page />
      </PluginScope>
    </PluginHost>,
  );
  await settle();
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

it("lists App Checks, marks an expiring certificate, and leaves their editing to their App", async () => {
  await open();

  const jellyfin = row("Jellyfin");
  expect(within(jellyfin).getByText("HTTP")).toBeTruthy();
  expect(within(jellyfin).getByText("https://jellyfin.home.example")).toBeTruthy();
  expect(within(jellyfin).getByText("certificate expiring")).toBeTruthy();
  expect(within(jellyfin).getByRole("button", { name: "Mute" })).toBeTruthy();
  expect(within(jellyfin).getByRole("button", { name: "Disable" })).toBeTruthy();
  expect(within(jellyfin).queryByRole("button", { name: "Edit" })).toBeNull();
  expect(within(jellyfin).queryByRole("button", { name: "Delete" })).toBeNull();
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

  fireEvent.click(within(row("adguard")).getByRole("button", { name: "Edit" }));
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

it("mutes, unmutes, disables and enables from the row", async () => {
  await open();

  for (const [check, button] of [["nas", "Mute"], ["router", "Unmute"], ["nas", "Disable"], ["nvr", "Enable"]]) {
    fireEvent.click(within(row(check)).getByRole("button", { name: button }));
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

  fireEvent.click(within(row("router")).getByRole("button", { name: "Delete" }));
  await settle();
  const ask = screen.getByRole("alertdialog");
  expect(ask.textContent).toContain("Delete the Check router? Its history goes with it.");
  expect(actions).toEqual([]);
  fireEvent.click(within(ask).getByRole("button", { name: "Delete" }));
  await settle();

  expect(actions).toEqual([{ method: "DeleteCheck", body: { id: "2" } }]);
});

it("says so when there are no Checks", async () => {
  fetch.mockImplementationOnce(async () => Response.json({}));
  await open();

  expect(screen.getByText(/No Checks yet/)).toBeTruthy();
});
