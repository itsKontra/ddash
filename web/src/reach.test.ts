import { expect, it, vi } from "vitest";
import { HostWatch } from "./reach";

const json = (status: number) => Response.json({ code: "unavailable", message: "busy" }, { status });
const html = (status: number) => new Response("<html>Bad Gateway</html>", { status, headers: { "Content-Type": "text/html" } });

async function after(res: Response, url: string) {
  const watch = new HostWatch(async () => res, vi.fn());
  await watch.fetch(url, {});
  return watch.get().unreachable;
}

it("counts a Component's Connect error, even a 503, as ddash answering", async () => {
  expect(await after(json(503), "/api/c/docker-api/ListContainers")).toBe(false);
});

it("counts any other 5xx from the host as ddash failing, Connect error or not", async () => {
  expect(await after(json(503), "/api/core/ddash.core.CoreService/SaveLayout")).toBe(true);
  expect(await after(json(500), "/api/core/ddash.core.CoreService/ListInstalledPlugins")).toBe(true);
  expect(await after(html(502), "/api/c/docker-api/ListContainers")).toBe(true);
  expect(await after(html(502), "/plugins/docker/v1.2.3/index.js")).toBe(true);
});

it("takes the path from a URL or a Request too", async () => {
  expect(await after(json(503), "https://ddash.home.example/api/c/docker-api/ListContainers")).toBe(false);
  const watch = new HostWatch(async () => json(503), vi.fn());
  await watch.fetch(new Request("https://ddash.home.example/api/core/ddash.core.CoreService/GetLayout"), {});
  expect(watch.get().unreachable).toBe(true);
});
