import { cleanup, fireEvent, screen, within } from "@testing-library/react";
import { afterEach, expect, it } from "vitest";
import { settle } from "@ddash/plugin/testing";
import plugin from "./index";
import { renderScoped, split } from "./testing";

afterEach(cleanup);

const Page = () => {
  const P = plugin.pages![0].Component!;
  return <P />;
};

const file = "/srv/compose/media/compose.yaml";
const compose = 'services:\n  jellyfin:\n    image: jellyfin/jellyfin:10.9\n    env_file: .env\n    ports: ["8096:8096"]\n';

/** ListProjects: Jellyfin's container (its Workload's name) in the Compose Project media. */
const projects = {
  hosts: [
    {
      name: "media",
      projects: [{ host: "media", name: "media", workingDir: "/srv/compose/media", configFile: file, services: [{ name: "jellyfin", containers: [{ container: { name: "jellyfin" } }] }] }],
    },
  ],
};

const open = async (id: string, answers = {}, installed = ["apps"]) => {
  const calls = renderScoped(<Page />, { ListApps: () => Response.json(split), ListProjects: () => Response.json(projects), ...answers }, installed, `/?app=${encodeURIComponent(id)}`);
  await settle();
  return calls;
};

const panel = () => within(screen.getByRole("article")).getByRole("region", { name: "Compose Project" });

it("shows the Compose file and .env of the Compose Project behind an App's container, linked to the Docker Page", async () => {
  await open(
    "proxy:jellyfin.home.example",
    { GetComposeFile: () => Response.json({ file: { path: file, content: compose, sha256: "c1" }, env: { path: "/srv/compose/media/.env", content: "TZ=Europe/Berlin\n", sha256: "e1" } }) },
    ["apps", "docker"],
  );
  const p = within(panel());
  expect(panel().textContent).toContain("media on media");
  expect(p.getByRole("link", { name: "Open on the Docker Page" }).getAttribute("href")).toBe("/docker/projects/media/media");
  expect(panel().textContent).toContain("image: jellyfin/jellyfin:10.9");

  fireEvent.click(p.getByRole("tab", { name: ".env" }));
  expect(panel().textContent).toContain("TZ=Europe/Berlin");
  expect(p.queryByRole("button", { name: "Add .env" })).toBeNull();
});

it("adds a .env the Compose Project doesn't have yet, through the compose Component's Plan", async () => {
  const calls = await open("proxy:jellyfin.home.example", {
    GetComposeFile: () => Response.json({ file: { path: file, content: compose, sha256: "c1" } }),
    PlanAction: () => Response.json({ plan: { steps: [{ system: "SYSTEM_DOCKER_AGENT", title: "Create /srv/compose/media/.env" }], diff: "+TZ=Europe/Berlin\n" } }),
  });
  const p = within(panel());
  expect(p.queryByRole("tab", { name: ".env" })).toBeNull();

  fireEvent.click(p.getByRole("button", { name: "Add .env" }));
  expect(panel().textContent).toContain("/srv/compose/media/.env (not there yet)");
  fireEvent.change(p.getByLabelText("Edit /srv/compose/media/.env"), { target: { value: "TZ=Europe/Berlin\n" } });
  fireEvent.click(p.getByRole("button", { name: "Save…" }));
  await settle();

  const dialog = within(screen.getByRole("dialog"));
  expect(dialog.getByRole("heading", { name: "Add .env" })).toBeTruthy();
  expect(screen.getByRole("dialog").textContent).toContain("Create /srv/compose/media/.env");
  const plan = calls.filter((c) => c.method === "PlanAction").at(-1);
  expect(plan?.body).toEqual({ action: { save: { host: "media", project: "media", path: "/srv/compose/media/.env", content: "TZ=Europe/Berlin\n" } } });
});

it("shows no Compose Project for an App whose Workload isn't a Compose Project's container", async () => {
  await open("proxy:grafana.home.example");
  expect(within(screen.getByRole("article")).queryByRole("region", { name: "Compose Project" })).toBeNull();
});
