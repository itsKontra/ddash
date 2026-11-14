import { metricValue, pending, showWidget, unavailable, type Answers } from "@ddash/plugin/testing";
import { cleanup, screen, within } from "@testing-library/react";
import { afterEach, expect, it } from "vitest";
import plugin from "./index";
import { listed, scope, tunnel } from "./testing";

afterEach(cleanup);

const widget = () => plugin.widgets!.find((w) => w.id === "tunnel")!;

async function show(answers: Answers, size?: "S" | "M") {
  const { reported } = await showWidget(scope, widget(), {
    size,
    answers: { GetTunnel: () => Response.json(tunnel), ListPublicHostnames: () => Response.json(listed), ...answers },
  });
  return reported;
}

it("is the Cloudflare Tunnel Widget, S or M, S by default", () => {
  const w = widget();
  expect([w.title, w.sizes, w.defaultSize]).toEqual(["Cloudflare Tunnel", ["S", "M"], "S"]);
});

it("reports the Tunnel's state with its connectors and Public Hostnames", async () => {
  const reported = await show({});

  expect(reported.status).toEqual({ tone: "up", summary: "Up · 2 connectors · 2 Public Hostnames" });
  const s = screen.getByRole("region", { name: "widget" });
  expect(metricValue("Public Hostnames", s)).toBe("2");
  expect(metricValue("connectors", s)).toBe("2");
});

it("shows the Tunnel and the disabled ones at M", async () => {
  await show({}, "M");
  const m = screen.getByRole("region", { name: "widget" });

  expect(within(m).getByText("Tunnel").nextElementSibling?.textContent).toBe("home: Up");
  expect(within(m).getByText("Disabled").nextElementSibling?.textContent).toBe("1");
});

it("reports down while no connector is connected", async () => {
  const reported = await show({ GetTunnel: () => Response.json({ setUp: true, tunnel: { ...tunnel.tunnel, status: "TUNNEL_STATUS_DOWN", connectors: [] } }) });

  expect(reported.status).toEqual({ tone: "down", summary: "Down · 0 connectors · 2 Public Hostnames" });
});

it("reports not set up, quietly", async () => {
  const reported = await show({ GetTunnel: () => Response.json({}), ListPublicHostnames: () => Response.json({}) });

  expect(reported.status).toEqual({ tone: "idle", summary: "Not set up" });
});

it("reports warn while cloudflare-api can't read the Tunnel", async () => {
  const reported = await show({ GetTunnel: () => unavailable("Cloudflare unreachable: timeout") });

  expect(reported.status).toEqual({ tone: "warn", summary: "cloudflare-api unavailable: Cloudflare unreachable: timeout" });
});

it("reports nothing until cloudflare-api first answers", async () => {
  const reported = await show({ GetTunnel: pending });

  expect(reported.status).toBeUndefined();
});
