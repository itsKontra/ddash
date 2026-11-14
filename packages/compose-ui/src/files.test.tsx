import { cleanup, fireEvent, screen, within } from "@testing-library/react";
import { afterEach, expect, it } from "vitest";
import { renderScoped, settle } from "@ddash/plugin/testing";
import { ComposeFiles } from "./files";

afterEach(cleanup);

const scope = { name: "docker", requires: ["docker-agent-api", "compose"] };
const project = { host: "media", name: "media", configFile: "/srv/compose/media/compose.yaml" };
const compose = "services:\n  jellyfin:\n    image: jellyfin/jellyfin:10.9\n";

const show = async (answers = {}) => {
  const calls = renderScoped(scope, <ComposeFiles project={project} />, {
    answers: {
      GetComposeFile: () => Response.json({ file: { path: project.configFile, content: compose, sha256: "c1" }, env: { path: "/srv/compose/media/.env", content: "TZ=UTC\n", sha256: "e1" } }),
      ...answers,
    },
  });
  await settle();
  return calls;
};

it("saves an edited .env expecting the content it was read with, the Compose file untouched", async () => {
  const calls = await show({ PlanAction: () => Response.json({ plan: { steps: [{ system: "SYSTEM_DOCKER_AGENT", title: "Write /srv/compose/media/.env" }] } }) });
  expect(screen.queryByRole("button", { name: "Add .env" })).toBeNull();

  fireEvent.click(screen.getByRole("tab", { name: ".env" }));
  fireEvent.click(screen.getByRole("button", { name: "Edit" }));
  const box = screen.getByLabelText("Edit /srv/compose/media/.env");
  expect((box as HTMLTextAreaElement).value).toBe("TZ=UTC\n");
  fireEvent.change(box, { target: { value: "TZ=Europe/Berlin\n" } });
  fireEvent.click(screen.getByRole("button", { name: "Save…" }));
  await settle();

  expect(within(screen.getByRole("dialog")).getByRole("heading", { name: "Save .env" })).toBeTruthy();
  expect(calls.filter((c) => c.method === "PlanAction").at(-1)?.body).toEqual({
    action: { save: { host: "media", project: "media", path: "/srv/compose/media/.env", content: "TZ=Europe/Berlin\n", expectSha256: "e1" } },
  });
});

it("says so when the Docker Agent doesn't mount the Compose file", async () => {
  renderScoped(scope, <ComposeFiles project={{ ...project, configFile: "" }} />);
  await settle();
  expect(screen.getByText(/file isn't known/)).toBeTruthy();
});

it("shows the Dockerfile a Compose service builds from, and saves it rebuilding that Compose service", async () => {
  const dockerfile = "FROM node:22-alpine\nCMD [\"node\", \"server.js\"]\n";
  const calls = await show({
    GetComposeFile: () =>
      Response.json({
        file: { path: project.configFile, content: "services:\n  app:\n    build: ./app\n", sha256: "c1" },
        dockerfiles: [{ file: { path: "/srv/compose/media/app/Dockerfile", content: dockerfile, sha256: "d1" }, services: ["app"] }],
      }),
    PlanAction: () => Response.json({ plan: { steps: [{ system: "SYSTEM_DOCKER_AGENT", title: "Rebuild and recreate app" }] } }),
  });

  fireEvent.click(screen.getByRole("tab", { name: "app/Dockerfile" }));
  expect(screen.getByText(/builds app/)).toBeTruthy();
  expect(document.body.textContent).toContain('CMD ["node", "server.js"]');
  fireEvent.click(screen.getByRole("button", { name: "Edit" }));
  fireEvent.change(screen.getByLabelText("Edit /srv/compose/media/app/Dockerfile"), { target: { value: "FROM node:24-alpine\n" } });
  fireEvent.click(screen.getByRole("button", { name: "Save…" }));
  await settle();

  const dialog = within(screen.getByRole("dialog"));
  expect(dialog.getByRole("heading", { name: "Save Dockerfile" })).toBeTruthy();
  fireEvent.click(dialog.getByLabelText(/Rebuild and recreate app now \(docker compose up -d --build app\)/));
  await settle();
  expect(dialog.getByRole("button", { name: "Save and rebuild" })).toBeTruthy();
  expect(calls.filter((c) => c.method === "PlanAction").at(-1)?.body).toEqual({
    action: { save: { host: "media", project: "media", path: "/srv/compose/media/app/Dockerfile", content: "FROM node:24-alpine\n", expectSha256: "d1", recreate: true } },
  });
});
