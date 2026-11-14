import { cleanup, fireEvent, screen, within } from "@testing-library/react";
import { settle, showPlugin, type Answers } from "@ddash/plugin/testing";
import { useLocation } from "react-router";
import { afterEach, expect, it } from "vitest";
import plugin from "./index";

afterEach(cleanup);

const scope = { name: "ai-helper", requires: ["ai-helper", "compose"] };

/** compose's ListProjects (Connect JSON): media with jellyfin on 8096 and a loose container on 9000. */
const projects = {
  hosts: [
    {
      name: "media",
      address: "198.18.2.10",
      roots: ["/opt/stacks"],
      projects: [
        {
          host: "media",
          name: "jellyfin",
          services: [{ name: "jellyfin", containers: [{ id: "a", name: "jellyfin-jellyfin-1", ports: [{ publicPort: 8096, privatePort: 8096, protocol: "tcp" }] }] }],
        },
      ],
      containers: [{ id: "b", name: "portainer", ports: [{ publicPort: 9000, privatePort: 9000, protocol: "tcp" }, { privatePort: 8000 }] }],
    },
  ],
};

/** DraftApp's answer: the canned Paperless draft. */
const drafted = {
  draft: {
    name: "paperless",
    compose: "services:\n  paperless:\n    image: ghcr.io/paperless-ngx/paperless-ngx:2.14.7\n    ports: [\"8010:8000\"]\n",
    app: { url: "https://paperless.home.example", name: "Paperless-ngx", icon: "paperless-ngx", group: "Documents", service: "paperless", port: 8010 },
    notes: ["Change PAPERLESS_SECRET_KEY before the first start."],
    model: "claude-sonnet-5-5",
  },
};

let where = "";
function Where() {
  const l = useLocation();
  where = l.pathname + l.search + l.hash;
  return null;
}

const Page = plugin.pages![0].Component!;
const Shown = () => (
  <>
    <Page />
    <Where />
  </>
);

const show = async (answers: Answers = {}, installed?: string[]) =>
  showPlugin({ ...plugin, pages: [{ index: true, Component: Shown }] }, scope, "/ai-helper", {
    answers: {
      GetStatus: () => Response.json({ setUp: true, model: "claude-sonnet-5-5" }),
      ListProjects: () => Response.json(projects),
      DraftApp: () => Response.json(drafted),
      ...answers,
    },
    installed,
  });

const describe = (text: string) => fireEvent.change(screen.getByLabelText("Description"), { target: { value: text } });

/** What the preview says is sent. */
const sent = () => screen.getByRole("region", { name: "Sent to Anthropic" }).textContent;

/** The draft JSON a "#draft=…" fragment carries. */
const decoded = (fragment: string) => {
  const raw = new URLSearchParams(fragment.replace(/^#/, "")).get("draft")!;
  const bin = atob(raw.replace(/-/g, "+").replace(/_/g, "/"));
  return JSON.parse(new TextDecoder().decode(Uint8Array.from(bin, (c) => c.charCodeAt(0))));
};

it("asks for an API key while not set up, linking Plugins, and offers nothing to send", async () => {
  await show({ GetStatus: () => Response.json({ model: "claude-sonnet-5-5" }) });
  expect(screen.getByText(/Enter an Anthropic API key/)).toBeTruthy();
  expect(screen.getByRole("link", { name: "Plugins" }).getAttribute("href")).toBe("/plugins");
  expect(screen.queryByLabelText("Description")).toBeNull();
});

it("shows exactly what is sent before sending, and sends only once asked", async () => {
  const calls = await show();
  describe("  Paperless-ngx with Postgres & Redis <fast>\n");
  fireEvent.change(screen.getByLabelText("Docker host"), { target: { value: "media" } });
  fireEvent.click(screen.getByRole("button", { name: "Preview what is sent" }));
  await settle();

  // The same text ai-helper's Go Prompt builds (TestThePromptIsWhatThePreviewShows).
  expect(sent()).toBe(`Description:
Paperless-ngx with Postgres & Redis <fast>

Context:
{
  "host": "media",
  "projects": [
    "jellyfin"
  ],
  "used_ports": [
    8096,
    9000
  ],
  "networks": [],
  "home_domain": ""
}`);
  expect(calls.filter((c) => c.method === "DraftApp")).toEqual([]);

  // Editing drops the preview: it must be looked at again.
  fireEvent.change(screen.getByLabelText("Home domain"), { target: { value: "home.example" } });
  fireEvent.change(screen.getByLabelText("Networks"), { target: { value: "proxy, media_default" } });
  expect(screen.queryByRole("region", { name: "Sent to Anthropic" })).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: "Preview what is sent" }));
  await settle();
  expect(sent()).toContain(`"networks": [\n    "proxy",\n    "media_default"\n  ],\n  "home_domain": "home.example"`);

  fireEvent.click(screen.getByRole("button", { name: "Send to Anthropic" }));
  await settle();
  expect(calls.filter((c) => c.method === "DraftApp").map((c) => c.body)).toEqual([
    {
      description: "Paperless-ngx with Postgres & Redis <fast>",
      context: { host: "media", projects: ["jellyfin"], usedPorts: [8096, 9000], networks: ["proxy", "media_default"], homeDomain: "home.example" },
    },
  ]);
  const draft = within(screen.getByRole("region", { name: "Draft" }));
  expect(draft.getByText("Change PAPERLESS_SECRET_KEY before the first start.")).toBeTruthy();
  expect(draft.getByRole("region", { name: "Compose file" }).textContent).toBe(drafted.draft.compose);
  // Without the Docker and Apps Plugins, the draft opens nowhere.
  expect(draft.queryByRole("link", { name: "Open in Add App" })).toBeNull();
  expect(draft.queryByRole("link", { name: "Open in Add Compose Project" })).toBeNull();
});

it("opens the draft in Add App or Add Compose Project, carried in the URL fragment", async () => {
  await show({}, ["ai-helper", "apps", "docker"]);
  describe("Paperless-ngx");
  fireEvent.change(screen.getByLabelText("Docker host"), { target: { value: "media" } });
  fireEvent.click(screen.getByRole("button", { name: "Preview what is sent" }));
  await settle();
  fireEvent.click(screen.getByRole("button", { name: "Send to Anthropic" }));
  await settle();

  const want = {
    host: "media",
    name: "paperless",
    compose: drafted.draft.compose,
    app: { url: "https://paperless.home.example", name: "Paperless-ngx", icon: "paperless-ngx", group: "Documents", service: "paperless", port: 8010 },
  };
  const toApps = screen.getByRole("link", { name: "Open in Add App" }).getAttribute("href")!;
  expect(toApps).toMatch(/^\/apps\?add=compose#draft=[A-Za-z0-9_-]+$/);
  expect(decoded(toApps.slice(toApps.indexOf("#")))).toEqual(want);
  const toDocker = screen.getByRole("link", { name: "Open in Add Compose Project" }).getAttribute("href")!;
  expect(toDocker).toMatch(/^\/docker\/projects\/new#draft=/);
  expect(decoded(toDocker.slice(toDocker.indexOf("#")))).toEqual(want);
  expect(where).toBe("/ai-helper");
});

it("sends no host when none is picked, and opens only in the Plugins installed", async () => {
  await show({}, ["ai-helper", "docker"]);
  describe("Paperless-ngx");
  fireEvent.click(screen.getByRole("button", { name: "Preview what is sent" }));
  await settle();
  expect(sent()).toContain(`"host": "",\n  "projects": [],\n  "used_ports": []`);
  fireEvent.click(screen.getByRole("button", { name: "Send to Anthropic" }));
  await settle();
  expect(screen.queryByRole("link", { name: "Open in Add App" })).toBeNull();
  const toDocker = screen.getByRole("link", { name: "Open in Add Compose Project" }).getAttribute("href")!;
  expect(decoded(toDocker.slice(toDocker.indexOf("#"))).host).toBeUndefined();
});

it("says why a draft failed, keeping the preview", async () => {
  await show({ DraftApp: () => Response.json({ code: "failed_precondition", message: "Anthropic refused the API key" }, { status: 400 }) });
  describe("Paperless-ngx");
  fireEvent.click(screen.getByRole("button", { name: "Preview what is sent" }));
  await settle();
  fireEvent.click(screen.getByRole("button", { name: "Send to Anthropic" }));
  await settle();
  expect(screen.getByText("Anthropic refused the API key")).toBeTruthy();
  expect(screen.getByRole("region", { name: "Sent to Anthropic" })).toBeTruthy();
});
