import { cleanup, fireEvent, screen, within } from "@testing-library/react";
import { settle, showPlugin, type Answers } from "@ddash/plugin/testing";
import { afterEach, expect, it } from "vitest";
import plugin from "./index";
import { docker } from "./testing";

afterEach(cleanup);

const container = (id: string, name: string, image: string) => ({ container: { id, name, image, state: "STATE_RUNNING", status: "Up 3 days" }, imageStatus: { state: "IMAGE_STATE_NOT_CHECKED" } });

// compose's ListProjects (Connect JSON): the Docker Agent's own Compose Project and another outside the Compose roots.
const projects = {
  hosts: [
    {
      name: "apps",
      roots: ["/opt/ddash"],
      projects: [
        {
          host: "apps",
          name: "ddash-docker-agent",
          workingDir: "/opt/ddash-docker-agent",
          configFile: "/opt/ddash-docker-agent/compose.yaml",
          state: "PROJECT_STATE_RUNNING",
          protected: true,
          dockerAgent: true,
          services: [{ name: "agent", containers: [container("a1", "ddash-docker-agent-agent-1", "ghcr.io/itskontra/ddash-docker-agent:v0.0.4")] }],
        },
        {
          host: "apps",
          name: "legacy",
          workingDir: "/srv/legacy",
          configFile: "/srv/legacy/compose.yaml",
          state: "PROJECT_STATE_RUNNING",
          error: "/srv/legacy/compose.yaml isn't under a Compose root the Docker Agent mounts",
          services: [{ name: "web", containers: [container("l1", "legacy-web-1", "nginx:1.27")] }],
        },
      ],
    },
  ],
};

const agent = (extra: object = {}) => ({ agents: [{ name: "apps", state: "AGENT_STATE_PAIRED", release: "v0.0.4", ...extra }] });

const answers = (extra: Answers = {}): Answers => ({
  ListProjects: () => Response.json(projects),
  ListAgents: () => Response.json(agent()),
  ListRuns: () => Response.json({ runs: [] }),
  ...extra,
});

it("shows the Docker Agent's own Compose Project as the Agent: its Release and how ddash updates it, no Actions, no Compose file", async () => {
  const calls = await showPlugin(plugin, docker, "/docker/projects/apps/ddash-docker-agent", { answers: answers() });

  const notice = screen.getByText("This is the Docker Agent on apps").closest("[role=status]")!;
  expect(notice.textContent).toContain("It runs Release v0.0.4.");
  expect(notice.textContent).toContain("ddash updates it with Update Agent");
  expect(screen.getByRole("link", { name: "Docker Agents" }).getAttribute("href")).toBe("/docker/agents");
  for (const name of ["Restart", "Stop", "Update", "Pull & update", "Update Agent", "Edit", "Actions for agent"]) {
    expect(screen.queryByRole("button", { name })).toBeNull();
  }
  expect(screen.queryByRole("region", { name: "Compose file" })).toBeNull();
  expect(screen.queryByText(/isn't under a Compose root/)).toBeNull();
  expect(calls.some((c) => c.method === "GetComposeFile")).toBe(false);
  // It still shows what runs.
  expect(screen.getByText("ddash-docker-agent-agent-1")).toBeTruthy();
});

it("offers Update Agent on the Docker Agent's own Compose Project while it runs another Release, through the same confirm as the Docker Agents list", async () => {
  const calls = await showPlugin(plugin, docker, "/docker/projects/apps/ddash-docker-agent", {
    answers: answers({ ListAgents: () => Response.json(agent({ state: "AGENT_STATE_OUTDATED", release: "v0.0.3" })), UpdateAgent: () => Response.json({}) }),
  });

  expect(screen.getByText(/It runs Release v0.0.3, not ddash's/)).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: "Update Agent" }));
  await settle();
  const confirm = screen.getByRole("alertdialog");
  expect(confirm.textContent).toContain("Update the Docker Agent on apps? It recreates itself on this Release's image");
  fireEvent.click(within(confirm).getByRole("button", { name: "Update Agent" }));
  await settle();
  expect(calls.filter((c) => c.method === "UpdateAgent").map((c) => c.body)).toEqual([{ name: "apps" }]);
});

it("offers Update Agent on a Compose Project's Page while its host can't be read because its Agent runs another Release", async () => {
  const message = "it runs Release v0.0.3 and ddash runs v0.0.4: update the Agent";
  await showPlugin(plugin, docker, "/docker/projects/apps/ddash-docker-agent", {
    answers: answers({
      ListProjects: () => Response.json({ hosts: [{ name: "apps", error: message }] }),
      ListAgents: () => Response.json(agent({ state: "AGENT_STATE_OUTDATED", release: "v0.0.3", message })),
    }),
  });

  expect(screen.getByText(message)).toBeTruthy();
  expect(screen.getByRole("button", { name: "Update Agent" })).toBeTruthy();
});

it("still warns of another Compose Project outside the Compose roots, and offers its Actions", async () => {
  await showPlugin(plugin, docker, "/docker/projects/apps/legacy", { answers: answers({ GetComposeFile: () => Response.json({}) }) });

  expect(screen.getByText("/srv/legacy/compose.yaml isn't under a Compose root the Docker Agent mounts")).toBeTruthy();
  expect(screen.getByRole("button", { name: "Restart" })).toBeTruthy();
  expect(screen.queryByText(/This is the Docker Agent/)).toBeNull();
});

it("lists the Docker Agent's own Compose Project as the Agent, without Actions", async () => {
  await showPlugin(plugin, docker, "/docker", { answers: answers({ ListContainers: () => Response.json({ hosts: [] }) }) });

  const row = screen.getByRole("row", { name: /ddash-docker-agent/ });
  expect(within(row).getByText("Docker Agent")).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: "Actions for ddash-docker-agent" }));
  const strip = screen.getByRole("group", { name: "Actions for ddash-docker-agent" });
  expect(within(strip).queryAllByRole("button")).toEqual([]);
  expect(strip.textContent).toContain("Update Agent updates it");
});
