import { act, cleanup, fireEvent, screen, within } from "@testing-library/react";
import { afterEach, expect, it } from "vitest";
import { settle } from "@ddash/plugin/testing";
import { planDelay } from "./add-app";
import { draftFragment, type Draft } from "./draft";
import plugin from "./index";
import { actions, ranThrough, renderScoped, split } from "./testing";

afterEach(cleanup);

const Page = plugin.pages![0].Component!;

/** ListDockerHosts' answer: media, with a Compose root and jellyfin on 8096. */
const dockerHosts = {
  hosts: [
    { name: "media", address: "198.18.2.10", roots: ["/opt/stacks"], projects: ["jellyfin"], usedPorts: [8096] },
    { name: "nas", address: "198.18.2.20", roots: ["/volume1/docker"], error: "timeout" },
  ],
};

const compose = "services:\n  paperless:\n    image: ghcr.io/paperless-ngx/paperless-ngx:2.14.7\n    ports: [\"8010:8000\", \"9000:9000\"]\n  db:\n    image: postgres:16.6\n";

/** The Plan of Add App of paperless from a new Compose Project (Connect JSON), its upstream port that asked for. */
const paperlessPlan = (port = 8010) => ({
  steps: [
    { system: "SYSTEM_DOCKER", title: "Write the Compose file of paperless on media", values: [{ label: "Compose file", after: "/opt/stacks/paperless/compose.yaml" }] },
    { system: "SYSTEM_DOCKER", title: "Start paperless on media" },
    { system: "SYSTEM_DOCKER", title: "Wait until paperless runs", values: [{ label: "Container", after: "paperless-paperless-1" }] },
    { system: "SYSTEM_NGINX_PROXY_MANAGER", title: "Create Proxy Host paperless.home.example", values: [{ label: "Upstream", after: `http://198.18.2.10:${port}` }] },
    { system: "SYSTEM_PFSENSE", title: "Add alias paperless.home.example to nginx.home.example" },
    { system: "SYSTEM_DDASH", title: "Create the App Paperless-ngx" },
  ],
  composeServices: [{ name: "paperless", ports: [8010, 9000] }, { name: "db" }],
});

interface PlanRequest {
  action?: { add?: { url?: string; compose?: { port?: number }; proxyHost?: object } };
}

/** PlanAction answering the paperless Plan for the port asked for. */
const planning = (body: unknown) => {
  const add = (body as PlanRequest).action?.add;
  if (!add?.url) return Response.json({ plan: { blocked: ["Give the App's URL."] } });
  return Response.json({ plan: paperlessPlan(add.compose?.port || 8010) });
};

const draft: Draft = {
  host: "media",
  name: "paperless",
  compose,
  app: { url: "https://paperless.home.example", name: "Paperless-ngx", icon: "paperless-ngx", group: "Documents", service: "paperless", port: 8010 },
};

const open = async (path: string, answers = {}, installed = ["apps"]) => {
  const calls = renderScoped(
    <Page />,
    { ListApps: () => Response.json(split), ListDockerHosts: () => Response.json(dockerHosts), PlanAction: planning, ...answers },
    installed,
    path,
  );
  await settle();
  await act(() => new Promise((r) => setTimeout(r, planDelay + 50)));
  await settle();
  return calls;
};

const form = () => within(screen.getByRole("region", { name: "Add App" }));
const will = () => within(screen.getByRole("region", { name: "ddash will" }));
const value = (label: string) => (form().getByLabelText(label) as HTMLInputElement).value;
const plans = (calls: { method: string; body: unknown }[]) => calls.filter((c) => c.method === "PlanAction").map((c) => c.body as PlanRequest);

it("opens a draft from the URL fragment in Add App from a new Compose Project, and runs nothing until Add", async () => {
  const calls = await open(`/?add=compose${draftFragment(draft)}`, { RunAction: ranThrough({ appId: "proxy:paperless.home.example", done: 6 }) });

  expect(form().getByText(/A draft from the AI App helper/)).toBeTruthy();
  expect(form().getByRole("radio", { name: "New Compose Project" }).getAttribute("aria-checked")).toBe("true");
  expect([value("URL"), value("Name"), value("Group"), value("Project name"), value("Compose file"), value("Docker host")]).toEqual([
    "https://paperless.home.example",
    "Paperless-ngx",
    "Documents",
    "paperless",
    compose,
    "media",
  ]);
  expect((form().getByLabelText("Directory") as HTMLInputElement).placeholder).toBe("/opt/stacks/paperless");
  expect(actions(calls)).toEqual([]);

  // The Plan is asked for the Compose Project, the upstream left to ddash.
  expect(plans(calls).at(-1)?.action?.add).toMatchObject({
    url: "https://paperless.home.example",
    name: "Paperless-ngx",
    icon: "paperless-ngx",
    group: "Documents",
    reach: "REACH_PROXIED",
    compose: { host: "media", name: "paperless", content: compose, service: "paperless", port: 8010 },
  });
  expect(JSON.stringify(plans(calls).at(-1)?.action?.add?.proxyHost)).not.toMatch(/forwardHost|forwardPort/);
  expect(value("Upstream")).toBe("http://198.18.2.10:8010");
  expect(will().getByText("Write the Compose file of paperless on media")).toBeTruthy();
  expect(will().getAllByText("Docker")).toHaveLength(3);

  fireEvent.click(form().getByRole("button", { name: "Add App" }));
  await settle();
  expect(actions(calls).map((c) => c.method)).toEqual(["RunAction"]);
});

it("offers the Compose services and their published ports from the Plan", async () => {
  const calls = await open("/?app=new");
  fireEvent.click(form().getByRole("radio", { name: "New Compose Project" }));
  await settle();
  // The first host until one is picked.
  expect(value("Docker host")).toBe("media");
  expect(form().getByText("Published ports answer at 198.18.2.10.")).toBeTruthy();
  expect(form().getByRole("option", { name: "nas (unreachable)" })).toBeTruthy();

  fireEvent.change(form().getByLabelText("URL"), { target: { value: "https://paperless.home.example" } });
  fireEvent.change(form().getByLabelText("Compose file"), { target: { value: compose } });
  await act(() => new Promise((r) => setTimeout(r, planDelay + 50)));
  await settle();

  const service = form().getByLabelText("Compose service") as HTMLSelectElement;
  expect([...service.options].map((o) => o.text)).toEqual(["Automatic: paperless", "paperless", "db"]);
  const port = form().getByLabelText("Published port") as HTMLSelectElement;
  expect([...port.options].map((o) => o.text)).toEqual(["Automatic: 8010", "8010", "9000"]);
  expect(form().getByText("In use on media: 8096.")).toBeTruthy();

  fireEvent.change(port, { target: { value: "9000" } });
  await act(() => new Promise((r) => setTimeout(r, planDelay + 50)));
  await settle();
  expect(plans(calls).at(-1)?.action?.add?.compose).toMatchObject({ host: "media", content: compose, port: 9000 });
  expect(value("Upstream")).toBe("http://198.18.2.10:9000");
});

it("says when no Docker host has an Agent, linking the Docker Page when it is installed", async () => {
  await open("/?add=compose", { ListDockerHosts: () => Response.json({}) }, ["apps", "docker"]);
  expect(form().getByText(/No Docker host has a paired Docker Agent yet/)).toBeTruthy();
  expect(form().getByRole("link", { name: "Add one on the Docker Page" }).getAttribute("href")).toBe("/docker");
  expect(form().queryByText(/A draft from the AI App helper/)).toBeNull();
});

it("links Draft with AI only while the AI App helper is installed", async () => {
  await open("/?app=new");
  expect(form().queryByRole("link", { name: "Draft with AI" })).toBeNull();
  cleanup();
  await open("/?app=new", {}, ["apps", "ai-helper"]);
  expect(form().getByRole("link", { name: "Draft with AI" }).getAttribute("href")).toBe("/ai-helper");
});

it("sends a draft's .env, and what's typed in it, with the new Compose Project", async () => {
  const calls = await open(`/?add=compose${draftFragment({ ...draft, env: "PAPERLESS_SECRET_KEY=s3cret\n" })}`);
  expect(value(".env")).toBe("PAPERLESS_SECRET_KEY=s3cret\n");
  const envOf = () => (plans(calls).at(-1)?.action?.add?.compose as { env?: string } | undefined)?.env;
  expect(envOf()).toBe("PAPERLESS_SECRET_KEY=s3cret\n");

  fireEvent.change(form().getByLabelText(".env"), { target: { value: "TZ=Europe/Berlin\n" } });
  await act(() => new Promise((r) => setTimeout(r, planDelay + 50)));
  await settle();
  expect(envOf()).toBe("TZ=Europe/Berlin\n");
});
