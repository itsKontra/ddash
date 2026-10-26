import { cleanup, screen, within } from "@testing-library/react";
import { afterEach, expect, it } from "vitest";
import { settle } from "@ddash/plugin/testing";
import plugin from "./index";
import { list, renderScoped } from "./testing";

afterEach(cleanup);

const Page = () => {
  const P = plugin.pages![0].Component!;
  return <P />;
};

const show = async (answer: object) => {
  renderScoped(<Page />, { ListApps: () => Response.json(answer) });
  await settle();
  return screen.queryAllByRole("status").filter((s) => s.getAttribute("aria-label") !== "Check");
};

it("shows no Notice while both sources read and the Reverse Proxy's Host Override is one", async () => {
  expect(await show(list)).toEqual([]);
});

it("asks to set up Nginx Proxy Manager until it is configured", async () => {
  const [notice] = await show({ apps: [], proxyHosts: { error: "not configured", notConfigured: true } });

  expect(notice.textContent).toMatch(/^Set up Nginx Proxy Manager/);
  expect(within(notice).getByRole("link", { name: "Configure it in Plugins" }).getAttribute("href")).toBe("/plugins");
  expect(screen.getByText("No Apps yet")).toBeTruthy();
});

it("says the Proxy Hosts are read-only", async () => {
  const [notice] = await show({ ...list, proxyHosts: { ...list.proxyHosts, readOnly: true } });

  expect(notice.textContent).toMatch(/^Proxy Hosts are read-only/);
  expect(notice.textContent).toContain("Set them to Manage");
});

it("says why proxied writes are blocked: the Reverse Proxy's Host Override problem", async () => {
  const blocked = "2 Host Overrides point at the Reverse Proxy (198.18.1.210): rp.home.example, old.home.example. Keep one to add proxied Apps.";
  const notices = await show({ ...list, reverseProxy: { address: "198.18.1.210", hostOverrides: ["rp.home.example", "old.home.example"], notice: blocked } });

  expect(notices.map((n) => n.textContent)).toEqual([blocked]);
});

it("says which source is unreadable since when, still showing its last good read", async () => {
  const since = "2026-10-26T07:15:00Z";
  const notices = await show({
    ...list,
    proxyHosts: { ...list.proxyHosts, error: "nginx-proxy-manager-api unavailable: sign-in refused", failingSince: since },
    hostOverrides: { error: "pfsense-api unavailable: timeout", failingSince: since },
  });

  expect(notices).toHaveLength(2);
  expect(notices[0].textContent).toMatch(/^Nginx Proxy Manager unreadable/);
  expect(notices[0].textContent).toContain("nginx-proxy-manager-api unavailable: sign-in refused. Showing the Proxy Hosts of its last good read.");
  expect(within(notices[0]).getByText((_, el) => el?.tagName === "TIME").getAttribute("datetime")).toBe("2026-10-26T07:15:00.000Z");
  expect(notices[1].textContent).toMatch(/^pfSense unreadable/);
  expect(notices[1].textContent).toContain("pfsense-api unavailable: timeout. No Host Overrides read since ddash started.");
  // The Apps of the last good read stay listed.
  expect(screen.getByRole("navigation", { name: "Apps" })).toBeTruthy();
});
