import { cleanup, screen, within } from "@testing-library/react";
import { pending, renderScoped, settle, unavailable, type Answer } from "@ddash/plugin/testing";
import { afterEach, expect, it } from "vitest";
import plugin from "./index";
import { docker } from "./testing";

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

// Renders the docker Page against docker-api answering ListContainers
// with answer, and returns the calls it made.
async function show(answer: Answer) {
  const Page = plugin.pages![0].Component!;
  const calls = renderScoped(docker, <Page />, { answers: { ListContainers: answer } });
  await settle();
  return calls;
}

it("lists containers per host in a table with a header row, each with its state", async () => {
  const calls = await show(() => Response.json(listed));

  expect(calls[0].url).toBe("/api/c/docker-api/ddash.dockerapi.DockerApiService/ListContainers");
  expect(screen.getByRole("heading", { level: 1, name: "Docker" })).toBeTruthy();
  // The kicker sits right above the title.
  expect(screen.getByRole("heading", { level: 1, name: "Docker" }).previousElementSibling?.textContent).toBe("Infrastructure");
  const media = screen.getByRole("region", { name: "media" });
  expect(within(media).getByText("1/2 running")).toBeTruthy();
  const headers = within(media)
    .getAllByRole("columnheader")
    .map((h) => h.textContent);
  expect(headers).toEqual(["Container", "State", "Image", "Status"]);
  const jellyfin = within(media).getByRole("row", { name: /jellyfin/ });
  expect(within(jellyfin).getByRole("rowheader").textContent).toBe("jellyfin");
  expect(within(jellyfin).getByRole("img", { name: "running" })).toBeTruthy();
  expect(within(jellyfin).getByText("Up 3 hours (healthy)")).toBeTruthy();
  const sonarr = within(media).getByRole("row", { name: /sonarr/ });
  expect(within(sonarr).getByRole("img", { name: "exited" })).toBeTruthy();
});

it("shows a host it can't read as a failure Notice in that host's Panel", async () => {
  await show(() => Response.json(listed));

  const tools = screen.getByRole("region", { name: "tools" });
  expect(within(tools).getByRole("alert").textContent).toMatch(/connection refused/);
  expect(within(tools).queryByRole("table")).toBeNull();
});

it("shows a failure Notice when docker-api can't be reached", async () => {
  await show(() => unavailable("docker-api is down"));

  expect(screen.getByRole("alert").textContent).toMatch(/docker-api is down/);
});

it("shows a skeleton, not loading text, until docker-api first answers", async () => {
  await show(pending);

  expect(screen.getByRole("status", { name: "Loading" })).toBeTruthy();
  expect(screen.queryByText(/loading/i)).toBeNull();
});

it("says a host with no containers has none", async () => {
  await show(() => Response.json({ hosts: [{ host: "spare", containers: [] }] }));

  const spare = screen.getByRole("region", { name: "spare" });
  expect(within(spare).getByText("No containers on this host")).toBeTruthy();
});
