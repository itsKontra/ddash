import { act, cleanup, render, screen, within } from "@testing-library/react";
import { createQueryClient, PluginHost, PluginScope } from "@ddash/plugin";
import { afterEach, expect, it, vi } from "vitest";
import plugin from "./index";

afterEach(cleanup);

// What docker-api answers ListContainers with (Connect JSON).
const listed = {
  hosts: [
    {
      host: "media",
      containers: [
        { id: "a1", name: "jellyfin", image: "jellyfin/jellyfin:10.10", state: "running", status: "Up 3 hours (healthy)" },
        { id: "b2", name: "sonarr", image: "linuxserver/sonarr", state: "exited", status: "Exited (0) 2 days ago" },
      ],
    },
    { host: "tools", error: "dial tcp 198.18.2.11:2375: connect: connection refused" },
  ],
};

it("is the docker Nav Entry at order 50 (its icon, container, is in plugin.yaml)", () => {
  expect(plugin.nav?.order).toBe(50);
});

it("lists containers per host with their state, and a host it can't read with the error", async () => {
  const requests: Request[] = [];
  const fetch = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    requests.push(new Request(new URL(String(input), "http://ddash.test"), init));
    return Response.json(listed);
  });
  const Page = plugin.pages![0].Component!;

  render(
    <PluginHost client={createQueryClient()}>
      <PluginScope plugin={{ name: "docker", requires: ["docker-api"] }} fetch={fetch}>
        <Page />
      </PluginScope>
    </PluginHost>,
  );
  await act(() => new Promise((r) => setTimeout(r, 50)));

  expect(new URL(requests[0].url).pathname).toBe("/api/c/docker-api/ddash.dockerapi.DockerApiService/ListContainers");
  const media = screen.getByRole("region", { name: "media" });
  const jellyfin = within(media).getByRole("row", { name: /jellyfin/ });
  expect(within(jellyfin).getByRole("img", { name: "running" })).toBeTruthy();
  expect(within(jellyfin).getByText("Up 3 hours (healthy)")).toBeTruthy();
  const sonarr = within(media).getByRole("row", { name: /sonarr/ });
  expect(within(sonarr).getByRole("img", { name: "exited" })).toBeTruthy();
  const tools = screen.getByRole("region", { name: "tools" });
  expect(within(tools).getByText(/connection refused/)).toBeTruthy();
});
