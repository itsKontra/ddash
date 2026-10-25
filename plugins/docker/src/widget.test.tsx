import { cleanup, screen, within } from "@testing-library/react";
import type { Status, Widget } from "@ddash/plugin";
import { metricValue, pending, renderScoped, settle, unavailable, type Answer } from "@ddash/plugin/testing";
import { afterEach, expect, it } from "vitest";
import plugin from "./index";
import { docker } from "./testing";

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
async function show(answer: Answer, size: "S" | "M" = "S") {
  const w = containers();
  const reported: { status?: Status } = {};
  function Probe({ widget }: { widget: Widget }) {
    reported.status = widget.useStatus();
    return null;
  }
  renderScoped(
    docker,
    <>
      <Probe widget={w} />
      <section aria-label="widget">
        <w.Body size={size} />
      </section>
    </>,
    { answers: { ListContainers: answer } },
  );
  await settle();
  return reported;
}

const widget = () => screen.getByRole("region", { name: "widget" });

it("is the containers Widget, S or M, S by default", () => {
  const w = containers();
  expect([w.title, w.sizes, w.defaultSize]).toEqual(["Containers", ["S", "M"], "S"]);
});

it("reports up with the running count while every container is fine", async () => {
  const reported = await show(() => Response.json(healthy));

  expect(reported.status).toEqual({ tone: "up", summary: "2 of 3 running" });
});

it("shows the counts at S, and each host's at M", async () => {
  await show(() => Response.json(healthy));
  const s = screen.getByRole("region", { name: "widget" });
  expect(metricValue("running", s)).toBe("2");
  expect(metricValue("containers", s)).toBe("3");
  expect(within(s).queryByRole("group", { name: "need attention" })).toBeNull();
  expect(within(s).queryByRole("definition")).toBeNull();
  cleanup();

  await show(() => Response.json({ hosts: [...healthy.hosts, { host: "nas", error: "connection refused" }] }), "M");
  const m = screen.getByRole("region", { name: "widget" });
  const facts = within(m)
    .getAllByRole("term")
    .map((t) => `${t.textContent}: ${t.nextElementSibling?.textContent}`);
  expect(facts).toEqual(["media: 1/2 running", "tools: 1/1 running", "nas: unreadable"]);
});

it("counts the containers that need attention", async () => {
  await show(() => Response.json({ hosts: [{ host: "media", containers: [container("sonarr", "restarting", "Restarting (1) 5 seconds ago")] }] }));

  expect(metricValue("need attention", widget())).toBe("1");
});

it("draws its tiles as skeletons until docker-api first answers", async () => {
  await show(pending);

  expect(metricValue("running", widget())).toBeUndefined();
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

  const reported = await show(() => Response.json(troubled));

  expect(reported.status).toEqual({ tone: "warn", summary: "1 unhealthy · 1 restarting" });
});

it("reports warn when docker-api is unavailable", async () => {
  const reported = await show(() => unavailable("docker-api is down"));

  expect(reported.status).toEqual({ tone: "warn", summary: "docker-api unavailable: docker-api is down" });
});

it("reports warn when a Docker host can't be read", async () => {
  const reported = await show(() => Response.json({ hosts: [...healthy.hosts, { host: "nas", error: "connection refused" }] }));

  expect(reported.status).toEqual({ tone: "warn", summary: "nas unreadable" });
});

it("reports nothing until docker-api first answers", async () => {
  const reported = await show(pending);

  expect(reported.status).toBeUndefined();
});
