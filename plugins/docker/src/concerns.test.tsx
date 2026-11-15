import { act, cleanup } from "@testing-library/react";
import type { Concern } from "@ddash/plugin";
import { pending, renderScoped, settle, unavailable, type Answers } from "@ddash/plugin/testing";
import { afterEach, expect, it } from "vitest";
import plugin from "./index";
import { docker } from "./testing";

afterEach(cleanup);

let reported: Concern[] | undefined;
const plain = () => reported?.map(({ actions, ...c }) => ({ ...c, actions: actions?.map((a) => a.label) }));

// Runs the docker Plugin's useConcerns as the Overview does.
function Probe() {
  reported = plugin.useConcerns!();
  return null;
}

async function show(answers: Answers) {
  const calls = renderScoped(docker, <Probe />, { answers });
  await settle();
  return calls;
}

const ago = (mins: number) => new Date(Date.now() - mins * 60_000).toISOString();

const container = (name: string, image: string, state: string, extra: object = {}) => ({
  container: { id: name, name, image, state: "STATE_RUNNING" },
  imageStatus: { state, ...extra },
});
const failed = (name: string, image: string, reason: string, mins = 70) => container(name, image, "IMAGE_STATE_ERROR", { reason, checked: ago(mins) });

const host = (name: string, containers: object[], loose: object[] = []) => ({
  name,
  projects: [{ host: name, name: "media", state: "PROJECT_STATE_RUNNING", services: [{ name: "app", containers }] }],
  containers: loose,
});
const listed = (...hosts: object[]) => () => Response.json({ hosts, checks: { last: ago(70) } });

it("reports each Docker host whose image check failed, with the images, why and when", async () => {
  await show({
    ListProjects: listed(
      host("apps", [failed("media-jellyfin-1", "jellyfin/jellyfin:10.10.3", "rate limited by registry-1.docker.io"), container("media-sonarr-1", "linuxserver/sonarr:4.0.11", "IMAGE_STATE_UNKNOWN")]),
      host("edge", [container("proxy-npm-1", "jc21/nginx-proxy-manager:2", "IMAGE_STATE_UP_TO_DATE")]),
      host(
        "lab",
        [failed("a-1", "ghcr.io/a/a:1", "denied"), failed("b-1", "ghcr.io/b/b:1", "denied")],
        [failed("c-1", "ghcr.io/c/c:1", "manifest unknown"), container("d-1", "local/d", "IMAGE_STATE_NOT_CHECKED", { reason: "a local image: it has no registry to check" })],
      ),
    ),
  });

  expect(plain()).toEqual([
    {
      id: "apps",
      tone: "info",
      subject: "apps",
      title: "Image check failed on apps",
      cause: "jellyfin/jellyfin:10.10.3 · rate limited by registry-1.docker.io · 1 h ago",
      icon: "package",
      href: "/docker",
      actions: ["Check again"],
    },
    {
      id: "lab",
      tone: "info",
      subject: "lab",
      title: "Image check failed on lab",
      cause: "ghcr.io/a/a:1, ghcr.io/b/b:1 and 1 more · denied; manifest unknown · 1 h ago",
      icon: "package",
      href: "/docker",
      actions: ["Check again"],
    },
  ]);
  // A fix: drawn filled.
  expect(reported![0].actions![0].secondary).toBeUndefined();
});

it("reports none while every check went through, nothing until compose answered, and none when it can't be read", async () => {
  await show({ ListProjects: listed(host("apps", [container("media-jellyfin-1", "jellyfin/jellyfin:10.10.3", "IMAGE_STATE_UP_TO_DATE")])) });
  expect(reported).toEqual([]);

  cleanup();
  await show({ ListProjects: () => pending() });
  expect(reported).toBeUndefined();

  cleanup();
  await show({ ListProjects: () => unavailable("compose is restarting") });
  expect(reported).toEqual([]);
});

it("checks the images again as the Page's Check now does, and drops the host once its check went through", async () => {
  let state = failed("media-jellyfin-1", "jellyfin/jellyfin:10.10.3", "registry timed out");
  const calls = await show({
    ListProjects: () => listed(host("apps", [state]))(),
    CheckImages: () => {
      state = container("media-jellyfin-1", "jellyfin/jellyfin:10.10.3", "IMAGE_STATE_UP_TO_DATE");
      return Response.json({});
    },
  });

  const again = reported![0].actions![0];
  await act(async () => void ("run" in again && (await again.run())));
  await settle();

  expect(calls.filter((c) => c.method === "CheckImages").map((c) => c.body)).toEqual([{}]);
  expect(reported).toEqual([]);
});

it("is busy while a check runs, and says why Check again failed", async () => {
  await show({
    ListProjects: () => Response.json({ hosts: [host("apps", [failed("media-jellyfin-1", "jellyfin/jellyfin:10.10.3", "registry timed out")])], checks: { running: true } }),
  });
  expect(reported![0].actions![0].pending).toBe(true);

  cleanup();
  await show({ ListProjects: listed(host("apps", [failed("media-jellyfin-1", "jellyfin/jellyfin:10.10.3", "registry timed out")])), CheckImages: () => unavailable("compose is restarting") });
  const again = reported![0].actions![0];
  await act(async () => void ("run" in again && (await again.run())));
  await settle();
  expect(reported![0].actions![0].error).toBe("compose is restarting");
});
