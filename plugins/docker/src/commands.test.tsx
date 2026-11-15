import { cleanup, fireEvent, screen, within } from "@testing-library/react";
import { pending, settle, showCommands, streamed, type Answers } from "@ddash/plugin/testing";
import { afterEach, expect, it } from "vitest";
import plugin from "./index";
import { docker } from "./testing";

afterEach(cleanup);

const project = (name: string, state: string, extra: object = {}) => ({
  host: "apps",
  name,
  workingDir: `/opt/stacks/${name}`,
  state,
  services: [{ name, containers: [{ container: { id: name, name: `${name}-1`, state: "STATE_RUNNING" } }] }],
  ...extra,
});

// compose's ListProjects (Connect JSON): one of each state, a Protected one and one running an Action.
const projects = {
  hosts: [
    {
      name: "apps",
      projects: [
        project("paperless", "PROJECT_STATE_RUNNING", { updateAvailable: true }),
        project("immich", "PROJECT_STATE_PARTIAL"),
        project("uptime", "PROJECT_STATE_NOT_STARTED"),
        project("backup", "PROJECT_STATE_STOPPED"),
        project("proxy", "PROJECT_STATE_RUNNING", { protected: true }),
        project("dns", "PROJECT_STATE_STOPPED", { protected: true }),
        project("media", "PROJECT_STATE_RUNNING", { runId: "r9" }),
      ],
    },
  ],
};

const plan = { steps: [{ system: "SYSTEM_DOCKER_AGENT", title: "Restart the Compose services" }] };
const base: Answers = { ListProjects: () => Response.json(projects), PlanAction: () => Response.json({ plan }), ListRuns: () => Response.json({ runs: [] }) };

it("offers Start, Restart and Update of each Compose Project as its state allows, none while it runs an Action", async () => {
  const shown = await showCommands(plugin, docker, { answers: base });

  expect(shown.commands()?.map((c) => [c.title, c.hint])).toEqual([
    ["Restart paperless", "Compose Project on apps"],
    ["Update paperless", "Compose Project on apps"],
    ["Start immich", "Compose Project on apps"],
    ["Restart immich", "Compose Project on apps"],
    ["Update immich", "Compose Project on apps"],
    ["Start uptime", "Compose Project on apps"],
    ["Start backup", "Compose Project on apps"],
    ["Update backup", "Compose Project on apps"],
    ["Start dns", "Compose Project on apps"],
  ]);
  // Found by its host and by the Page's "Pull & update" too.
  expect(shown.commands()?.find((c) => c.title === "Update backup")?.keywords).toEqual(expect.arrayContaining(["apps", "pull"]));
});

it("offers a Protected Compose Project no Stop, Restart or Update, only Start", async () => {
  const shown = await showCommands(plugin, docker, { answers: base });

  const titles = shown.commands()?.map((c) => c.title) ?? [];
  expect(titles.filter((t) => / (proxy|dns)$/.test(t))).toEqual(["Start dns"]);
  expect(titles.some((t) => t.startsWith("Stop "))).toBe(false);
});

it("opens the Page's own Plan on any Page, and runs it as the Page does", async () => {
  const shown = await showCommands(plugin, docker, {
    answers: {
      ...base,
      RunAction: () => streamed([{ run: { id: "r1", steps: plan.steps, states: ["STEP_STATE_DONE"], details: [""], ended: "2026-11-15T10:00:00Z", outcome: "done" } }]),
    },
  });

  await shown.choose("Restart paperless");
  const dialog = screen.getByRole("dialog", { name: "Restart paperless" });
  expect(shown.calls.find((c) => c.method === "PlanAction")?.body).toEqual({ action: { restart: { host: "apps", project: "paperless" } } });
  // Nothing runs before the Plan is confirmed.
  expect(shown.calls.filter((c) => c.method === "RunAction")).toEqual([]);
  fireEvent.click(within(dialog).getByRole("button", { name: "Restart" }));
  await settle();
  expect(shown.calls.find((c) => c.method === "RunAction")?.body).toMatchObject({ action: { restart: { host: "apps", project: "paperless" } }, plan });
  expect(within(dialog).getByText("Done.")).toBeTruthy();
});

it("offers nothing until compose listed the Compose Projects", async () => {
  const shown = await showCommands(plugin, docker, { answers: { ListProjects: () => pending() } });
  expect(shown.commands()).toBeUndefined();
});
