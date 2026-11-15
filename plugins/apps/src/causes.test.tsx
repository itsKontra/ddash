import { cleanup, screen, within } from "@testing-library/react";
import { settle } from "@ddash/plugin/testing";
import { afterEach, expect, it } from "vitest";
import plugin from "./index";
import { renderScoped } from "./testing";

afterEach(cleanup);

const Page = plugin.pages![0].Component!;

/** compose's Projects: media's jellyfin, Jellyfin's Workload, exited out of memory. */
const projects = {
  hosts: [
    {
      name: "media",
      projects: [
        {
          host: "media",
          name: "media",
          state: "PROJECT_STATE_STOPPED",
          services: [{ name: "jellyfin", containers: [{ container: { name: "jellyfin", state: "STATE_EXITED", status: "Exited (137) 6 minutes ago" } }] }],
        },
      ],
    },
  ],
};

const row = (name: string) => within(screen.getByRole("navigation", { name: "Apps" })).getAllByRole("button", { name })[0];

it("shows a down App's stopped Workload as its cause in its row and its pane", async () => {
  renderScoped(<Page />, { ListProjects: () => Response.json(projects) }, ["apps"], "/?app=proxy:jellyfin.home.example");
  await settle();

  expect(row("Jellyfin").textContent).toContain("Compose Project media exited (137)");
  const pane = screen.getByRole("article");
  expect(within(pane).getByText("down: Compose Project media exited (137)")).toBeTruthy();
  // The Check's own status stays with the Check.
  expect(within(within(pane).getByRole("status", { name: "Check" })).getByText("down: HTTP 502")).toBeTruthy();
  // Grafana is up: no cause.
  expect(row("Grafana").textContent).toBe("Grafana");
});

it("names the container alone while compose can't say which Compose Project it is in", async () => {
  renderScoped(<Page />, {}, ["apps"]);
  await settle();

  expect(row("Jellyfin").textContent).toContain("Container jellyfin exited");
});
