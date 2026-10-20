import { create, toBinary } from "@bufbuild/protobuf";
import { ConfigErrorsSchema } from "@ddash/proto/ddash/core/core_pb";
import { act, cleanup, fireEvent, render, screen, within } from "@testing-library/react";
import { createQueryClient } from "@ddash/plugin";
import { MemoryRouter } from "react-router";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { App } from "./app";

// Config forms as GetConfigForm answers them in JSON, by owner.
const ntfyForm = () => ({
  fields: [
    { name: "server", kind: "KIND_STRING", label: "Server", format: "url", default: "https://ntfy.sh", placeholder: "https://ntfy.sh" },
    { name: "topic", kind: "KIND_STRING", label: "Topic", help: "Where Notifications go.", required: true },
    { name: "token", kind: "KIND_STRING", label: "Token", secret: true },
    { name: "priority", kind: "KIND_INT", label: "Priority", min: "1", max: "5", default: "3", placeholder: "3" },
    { name: "markdown", kind: "KIND_BOOL", label: "Markdown", default: "false", placeholder: "false" },
    {
      name: "tone",
      kind: "KIND_ENUM",
      label: "Tone",
      default: "TONE_WARM",
      placeholder: "TONE_WARM",
      options: [
        { value: "TONE_WARM", label: "Warm" },
        { value: "TONE_COLD", label: "Cold" },
      ],
    },
    { name: "tags", kind: "KIND_STRING", label: "Tags", repeated: true },
  ],
  values: { topic: "ddash", priority: "4", tags: ["a", "b"] },
  secretsSet: ["token"],
});

const dockerForm = () => ({
  fields: [
    {
      name: "hosts",
      kind: "KIND_TABLE",
      label: "Docker hosts",
      repeated: true,
      columns: [
        { name: "name", kind: "KIND_STRING", label: "Name", required: true },
        { name: "url", kind: "KIND_STRING", label: "Socket-proxy URL", required: true, format: "url" },
      ],
    },
  ],
  values: { hosts: [{ name: "media", url: "tcp://198.18.2.10:2375" }] },
});

type Comp = { name: string; state?: string; message?: string; configurable?: boolean; needsConfig?: boolean };

let components: Comp[];
let forms: Record<string, ReturnType<typeof ntfyForm> | ReturnType<typeof dockerForm>>;
let saves: Record<string, unknown>[];
let saveAnswer: () => Response;
let installAnswer: Record<string, unknown>;

beforeEach(() => {
  saves = [];
  components = [
    { name: "ntfy-api", state: "ok", configurable: true },
    { name: "docker-api", state: "ok", configurable: true },
    { name: "checks", state: "ok" },
  ];
  forms = { "ntfy-api": ntfyForm(), "docker-api": dockerForm() };
  saveAnswer = () => Response.json({ status: { name: "ntfy-api", state: "ok", configurable: true } });
  installAnswer = {};
  window.localStorage.clear();
});

afterEach(cleanup);

const fetchFake = async (input: RequestInfo | URL, init?: RequestInit) => {
  const method = String(input).split("/").pop()!;
  const body = JSON.parse(new TextDecoder().decode(init?.body as Uint8Array));
  switch (method) {
    case "ListCatalogue":
      return Response.json({
        release: "v1",
        plugins: [
          { name: "checks", title: "Checks", installed: false, components: [{ name: "checks" }, { name: "ntfy-api" }] },
          { name: "docker", title: "Docker", installed: true, components },
        ],
      });
    case "GetConfigForm":
      return Response.json(forms[body.owner]);
    case "SaveConfig":
      saves.push(body);
      return saveAnswer();
    case "Install":
      return Response.json(installAnswer);
    default:
      return Response.json({});
  }
};

const settle = () => act(() => new Promise((r) => setTimeout(r, 20)));

async function boot() {
  render(
    <MemoryRouter initialEntries={["/plugins"]}>
      <App client={createQueryClient()} fetch={vi.fn(fetchFake)} importBundle={async () => ({})} />
    </MemoryRouter>,
  );
  await settle();
}

async function configure(component: string) {
  const docker = screen.getByRole("listitem", { name: "Docker" });
  fireEvent.click(within(docker).getByRole("button", { expanded: false }));
  const row = within(within(docker).getByRole("list", { name: "Component health" })).getByRole("listitem", { name: component });
  fireEvent.click(within(row).getByRole("button", { name: "Configure" }));
  await settle();
  return screen.getByRole("dialog", { name: `${component} config` });
}

const save = async (form: HTMLElement) => {
  fireEvent.click(within(form).getByRole("button", { name: "Save" }));
  await settle();
};

const input = (form: HTMLElement, label: string) => within(form).getByLabelText(label) as HTMLInputElement;

it("renders each field from the schema: labels, help, defaults as placeholders, stored values", async () => {
  await boot();

  const form = await configure("ntfy-api");

  expect(input(form, "Server").value).toBe("");
  expect(input(form, "Server").placeholder).toBe("https://ntfy.sh");
  expect(input(form, "Topic").value).toBe("ddash");
  expect(within(form).getByText("Where Notifications go.")).toBeTruthy();
  expect(input(form, "Priority").value).toBe("4");
  expect(within(form).getByRole("switch", { name: "Markdown" }).getAttribute("aria-checked")).toBe("false");
  const tone = within(form).getByLabelText("Tone") as HTMLSelectElement;
  expect([...tone.options].map((o) => o.textContent)).toEqual(["Default (Warm)", "Warm", "Cold"]);
  expect(within(form).getAllByRole("textbox", { name: /^Tags/ }).map((t) => (t as HTMLInputElement).value)).toEqual(["a", "b"]);
});

it("never shows a secret: only set or unset, with Replace and Clear", async () => {
  await boot();
  const form = await configure("ntfy-api");

  const token = within(form).getByRole("group", { name: "Token" });
  expect(token.textContent).toMatch(/set/);
  expect(within(token).queryByRole("textbox")).toBeNull();
  expect(within(token).getByRole("button", { name: "Replace" })).toBeTruthy();
  expect(within(token).getByRole("button", { name: "Clear" })).toBeTruthy();
});

it("saves only what is set, leaving an unchanged secret out, and shows the health polled after Start", async () => {
  saveAnswer = () => Response.json({ status: { name: "ntfy-api", state: "down", message: "401 Unauthorized", configurable: true } });
  await boot();
  const form = await configure("ntfy-api");

  fireEvent.change(input(form, "Server"), { target: { value: "https://ntfy.home.example" } });
  await save(form);

  expect(saves).toEqual([
    { owner: "ntfy-api", values: { server: "https://ntfy.home.example", topic: "ddash", priority: "4", tags: ["a", "b"] } },
  ]);
  expect(screen.getByRole("status").textContent).toMatch(/down.*401 Unauthorized/);
});

it("replaces or clears a secret", async () => {
  await boot();
  let form = await configure("ntfy-api");

  fireEvent.click(within(within(form).getByRole("group", { name: "Token" })).getByRole("button", { name: "Replace" }));
  fireEvent.change(input(form, "Token"), { target: { value: "tk_new" } });
  await save(form);
  expect(saves.at(-1)).toMatchObject({ values: { token: "tk_new" } });

  form = screen.getByRole("dialog", { name: "ntfy-api config" });
  fireEvent.click(within(within(form).getByRole("group", { name: "Token" })).getByRole("button", { name: "Clear" }));
  await save(form);
  expect(saves.at(-1)).toMatchObject({ clearSecrets: ["token"] });
  expect(saves.at(-1)?.values).not.toHaveProperty("token");
});

it("resets a field to its default by unsetting it", async () => {
  await boot();
  const form = await configure("ntfy-api");

  fireEvent.click(within(form).getByRole("button", { name: "Reset Priority" }));
  await save(form);

  expect(saves[0].values).not.toHaveProperty("priority");
  expect(input(form, "Priority").placeholder).toBe("3");
});

it("checks required, format and min/max before saving, showing each error under its field", async () => {
  await boot();
  const form = await configure("ntfy-api");

  fireEvent.change(input(form, "Topic"), { target: { value: "" } });
  fireEvent.change(input(form, "Server"), { target: { value: "ntfy.sh" } });
  fireEvent.change(input(form, "Priority"), { target: { value: "9" } });
  await save(form);

  expect(saves).toEqual([]);
  expect(within(form).getByText("is required").closest("[data-field]")?.getAttribute("data-field")).toBe("topic");
  expect(within(form).getByText("must be a URL like https://host.example").closest("[data-field]")?.getAttribute("data-field")).toBe("server");
  expect(within(form).getByText("must be at most 5").closest("[data-field]")?.getAttribute("data-field")).toBe("priority");
});

it("edits a table of rows and shows Start's errors under their cells", async () => {
  const detail = toBinary(ConfigErrorsSchema, create(ConfigErrorsSchema, { errors: [{ path: "hosts[1].url", message: "must use tcp://" }] }));
  saveAnswer = () =>
    Response.json(
      { code: "invalid_argument", message: "invalid config", details: [{ type: "ddash.core.ConfigErrors", value: btoa(String.fromCharCode(...detail)) }] },
      { status: 400 },
    );
  await boot();
  const form = await configure("docker-api");

  const table = within(form).getByRole("table", { name: "Docker hosts" });
  expect(within(table).getAllByRole("columnheader").map((h) => h.textContent)).toEqual(["Name", "Socket-proxy URL", ""]);
  fireEvent.click(within(form).getByRole("button", { name: "Add a row to Docker hosts" }));
  fireEvent.change(within(table).getByLabelText("Name, row 2"), { target: { value: "nas" } });
  fireEvent.change(within(table).getByLabelText("Socket-proxy URL, row 2"), { target: { value: "https://nas.home.example" } });
  await save(form);

  expect(saves[0]).toEqual({
    owner: "docker-api",
    values: { hosts: [{ name: "media", url: "tcp://198.18.2.10:2375" }, { name: "nas", url: "https://nas.home.example" }] },
  });
  expect(within(form).getByText("must use tcp://").closest("[data-field]")?.getAttribute("data-field")).toBe("hosts[1].url");

  fireEvent.click(within(table).getByRole("button", { name: "Remove row 1" }));
  expect((within(table).getByLabelText("Name, row 1") as HTMLInputElement).value).toBe("nas");
});

it("marks a Component needing config and opens its form with the stored config's errors", async () => {
  components[0] = { name: "ntfy-api", state: "unconfigured", configurable: true, needsConfig: true };
  forms["ntfy-api"] = { ...ntfyForm(), values: {}, errors: [{ path: "topic", message: "is required" }] } as never;
  await boot();

  expect(within(screen.getByRole("listitem", { name: "Docker" })).getByText("needs config")).toBeTruthy();
  const form = await configure("ntfy-api");

  expect(within(form).getByText("is required").closest("[data-field]")?.getAttribute("data-field")).toBe("topic");
});

it("opens the forms of the unconfigured Components an install adds, in turn", async () => {
  installAnswer = { components: ["ntfy-api", "checks", "docker-api"] };
  components = [
    { name: "ntfy-api", state: "unconfigured", configurable: true },
    { name: "checks", state: "ok" },
    { name: "docker-api", state: "unconfigured", configurable: true },
  ];
  await boot();

  fireEvent.click(within(screen.getByRole("listitem", { name: "Checks" })).getByRole("button", { name: "Install" }));
  await settle();
  await settle();

  expect(screen.getByRole("dialog", { name: "ntfy-api config" })).toBeTruthy();
  fireEvent.click(within(screen.getByRole("dialog")).getByRole("button", { name: "Close" }));
  await settle();
  expect(screen.getByRole("dialog", { name: "docker-api config" })).toBeTruthy();
  fireEvent.click(within(screen.getByRole("dialog")).getByRole("button", { name: "Close" }));
  await settle();
  expect(screen.queryByRole("dialog")).toBeNull();
});

it("keeps a form an install opened until it is closed, to show the health after saving", async () => {
  installAnswer = { components: ["ntfy-api"] };
  components = [{ name: "ntfy-api", state: "unconfigured", configurable: true }];
  saveAnswer = () => {
    components = [{ name: "ntfy-api", state: "ok", configurable: true }];
    return Response.json({ status: { name: "ntfy-api", state: "ok", configurable: true } });
  };
  await boot();
  fireEvent.click(within(screen.getByRole("listitem", { name: "Checks" })).getByRole("button", { name: "Install" }));
  await settle();
  await settle();

  await save(screen.getByRole("dialog", { name: "ntfy-api config" }));
  await settle();

  expect(within(screen.getByRole("dialog", { name: "ntfy-api config" })).getByRole("status").textContent).toMatch(/ok/);
});

it("shows a started Component's setup hint, with a button copying its block", async () => {
  const line = 'restrict,from="198.18.1.5",command="/usr/sbin/nginx -T" ssh-ed25519 AAAAC3Nza ddash';
  forms["docker-api"] = { ...dockerForm(), setupHint: { text: "Add this line to root's authorized_keys.", block: line } } as ReturnType<typeof dockerForm>;
  const writeText = vi.fn<(text: string) => Promise<void>>(async () => {});
  Object.defineProperty(navigator, "clipboard", { value: { writeText }, configurable: true });
  await boot();

  const form = await configure("docker-api");

  const hint = within(form).getByRole("region", { name: "Setup" });
  expect(hint.textContent).toContain("Add this line to root's authorized_keys.");
  expect(within(hint).getByText(line).tagName).toBe("PRE");
  fireEvent.click(within(hint).getByRole("button", { name: "Copy" }));
  await settle();
  expect(writeText).toHaveBeenCalledWith(line);
  expect(within(hint).getByRole("button", { name: "Copied" })).toBeTruthy();
});

it("shows no setup hint for a Component without one", async () => {
  await boot();

  const form = await configure("ntfy-api");

  expect(within(form).queryByRole("region", { name: "Setup" })).toBeNull();
});
