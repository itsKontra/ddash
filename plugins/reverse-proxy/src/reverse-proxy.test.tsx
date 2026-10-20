import { act, cleanup, fireEvent, render, screen, within } from "@testing-library/react";
import { createQueryClient, PluginHost, PluginScope, type Badge, type Status, type Widget } from "@ddash/plugin";
import { afterEach, expect, it, vi } from "vitest";
import plugin from "./index";

afterEach(cleanup);

// GetSnapshot's answers (Connect JSON).
const loads = { snapshot: { revision: "3", readAt: "2026-10-20T07:15:00Z", servers: [{ serverNames: ["jellyfin.home.example"] }] } };
const nginxError = 'nginx: [emerg] unknown directive "proxy_pas" in /etc/nginx/conf.d/jellyfin.conf:11\nnginx: configuration file /etc/nginx/nginx.conf test failed';
const fails = { snapshot: { revision: "4", readAt: "2026-10-20T07:15:00Z", error: nginxError } };
const neverLoaded = { snapshot: { revision: "1", error: "permission denied (publickey)" } };

type Answers = Record<string, () => Response>;

const settle = () => act(() => new Promise((r) => setTimeout(r, 50)));

// Renders children in the reverse-proxy Plugin's scope against nginx-config
// answering by method name, and returns the methods called.
function renderScoped(children: React.ReactNode, answers: Answers) {
  const calls: string[] = [];
  const fetch = vi.fn(async (input: RequestInfo | URL) => {
    const method = String(input).split("/").pop()!;
    calls.push(method);
    return answers[method]?.() ?? Response.json({ code: "unimplemented", message: method }, { status: 501 });
  });
  render(
    <PluginHost client={createQueryClient()}>
      <PluginScope plugin={{ name: "reverse-proxy", requires: ["nginx-config"] }} fetch={fetch}>
        {children}
      </PluginScope>
    </PluginHost>,
  );
  return calls;
}

const Page = () => {
  const P = plugin.pages![0].Component!;
  return <P />;
};

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
  expect(within(health).getByRole("alert").textContent).toBe(nginxError);
  expect(health.textContent).toMatch(/doesn't load/);
  expect(health.textContent).toMatch(/Last good read/);
  expect(within(health).getByText((_, el) => el?.tagName === "TIME").getAttribute("datetime")).toBe("2026-10-20T07:15:00.000Z");
});

it("says so when the config never loaded", async () => {
  renderScoped(<Page />, { GetSnapshot: () => Response.json(neverLoaded) });
  await settle();

  const health = screen.getByRole("region", { name: "Config health" });
  expect(within(health).getByRole("alert").textContent).toBe("permission denied (publickey)");
  expect(health.textContent).toMatch(/No good read yet/);
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

  expect(calls).toContain("Refresh");
  expect(screen.getByRole("region", { name: "Config health" }).textContent).toMatch(/Config loads/);
});

it("says nginx-config is unavailable when it can't answer", async () => {
  renderScoped(<Page />, { GetSnapshot: () => Response.json({ code: "unavailable", message: "nginx-config is starting" }, { status: 503 }) });
  await settle();

  expect(screen.getByRole("region", { name: "Config health" }).textContent).toMatch(/nginx-config is starting/);
});

const nginx = () => plugin.widgets!.find((w) => w.id === "nginx")!;

async function widgetStatus(answer: () => Response) {
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

it("reports warn when nginx-config is unavailable", async () => {
  const status = await widgetStatus(() => Response.json({ code: "unavailable", message: "nginx-config is down" }, { status: 503 }));
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
