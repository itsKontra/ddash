import { cleanup } from "@testing-library/react";
import type { Command } from "@ddash/plugin";
import { settle } from "@ddash/plugin/testing";
import { afterEach, expect, it, vi } from "vitest";
import plugin from "./index";
import { allUp, renderScoped } from "./testing";

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

let seen: Command[] | undefined;
function Probe() {
  seen = plugin.useCommands!();
  return null;
}

it("offers Open for each App the Widget shows, none Hidden or orphaned, with its host and icon", async () => {
  renderScoped(<Probe />, { ListChecks: () => Response.json(allUp) });
  await settle();

  expect(seen?.map((c) => [c.title, c.hint])).toEqual([
    ["Open Grafana", "grafana.home.example"],
    ["Open Jellyfin", "jellyfin.home.example"],
    ["Open pfSense", "198.18.0.1"],
    ["Open Printer", "printer.home.example"],
  ]);
  expect(seen?.[0]?.icon).toEqual({ app: "Grafana", icon: "", url: "https://grafana.home.example" });
  // Found by its group and aliases too.
  expect(seen?.[1]?.keywords).toEqual(expect.arrayContaining(["media.home.example", "tv.home.example"]));
});

it("opens the App's URL in a new tab, as the Widget's icon does", async () => {
  const open = vi.spyOn(window, "open").mockReturnValue(null);
  renderScoped(<Probe />, { ListChecks: () => Response.json(allUp) });
  await settle();

  seen!.find((c) => c.title === "Open Grafana")!.run();
  expect(open).toHaveBeenCalledWith("https://grafana.home.example", "_blank", "noopener,noreferrer");
});

it("offers nothing until ListApps answered", () => {
  renderScoped(<Probe />, { ListApps: () => new Promise(() => {}), ListChecks: () => Response.json(allUp) });

  expect(seen).toBeUndefined();
});
