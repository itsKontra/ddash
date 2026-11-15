import { cleanup, fireEvent, screen, within } from "@testing-library/react";
import { settle, showPlugin, showWidget, streamed, type Answers } from "@ddash/plugin/testing";
import { afterEach, expect, it } from "vitest";
import plugin from "./index";
import { docker } from "./testing";
import { containers as widget } from "./widget";

afterEach(cleanup);

const status = (state: string, extra: object = {}) => ({ imageStatus: { state, ...extra } });

// What compose answers ListProjects with (Connect JSON).
const projects = {
  hosts: [
    {
      name: "apps",
      address: "198.18.2.20",
      roots: ["/opt/stacks"],
      projects: [
        {
          host: "apps",
          name: "media",
          workingDir: "/opt/stacks/media",
          configFile: "/opt/stacks/media/compose.yaml",
          state: "PROJECT_STATE_RUNNING",
          updateAvailable: true,
          services: [
            {
              name: "jellyfin",
              image: "jellyfin/jellyfin:10.10.3",
              containers: [
                {
                  container: { id: "c1", name: "media-jellyfin-1", image: "jellyfin/jellyfin:10.10.3", state: "STATE_RUNNING", status: "Up 3 hours" },
                  ...status("IMAGE_STATE_STALE", { currentDigest: "sha256:aaaaaaaaaaaaaaaa", latestDigest: "sha256:bbbbbbbbbbbbbbbb" }),
                },
              ],
            },
          ],
        },
        {
          host: "apps",
          name: "proxy",
          workingDir: "/opt/stacks/proxy",
          configFile: "/opt/stacks/proxy/compose.yaml",
          state: "PROJECT_STATE_RUNNING",
          protected: true,
          services: [{ name: "npm", containers: [{ container: { id: "c2", name: "proxy-npm-1", state: "STATE_RUNNING", protected: true }, ...status("IMAGE_STATE_UP_TO_DATE") }] }],
        },
        { host: "apps", name: "uptime", workingDir: "/opt/stacks/uptime", configFile: "/opt/stacks/uptime/compose.yaml", state: "PROJECT_STATE_NOT_STARTED", services: [{ name: "kuma" }] },
      ],
      containers: [{ container: { id: "c9", name: "portainer_agent", image: "portainer/agent:2.21.4", state: "STATE_RUNNING" }, ...status("IMAGE_STATE_UP_TO_DATE") }],
    },
  ],
  checks: { last: "2026-11-14T08:00:00Z", next: "2026-11-14T14:00:00Z" },
};

const proxyHosts = { hosts: [{ host: "media", containers: [{ id: "a1", name: "plex", image: "plex", state: "STATE_RUNNING", status: "Up" }] }] };

const base: Answers = {
  ListProjects: () => Response.json(projects),
  ListContainers: () => Response.json(proxyHosts),
  ListAgents: () => Response.json({ agents: [{ name: "apps", state: "AGENT_STATE_PAIRED" }] }),
  ListRuns: () => Response.json({ runs: [] }),
};

it("lists each Agent host's Compose Projects with their state and image updates, and socket-proxy hosts read-only", async () => {
  await showPlugin(plugin, docker, "/docker", { answers: base });

  const apps = screen.getByRole("region", { name: "apps" });
  expect(within(apps).getByText("Docker Agent at 198.18.2.20")).toBeTruthy();
  const media = within(apps).getByRole("row", { name: /media/ });
  expect(within(media).getByText("running")).toBeTruthy();
  expect(within(media).getByText("1 update")).toBeTruthy();
  const proxy = within(apps).getByRole("row", { name: /proxy/ });
  expect(within(proxy).getByText("Protected")).toBeTruthy();
  expect(within(within(apps).getByRole("row", { name: /uptime/ })).getByText("not started")).toBeTruthy();
  // A container no Compose Project started stays listed.
  expect(within(apps).getByText("Not started by Compose")).toBeTruthy();
  expect(within(apps).getByText("portainer_agent")).toBeTruthy();
  // The socket-proxy host, read-only.
  const plex = screen.getByRole("region", { name: "media" });
  expect(within(plex).getByText(/Read-only, through a socket-proxy/)).toBeTruthy();
  expect(screen.getByText(/Images checked/)).toBeTruthy();
});

const plan = {
  steps: [
    { system: "SYSTEM_DDASH", title: "Maintenance for media-jellyfin-1", values: [{ label: "Until", after: "its Checks are up, or 5 min" }] },
    { system: "SYSTEM_DOCKER_AGENT", title: "Pull images", values: [{ label: "jellyfin (jellyfin/jellyfin:10.10.3)", before: "aaaaaaaaaaaa", after: "bbbbbbbbbbbb" }] },
    { system: "SYSTEM_DOCKER_AGENT", title: "Compare digests" },
    { system: "SYSTEM_DOCKER_AGENT", title: "Recreate the Compose services whose image changed" },
  ],
};

it("updates a Compose Project through its Plan, listing each image's current and new digest, and streams its steps", async () => {
  const calls = await showPlugin(plugin, docker, "/docker", {
    answers: {
      ...base,
      PlanAction: () => Response.json({ plan }),
      RunAction: () =>
        streamed([
          { run: { id: "r1", steps: plan.steps, states: ["STEP_STATE_DONE", "STEP_STATE_RUNNING", "STEP_STATE_PENDING", "STEP_STATE_PENDING"], details: ["", "", "", ""] } },
          {
            run: {
              id: "r1",
              steps: plan.steps,
              states: ["STEP_STATE_DONE", "STEP_STATE_DONE", "STEP_STATE_DONE", "STEP_STATE_DONE"],
              details: ["", "", "1 changed: jellyfin", "jellyfin"],
              ended: "2026-11-14T10:00:00Z",
              outcome: "1 Compose service updated: jellyfin",
            },
          },
        ]),
    },
  });
  fireEvent.click(screen.getByRole("button", { name: "Actions for media" }));
  fireEvent.click(within(screen.getByRole("group", { name: "Actions for media" })).getByRole("button", { name: "Update" }));
  await settle();
  const dialog = screen.getByRole("dialog", { name: "Update media" });
  expect(within(dialog).getByText("aaaaaaaaaaaa →")).toBeTruthy();
  expect(within(dialog).getByText("bbbbbbbbbbbb")).toBeTruthy();
  expect(calls.find((c) => c.method === "PlanAction")?.body).toEqual({ action: { update: { host: "apps", project: "media" } } });
  fireEvent.click(within(dialog).getByRole("button", { name: "Update" }));
  await settle();
  expect(within(dialog).getByText("Done: 1 Compose service updated: jellyfin.")).toBeTruthy();
  expect(calls.find((c) => c.method === "RunAction")?.body).toMatchObject({ action: { update: { host: "apps", project: "media" } }, plan });
});

it("doesn't ask for the runs while a Compose Plan Dialog has no run to follow", async () => {
  const calls = await showPlugin(plugin, docker, "/docker", { answers: { ...base, PlanAction: () => Response.json({ plan }) } });
  fireEvent.click(screen.getByRole("button", { name: "Actions for media" }));
  fireEvent.click(within(screen.getByRole("group", { name: "Actions for media" })).getByRole("button", { name: "Restart" }));
  await settle();

  expect(screen.getByRole("dialog", { name: "Restart media" })).toBeTruthy();
  expect(calls.filter((c) => c.method === "ListRuns")).toEqual([]);
});

it("asks for the Protected override before stopping a Protected Compose Project", async () => {
  // The Plan says the override is needed in a field of its own; its wording is the Operator's to read, not the dialog's to parse.
  const blocked = { ...plan, protected: ["proxy-npm-1"], overrideNeeded: "ddash may stop proxy-npm-1 only with the override." };
  const calls = await showPlugin(plugin, docker, "/docker", { answers: { ...base, PlanAction: () => Response.json({ plan: blocked }) } });
  fireEvent.click(screen.getByRole("button", { name: "Actions for proxy" }));
  fireEvent.click(within(screen.getByRole("group", { name: "Actions for proxy" })).getByRole("button", { name: "Stop" }));
  await settle();
  const dialog = screen.getByRole("dialog", { name: "Stop proxy" });
  const confirm = within(dialog).getByRole("button", { name: "Stop" }) as HTMLButtonElement;
  expect(confirm.disabled).toBe(true);
  fireEvent.click(within(dialog).getByRole("checkbox", { name: "Override: do it anyway" }));
  await settle();
  expect(calls.filter((c) => c.method === "PlanAction").at(-1)?.body).toMatchObject({ action: { overrideProtected: true } });
  expect((within(dialog).getByRole("button", { name: "Stop" }) as HTMLButtonElement).disabled).toBe(false);
});

it("edits a Compose file and shows the change as a diff in the save's Plan", async () => {
  const content = "services:\n  jellyfin:\n    image: jellyfin/jellyfin:10.10.3\n";
  const calls = await showPlugin(plugin, docker, "/docker/projects/apps/media", {
    answers: {
      ...base,
      GetComposeFile: () => Response.json({ file: { path: "/opt/stacks/media/compose.yaml", content, sha256: "abc" }, env: { path: "/opt/stacks/media/.env", content: "TZ=x\n", sha256: "def" } }),
      PlanAction: () =>
        Response.json({
          plan: { diff: "--- f\n+++ f\n@@ -3,1 +3,1 @@\n-    image: jellyfin/jellyfin:10.10.3\n+    image: jellyfin/jellyfin:10.11.0\n", steps: [{ system: "SYSTEM_DOCKER_AGENT", title: "Write /opt/stacks/media/compose.yaml" }] },
        }),
    },
  });
  expect(screen.getByRole("heading", { level: 1, name: "media" })).toBeTruthy();
  expect(screen.getByRole("tab", { name: ".env" })).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: "Edit" }));
  const editor = screen.getByRole("textbox", { name: "Edit /opt/stacks/media/compose.yaml" });
  fireEvent.change(editor, { target: { value: content.replace("10.10.3", "10.11.0") } });
  fireEvent.click(screen.getByRole("button", { name: "Save…" }));
  await settle();
  const dialog = screen.getByRole("dialog", { name: "Save compose.yaml" });
  expect(within(dialog).getByRole("region", { name: "The change" }).textContent).toContain("+    image: jellyfin/jellyfin:10.11.0");
  expect(calls.find((c) => c.method === "PlanAction")?.body).toMatchObject({ action: { save: { path: "/opt/stacks/media/compose.yaml", expectSha256: "abc" } } });
});

it("opens a draft from the URL fragment in Add Compose Project", async () => {
  const json = JSON.stringify({ host: "apps", name: "paperless", compose: "services:\n  web:\n    image: paperlessngx/paperless-ngx:2.13.5\n" });
  const draft = btoa(json).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
  await showPlugin(plugin, docker, `/docker/projects/new#draft=${draft}`, { answers: base });
  expect((screen.getByRole("textbox", { name: "Name" }) as HTMLInputElement).value).toBe("paperless");
  expect((screen.getByRole("textbox", { name: "Directory" }) as HTMLInputElement).value).toBe("/opt/stacks/paperless");
  expect(screen.getByText(/This is a draft/)).toBeTruthy();
});

it("opens Add Compose Project empty when the fragment's draft has fields of the wrong type", async () => {
  const draft = btoa(JSON.stringify({ host: "apps", name: 123, compose: "services: {}\n" })).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
  await showPlugin(plugin, docker, `/docker/projects/new#draft=${draft}`, { answers: base });
  expect((screen.getByRole("textbox", { name: "Name" }) as HTMLInputElement).value).toBe("");
  expect(screen.queryByText(/This is a draft/)).toBeNull();
});

it("counts the updates waiting in the Widget", async () => {
  const { reported } = await showWidget(docker, widget, { answers: base });
  expect(reported.status?.summary).toBe("1 of 1 running · 1 update waiting");
  expect(screen.getByRole("group", { name: "update waiting" })).toBeTruthy();
});

it("restarts one Compose service from its row on the Compose Project's Page", async () => {
  const calls = await showPlugin(plugin, docker, "/docker/projects/apps/media", {
    answers: { ...base, GetComposeFile: () => Response.json({ file: { path: "/opt/stacks/media/compose.yaml", content: "services: {}\n", sha256: "a" } }), PlanAction: () => Response.json({ plan }) },
  });
  fireEvent.click(screen.getByRole("button", { name: "Actions for jellyfin" }));
  fireEvent.click(within(screen.getByRole("group", { name: "Actions for jellyfin" })).getByRole("button", { name: "Restart" }));
  await settle();
  expect(screen.getByRole("dialog", { name: "Restart media (jellyfin)" })).toBeTruthy();
  expect(calls.find((c) => c.method === "PlanAction")?.body).toEqual({ action: { restart: { host: "apps", project: "media", services: ["jellyfin"] } } });
});

it("says why Check now failed", async () => {
  await showPlugin(plugin, docker, "/docker", {
    answers: { ...base, CheckImages: () => Response.json({ code: "unavailable", message: "docker.io refused ddash" }, { status: 503 }) },
  });

  fireEvent.click(screen.getByRole("button", { name: "Check now" }));
  await settle();

  expect(screen.getByText("docker.io refused ddash")).toBeTruthy();
});
