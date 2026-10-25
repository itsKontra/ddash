import { cleanup, fireEvent, screen, within } from "@testing-library/react";
import type { Badge, Status, Widget } from "@ddash/plugin";
import { afterEach, expect, it } from "vitest";
import plugin from "./index";
import { pending, settle, unavailable, type Answers } from "@ddash/plugin/testing";
import { Page, renderScoped } from "./testing";

afterEach(cleanup);

// GetSnapshot's answers (Connect JSON).
const loads = { snapshot: { revision: "3", readAt: "2026-10-20T07:15:00Z", servers: [{ serverNames: ["jellyfin.home.example"] }] } };
const nginxError = 'nginx: [emerg] unknown directive "proxy_pas" in /etc/nginx/conf.d/jellyfin.conf:11\nnginx: configuration file /etc/nginx/nginx.conf test failed';
const fails = { snapshot: { revision: "4", readAt: "2026-10-20T07:15:00Z", error: nginxError } };
const neverLoaded = { snapshot: { revision: "1", error: "permission denied (publickey)" } };

it("is the reverse-proxy Nav Entry at order 60 (its icon, waypoints, is in plugin.yaml)", () => {
  expect(plugin.nav?.order).toBe(60);
});

it("shows that the config loads, with the time of the last read", async () => {
  renderScoped(<Page />, { GetSnapshot: () => Response.json(loads) });
  await settle();

  const health = screen.getByRole("region", { name: "Config health" });
  expect(health.textContent).toMatch(/Config loads/);
  expect(within(health).getByText((_, el) => el?.tagName === "TIME").getAttribute("datetime")).toBe("2026-10-20T07:15:00.000Z");
  expect(within(health).queryByRole("alert")).toBeNull();
});

it("shows nginx's error text while the read fails, with the last good read", async () => {
  renderScoped(<Page />, { GetSnapshot: () => Response.json(fails) });
  await settle();

  const health = screen.getByRole("region", { name: "Config health" });
  expect(within(health).getByRole("alert").textContent).toMatch(/nginx's config doesn't load/);
  expect(within(health).getByRole("region", { name: "nginx's error" }).textContent).toBe(nginxError);
  expect(health.textContent).toMatch(/Last good read/);
  expect(within(health).getByText((_, el) => el?.tagName === "TIME").getAttribute("datetime")).toBe("2026-10-20T07:15:00.000Z");
});

it("says so when the config never loaded", async () => {
  renderScoped(<Page />, { GetSnapshot: () => Response.json(neverLoaded) });
  await settle();

  const health = screen.getByRole("region", { name: "Config health" });
  expect(within(health).getByRole("region", { name: "nginx's error" }).textContent).toBe("permission denied (publickey)");
  expect(health.textContent).toMatch(/No good read yet/);
});

it("is the Reverse proxy Page, and shows a skeleton until nginx-config answers", async () => {
  renderScoped(<Page />, { GetSnapshot: pending });
  await settle();

  expect(screen.getByRole("heading", { level: 1, name: "Reverse proxy" })).toBeTruthy();
  // The kicker sits right above the title.
  expect(screen.getByRole("heading", { level: 1, name: "Reverse proxy" }).previousElementSibling?.textContent).toBe("Infrastructure");
  expect(within(screen.getByRole("region", { name: "Config health" })).getByRole("status", { name: "Loading" })).toBeTruthy();
});

it("says why a Refresh failed", async () => {
  renderScoped(<Page />, {
    GetSnapshot: () => Response.json(loads),
    Refresh: () => unavailable("nginx-config is restarting"),
  });
  await settle();

  fireEvent.click(screen.getByRole("button", { name: "Refresh" }));
  await settle();

  expect(screen.getAllByRole("alert").map((a) => a.textContent)).toContain("Refresh failed: nginx-config is restarting");
});

it("re-reads the config at once with Refresh, and shows the new read", async () => {
  let snapshot: object = fails;
  const calls = renderScoped(<Page />, {
    GetSnapshot: () => Response.json(snapshot),
    Refresh: () => {
      snapshot = loads;
      return Response.json(loads);
    },
  });
  await settle();

  fireEvent.click(screen.getByRole("button", { name: "Refresh" }));
  await settle();

  expect(calls.map((c) => c.method)).toContain("Refresh");
  expect(screen.getByRole("region", { name: "Config health" }).textContent).toMatch(/Config loads/);
});

it("says nginx-config is unavailable when it can't answer", async () => {
  renderScoped(<Page />, { GetSnapshot: () => unavailable("nginx-config is starting") });
  await settle();

  const health = screen.getByRole("region", { name: "Config health" });
  expect(within(health).getByRole("alert").textContent).toBe("nginx-config unavailable: nginx-config is starting");
});

const nginx = () => plugin.widgets!.find((w) => w.id === "nginx")!;

async function widgetStatus(answer: Answers[string]) {
  const reported: { status?: Status } = {};
  function Probe({ widget }: { widget: Widget }) {
    reported.status = widget.useStatus();
    return null;
  }
  const W = nginx();
  renderScoped(
    <>
      <Probe widget={W} />
      <section aria-label="widget">
        <W.Body size="S" />
      </section>
    </>,
    { GetSnapshot: answer },
  );
  await settle();
  return reported.status;
}

it("is the nginx Widget, S only", () => {
  const w = nginx();
  expect([w.title, w.sizes, w.defaultSize]).toEqual(["nginx", ["S"], "S"]);
});

it("reports up while the config loads, and shows when it was read", async () => {
  expect(await widgetStatus(() => Response.json(loads))).toEqual({ tone: "up", summary: "config loads" });
  const body = screen.getByRole("region", { name: "widget" });
  expect(body.textContent).toMatch(/loads/);
  expect(within(body).getByText((_, el) => el?.tagName === "TIME").getAttribute("datetime")).toBe("2026-10-20T07:15:00.000Z");
});

it("reports down with the error's first line while the read fails", async () => {
  expect(await widgetStatus(() => Response.json(fails))).toEqual({
    tone: "down",
    summary: 'nginx: [emerg] unknown directive "proxy_pas" in /etc/nginx/conf.d/jellyfin.conf:11',
  });
  expect(screen.getByRole("region", { name: "widget" }).textContent).toMatch(/doesn't load/);
});

it("shows a skeleton until nginx-config answers", async () => {
  expect(await widgetStatus(pending)).toBeUndefined();
  expect(within(screen.getByRole("region", { name: "widget" })).getByRole("status", { name: "Loading" })).toBeTruthy();
});

it("reports warn when nginx-config is unavailable", async () => {
  const status = await widgetStatus(() => unavailable("nginx-config is down"));
  expect(status).toEqual({ tone: "warn", summary: "nginx-config unavailable: nginx-config is down" });
});

async function badge(answer: () => Response) {
  const seen: { badge?: Badge | null } = {};
  function Probe() {
    seen.badge = plugin.nav!.useBadge!();
    return null;
  }
  renderScoped(<Probe />, { GetSnapshot: answer });
  await settle();
  return seen.badge;
}

it("badges the Nav Entry with ! while the read fails, and not otherwise", async () => {
  expect(await badge(() => Response.json(fails))).toEqual({ text: "!", tone: "down" });
  cleanup();
  expect(await badge(() => Response.json(loads))).toBeNull();
});
