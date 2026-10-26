import { cleanup, fireEvent, screen } from "@testing-library/react";
import { settle, unavailable } from "@ddash/plugin/testing";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { apps, down, now, Page, renderScoped, up } from "./testing";

beforeEach(() => {
  vi.useFakeTimers({ toFake: ["Date"] });
  vi.setSystemTime(now);
});
afterEach(() => {
  cleanup();
  vi.useRealTimers();
});

const offline = () => screen.queryByRole("link", { name: /offline/ });

it("counts the Offline Proxy Hosts whose Warning counts, linking to the Apps that need attention", async () => {
  renderScoped(<Page />, {}, ["reverse-proxy", "apps"]);
  await settle();

  // Jellyfin's and NAS's: Printer's is Ignored, Old is Hidden.
  expect(offline()?.textContent).toBe("2 Proxy Hosts offline");
  expect(offline()?.getAttribute("href")).toBe("/apps");
});

it("says one Proxy Host offline in the singular, as plain text without the Apps Plugin", async () => {
  const one = { ...apps, apps: apps.apps.filter((a) => a.name !== "NAS") };
  renderScoped(<Page />, { ListApps: () => Response.json(one) });
  await settle();

  expect(offline()).toBeNull();
  expect(screen.getByText("1 Proxy Host offline")).toBeTruthy();
});

it("shows no offline line while no Offline Proxy Host counts", async () => {
  const none = { ...apps, apps: apps.apps.filter((a) => a.name !== "NAS" && a.name !== "Jellyfin") };
  renderScoped(<Page />, { ListApps: () => Response.json(none) }, ["reverse-proxy", "apps"]);
  await settle();

  expect(screen.queryByText(/offline/)).toBeNull();
});

it("re-reads Nginx Proxy Manager and the Apps with Refresh, and shows the new reads", async () => {
  let status: object = down;
  const calls = renderScoped(<Page />, {
    GetStatus: () => Response.json(status),
    Refresh: () => {
      status = up;
      return Response.json({});
    },
  });
  await settle();

  fireEvent.click(screen.getByRole("button", { name: "Refresh" }));
  await settle();

  expect(calls.filter((c) => c.method === "Refresh").map((c) => c.url)).toEqual([
    "/api/c/nginx-proxy-manager-api/ddash.nginxproxymanagerapi.NginxProxyManagerApiService/Refresh",
    "/api/c/apps/ddash.apps.AppsService/Refresh",
  ]);
  expect(screen.getByRole("region", { name: "Status" }).textContent).toMatch(/Nginx Proxy Manager answers/);
});

it("says why a Refresh failed", async () => {
  renderScoped(<Page />, { Refresh: () => unavailable("nginx-proxy-manager-api is restarting") });
  await settle();

  fireEvent.click(screen.getByRole("button", { name: "Refresh" }));
  await settle();

  expect(screen.getAllByRole("alert").map((a) => a.textContent)).toContain("Refresh failed: nginx-proxy-manager-api is restarting");
});
