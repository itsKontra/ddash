import { cleanup, screen, within } from "@testing-library/react";
import { pending, settle, unavailable } from "@ddash/plugin/testing";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import plugin from "./index";
import { down, now, notConfigured, Page, renderScoped, warn } from "./testing";

beforeEach(() => {
  vi.useFakeTimers({ toFake: ["Date"] });
  vi.setSystemTime(now);
});
afterEach(() => {
  cleanup();
  vi.useRealTimers();
});

const status = () => screen.getByRole("region", { name: "Status" });
const times = (el: HTMLElement) => within(el).queryAllByText((_, t) => t?.tagName === "TIME").map((t) => t.getAttribute("datetime"));

it("is the reverse-proxy Nav Entry at order 60 (its icon, waypoints, is in plugin.yaml)", () => {
  expect(plugin.nav?.order).toBe(60);
});

it("is the Reverse proxy Page, with a skeleton until Nginx Proxy Manager's status answers", async () => {
  renderScoped(<Page />, { GetStatus: pending });
  await settle();

  expect(screen.getByRole("heading", { level: 1, name: "Reverse proxy" }).previousElementSibling?.textContent).toBe("Infrastructure");
  expect(within(status()).getByRole("status", { name: "Loading" })).toBeTruthy();
});

it("shows Up with the read time and Nginx Proxy Manager's version", async () => {
  renderScoped(<Page />);
  await settle();

  expect(status().textContent).toMatch(/Up/);
  expect(status().textContent).toMatch(/Nginx Proxy Manager answers/);
  expect(status().textContent).toMatch(/2\.10\.4/);
  expect(times(status())).toEqual(["2026-10-26T07:59:00.000Z"]);
  expect(within(status()).queryByRole("region", { name: "Error" })).toBeNull();
});

it("shows Warn with the last good read and the error while reads fail after a good one", async () => {
  renderScoped(<Page />, { GetStatus: () => Response.json(warn) });
  await settle();

  expect(status().textContent).toMatch(/Warn/);
  expect(status().textContent).toMatch(/last good read/i);
  expect(times(status())).toEqual(["2026-10-26T07:59:00.000Z", "2026-10-26T07:15:00.000Z"]);
  expect(within(status()).getByRole("region", { name: "Error" }).textContent).toBe(warn.error);
});

it("shows Down with the error", async () => {
  renderScoped(<Page />, { GetStatus: () => Response.json(down) });
  await settle();

  expect(status().textContent).toMatch(/Down/);
  expect(status().textContent).toMatch(/can't read Nginx Proxy Manager/);
  expect(status().textContent).not.toMatch(/last good read/i);
  expect(within(status()).getByRole("region", { name: "Error" }).textContent).toBe(down.error);
});

it("says nginx-proxy-manager-api is unavailable when it can't answer", async () => {
  renderScoped(<Page />, { GetStatus: () => unavailable("nginx-proxy-manager-api is starting") });
  await settle();

  expect(within(status()).getByRole("alert").textContent).toBe("nginx-proxy-manager-api unavailable: nginx-proxy-manager-api is starting");
});

it("asks to set up Nginx Proxy Manager until it is configured, and shows nothing else", async () => {
  renderScoped(<Page />, { GetStatus: notConfigured, ListCertificates: notConfigured });
  await settle();

  expect(screen.getByText("Set up Nginx Proxy Manager")).toBeTruthy();
  expect(screen.getByRole("link", { name: "Configure it in Plugins" }).getAttribute("href")).toBe("/plugins");
  expect(screen.queryByRole("region", { name: "Status" })).toBeNull();
  expect(screen.queryByRole("region", { name: "Certificates" })).toBeNull();
});
