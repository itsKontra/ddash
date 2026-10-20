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
  expect(themes.map((t) => [t.textContent, t.getAttribute("aria-checked")])).toEqual([["ddash", "true"]]);
  fireEvent.click(themes[0]);
  await settle();

  expect(saves).toEqual([{ owner: "core", values: { theme: "THEME_DDASH" } }]);
});

it("starts light or dark from the browser's preference", async () => {
  prefersDark = true;
  await boot();

  expect(html.classList.contains("dark")).toBe(true);
  expect(screen.getByRole("switch", { name: "Dark" }).getAttribute("aria-checked")).toBe("true");
});

it("remembers the dark switch in this browser, over its preference", async () => {
  await boot();

  fireEvent.click(screen.getByRole("switch", { name: "Dark" }));
  expect(html.classList.contains("dark")).toBe(true);
  cleanup();
  html.className = "";
  await boot();

  expect(html.classList.contains("dark")).toBe(true);
  expect(saves).toEqual([]);
});
