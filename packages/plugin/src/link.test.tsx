import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, expect, it } from "vitest";
import { InstalledPlugins, usePluginLink } from "./link";

afterEach(cleanup);

function Probe({ plugin, path }: { plugin: string; path?: string }) {
  const href = usePluginLink(plugin, path);
  return <p>{href ?? "none"}</p>;
}

it("links to an installed Plugin's Page", () => {
  render(
    <InstalledPlugins names={["proxmox", "docker"]}>
      <Probe plugin="proxmox" />
      <Probe plugin="docker" path="hosts/media" />
      <Probe plugin="docker" path="/hosts/build" />
    </InstalledPlugins>,
  );

  expect(screen.getAllByRole("paragraph").map((p) => p.textContent)).toEqual(["/proxmox", "/docker/hosts/media", "/docker/hosts/build"]);
});

it("is null for a Plugin that isn't installed, or before the shell knows", () => {
  render(
    <>
      <InstalledPlugins names={["apps"]}>
        <Probe plugin="proxmox" />
      </InstalledPlugins>
      <InstalledPlugins names={undefined}>
        <Probe plugin="proxmox" />
      </InstalledPlugins>
      <Probe plugin="proxmox" />
    </>,
  );

  expect(screen.getAllByRole("paragraph").map((p) => p.textContent)).toEqual(["none", "none", "none"]);
});
