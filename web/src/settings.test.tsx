import { act, cleanup, fireEvent, render, screen, within } from "@testing-library/react";
import { createQueryClient } from "@ddash/plugin";
import { MemoryRouter } from "react-router";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { App } from "./app";

let saves: Record<string, unknown>[];
let prefersDark: boolean;

beforeEach(() => {
  saves = [];
  prefersDark = false;
  window.localStorage.clear();
  document.documentElement.className = "";
  delete document.documentElement.dataset.theme;
  window.matchMedia = ((query: string) => ({
    matches: query === "(prefers-color-scheme: dark)" && prefersDark,
    media: query,
    addEventListener: () => {},
    removeEventListener: () => {},
  })) as unknown as typeof window.matchMedia;
});

afterEach(cleanup);

const fetchFake = async (input: RequestInfo | URL, init?: RequestInit) => {
  const method = String(input).split("/").pop()!;
  const body = JSON.parse(new TextDecoder().decode(init?.body as Uint8Array));
  switch (method) {
    case "GetCoreSettings":
      return Response.json({ settings: { theme: "THEME_DDASH" } });
    case "GetConfigForm":
      return Response.json({
        fields: [
          {
            name: "theme",
            kind: "KIND_ENUM",
            label: "Theme",
            default: "THEME_DDASH",
            placeholder: "THEME_DDASH",
            options: [{ value: "THEME_DDASH", label: "ddash" }],
          },
        ],
        values: {},
      });
    case "SaveConfig":
      saves.push(body);
      return Response.json({});
    default:
      return Response.json({});
  }
};

const settle = () => act(() => new Promise((r) => setTimeout(r, 20)));

async function boot() {
  render(
    <MemoryRouter initialEntries={["/settings"]}>
      <App client={createQueryClient()} fetch={vi.fn(fetchFake)} importBundle={async () => ({})} />
    </MemoryRouter>,
  );
  await settle();
}

const html = document.documentElement;

it("applies the Theme from the core's settings", async () => {
  await boot();

  expect(html.dataset.theme).toBe("ddash");
});

it("offers the core's Themes and saves the one picked as a core setting", async () => {
  await boot();

  const themes = within(screen.getByRole("radiogroup", { name: "Theme" })).getAllByRole("radio");
  expect(themes.map((t) => [t.textContent, t.getAttribute("aria-checked")])).toEqual([["ddashin use", "true"]]);
  fireEvent.click(themes[0]);
  await settle();

  expect(saves).toEqual([{ owner: "core", values: { theme: "THEME_DDASH" } }]);
});

it("is a System Page with an Appearance panel, marking the Theme in use", async () => {
  await boot();

  expect(screen.getByRole("heading", { level: 1, name: "Settings" })).toBeTruthy();
  expect(screen.getByText("System")).toBeTruthy();
  const appearance = screen.getByRole("region", { name: "Appearance" });
  const theme = within(appearance).getByRole("radio", { name: /ddash/ });
  expect(theme.textContent).toMatch(/in use/);
  expect(within(appearance).getByText(/remembered in this browser only/i)).toBeTruthy();
});

it("links to the Audit Log and Plugins under More settings", async () => {
  await boot();

  const more = screen.getByRole("region", { name: "More settings" });
  expect(within(more).getByRole("link", { name: /Audit Log/ }).getAttribute("href")).toBe("/settings/audit-log");
  expect(within(more).getByRole("link", { name: /Plugins/ }).getAttribute("href")).toBe("/plugins");
});

const scheme = () => screen.getByRole("radiogroup", { name: "Scheme" });

it("starts light or dark from the browser's preference", async () => {
  prefersDark = true;
  await boot();

  expect(html.classList.contains("dark")).toBe(true);
  expect(within(scheme()).getByRole("radio", { name: "Dark" }).getAttribute("aria-checked")).toBe("true");
});

it("remembers the scheme picked in this browser, over its preference", async () => {
  await boot();

  fireEvent.click(within(scheme()).getByRole("radio", { name: "Dark" }));
  expect(html.classList.contains("dark")).toBe(true);
  cleanup();
  html.className = "";
  await boot();

  expect(html.classList.contains("dark")).toBe(true);
  expect(saves).toEqual([]);
});
