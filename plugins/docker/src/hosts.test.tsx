import { cleanup, screen, within } from "@testing-library/react";
import { showWidget, unavailable, type Answer } from "@ddash/plugin/testing";
import { afterEach, expect, it } from "vitest";
import plugin from "./index";
import { docker } from "./testing";

afterEach(cleanup);

const container = (name: string, state: string, image = "") => ({ id: name, name, state: `STATE_${state.toUpperCase()}`, image });

const listed = {
  hosts: [
    { host: "media", containers: [container("jellyfin", "running"), container("sonarr", "exited"), container("radarr", "running")] },
    { host: "tools", containers: [container("grafana", "running")] },
  ],
};

const minutesAgo = (m: number) => new Date(Date.now() - m * 60_000).toISOString();

// compose's Projects on the Agent host, one container's image stale.
const projects = (stale: boolean, checked: string | null = minutesAgo(70)) => ({
  hosts: [
    {
      name: "media",
      projects: [
        {
          name: "media",
          services: [{ name: "jellyfin", containers: [{ container: container("jellyfin", "running"), imageStatus: { state: stale ? "IMAGE_STATE_STALE" : "IMAGE_STATE_UP_TO_DATE" } }] }],
        },
      ],
    },
  ],
  checks: checked ? { last: checked } : undefined,
});

const hostsWidget = () => plugin.widgets!.find((w) => w.id === "docker-hosts")!;

async function show(answers: Record<string, Answer>) {
  const { reported } = await showWidget(docker, hostsWidget(), { answers });
  return { reported, widget: screen.getByRole("region", { name: "widget" }) };
}

it("is the Docker hosts Widget, S or M, S by default", () => {
  const w = hostsWidget();
  expect([w.title, w.sizes, w.defaultSize]).toEqual(["Docker hosts", ["S", "M"], "S"]);
});

it("heads with the containers running, the exited ones in the down tone and named, then the update state with its age", async () => {
  const { reported, widget } = await show({ ListContainers: () => Response.json(listed), ListProjects: () => Response.json(projects(false)) });

  expect(within(widget).getByText("3 of 4 containers running")).toBeTruthy();
  const exited = within(widget).getByText("1 exited");
  expect(exited.className).toContain("text-down-text");
  expect(exited.parentElement?.textContent).toBe("1 exited · all up to date, checked 1 h ago");
  expect(within(widget).getAllByRole("listitem").map((li) => li.textContent)).toEqual(["sonarrexited on media"]);
  expect(reported.status).toEqual({ tone: "down", summary: "1 exited: sonarr" });
  // No node here: no meters, and no empty frames for them.
  expect(within(widget).queryByRole("meter")).toBeNull();
});

it("is up with the running count when none has exited, and counts the updates waiting", async () => {
  const fine = { hosts: [listed.hosts[1]] };
  const { reported, widget } = await show({ ListContainers: () => Response.json(fine), ListProjects: () => Response.json(projects(true)) });

  expect(reported.status).toEqual({ tone: "up", summary: "1 of 1 container running" });
  expect(within(widget).getByText("1 update waiting, checked 1 h ago")).toBeTruthy();
});

it("says the images aren't checked yet, and leaves the update state out without a Docker Agent", async () => {
  await show({ ListContainers: () => Response.json(listed), ListProjects: () => Response.json(projects(false, null)) });
  expect(screen.getByText("images not checked yet")).toBeTruthy();
  cleanup();

  const { widget } = await show({ ListContainers: () => Response.json(listed), ListProjects: () => Response.json({}) });
  expect(within(widget).getByText("1 exited").parentElement?.textContent).toBe("1 exited");
});

it("is warn while a host can't be read, and while docker-api is unavailable", async () => {
  const { reported } = await show({ ListContainers: () => Response.json({ hosts: [...listed.hosts.slice(1), { host: "nas", error: "connection refused" }] }) });
  expect(reported.status).toEqual({ tone: "warn", summary: "nas unreadable" });
  cleanup();

  const down = await show({ ListContainers: () => unavailable("no socket-proxy") });
  expect(down.reported.status).toEqual({ tone: "warn", summary: "docker-api unavailable: no socket-proxy" });
});
