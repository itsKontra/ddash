import { act, cleanup, render, screen, within } from "@testing-library/react";
import { createQueryClient, PluginHost, PluginScope, type Status, type Widget } from "@ddash/plugin";
import { afterEach, expect, it, vi } from "vitest";
import plugin from "./index";

afterEach(cleanup);

const container = (name: string, state: string, status: string) => ({ id: name, name, image: `${name}:latest`, state, status });

const healthy = {
  hosts: [
    {
      host: "media",
      containers: [container("jellyfin", "running", "Up 3 hours (healthy)"), container("sonarr", "exited", "Exited (0) 2 days ago")],
    },
    { host: "tools", containers: [container("grafana", "running", "Up 2 days")] },
  ],
};

const containers = () => plugin.widgets!.find((w) => w.id === "containers")!;

// Renders the Widget's body at size, against docker-api answering with
// answer, and returns what its useStatus reported last.
async function show(answer: Response | (() => Response), size: "S" | "M" = "S") {
  const w = containers();
  const fetch = vi.fn(async () => (typeof answer === "function" ? answer() : answer.clone()));
  const reported: { status?: Status } = {};
  function Probe({ widget }: { widget: Widget }) {
    reported.status = widget.useStatus();
    return null;
  }
  render(
    <PluginHost client={createQueryClient()}>
      <PluginScope plugin={{ name: "docker", requires: ["docker-api"] }} fetch={fetch}>
        <Probe widget={w} />
        <section aria-label="widget">
          <w.Body size={size} />
        </section>
      </PluginScope>
    </PluginHost>,
  );
  await act(() => new Promise((r) => setTimeout(r, 50)));
  return reported;
}

it("is the containers Widget, S or M, S by default", () => {
  const w = containers();
  expect([w.title, w.sizes, w.defaultSize]).toEqual(["Containers", ["S", "M"], "S"]);
});

it("reports up with the running count while every container is fine", async () => {
  const reported = await show(Response.json(healthy));

  expect(reported.status).toEqual({ tone: "up", summary: "2 of 3 running" });
});

it("shows the counts at S, and each host's at M", async () => {
  await show(Response.json(healthy));
  const s = screen.getByRole("region", { name: "widget" });
  expect(within(s).getByText("running").previousSibling?.textContent).toBe("2");
  expect(within(s).getByText("containers").previousSibling?.textContent).toBe("3");
  cleanup();

  await show(Response.json(healthy), "M");
  const m = screen.getByRole("region", { name: "widget" });
  expect(within(m).getByRole("row", { name: /media/ }).textContent).toMatch(/media.*1\/2 running/);
  expect(within(m).getByRole("row", { name: /tools/ }).textContent).toMatch(/tools.*1\/1 running/);
});

it("reports warn when a container is unhealthy or restarting", async () => {
  const troubled = {
    hosts: [
      {
        host: "media",
        containers: [
          container("jellyfin", "running", "Up 3 hours (unhealthy)"),
          container("sonarr", "restarting", "Restarting (1) 5 seconds ago"),
          container("radarr", "running", "Up 3 hours (healthy)"),
        ],
      },
    ],
  };

  const reported = await show(Response.json(troubled));

  expect(reported.status).toEqual({ tone: "warn", summary: "1 unhealthy · 1 restarting" });
});

it("reports warn when docker-api is unavailable", async () => {
  const reported = await show(() => Response.json({ code: "unavailable", message: "docker-api is down" }, { status: 503 }));

  expect(reported.status).toEqual({ tone: "warn", summary: "docker-api unavailable: docker-api is down" });
});

it("reports warn when a Docker host can't be read", async () => {
  const reported = await show(Response.json({ hosts: [...healthy.hosts, { host: "nas", error: "connection refused" }] }));

  expect(reported.status).toEqual({ tone: "warn", summary: "nas unreadable" });
});

it("reports nothing until docker-api first answers", async () => {
  const reported = await show(() => new Promise<Response>(() => {}) as unknown as Response);

  expect(reported.status).toBeUndefined();
});
